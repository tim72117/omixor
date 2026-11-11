// 右側的 AI 維運助理:依目前頁面給出狀況摘要、建議問題、對話與可直接執行的動作
// (Swift 的 AssistantPanel、AssistChat;回答由後端的 assistant_briefing / assistant_ask 產生)
import { useEffect, useRef, useState } from "react";
import { action, tool, useVersion, useView } from "../api";
import { Markdown, copyText, go } from "../components/bits";
import { Icon } from "../components/Icon";
import { RISK_COLOR, RISK_LABEL, arr, type Approval } from "../components/models";
import { color } from "../components/ui";
import { SECTIONS, type Nav, type SectionId } from "../types";
import { useAssistantVisible } from "./store";

type Line = { icon: string; color: string; text: string };
type Briefing = { headline: string; tone: string; lines: Line[]; suggestions: string[]; focusLabel?: string | null; section?: string; sectionTitle?: string };
type AssistAction = {
  kind: "acknowledge" | "approve" | "reject" | "retry" | "go" | "copy"; key: string; label: string; icon: string; prominent?: boolean;
  alertId?: string; title?: string; approvalId?: string; runId?: number; section?: SectionId; focus?: string | null; text?: string;
};
type Msg = { id: string; role: "user" | "assistant"; text: string; actions: AssistAction[]; thinking?: boolean; used: Set<string> };

const uid = () => crypto.randomUUID();

export function AssistantPanel() {
  const [nav] = useView<Nav>("nav");
  const [apvRaw] = useView<Approval[]>("approvals");
  const [alertsRaw] = useView<{ id: string; state: string }[]>("alerts");
  const version = useVersion(10000);
  const [, setVisible] = useAssistantVisible();
  const section = nav?.section ?? "overview";
  const [brief, setBrief] = useState<Briefing>();
  const [briefErr, setBriefErr] = useState<string>();
  const [msgs, setMsgs] = useState<Msg[]>([]);
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState("");
  const cancel = useRef(0);
  const bottom = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  // 換頁、換焦點或資料變更時重讀「目前狀況」(合併短時間內的多次變更)
  useEffect(() => {
    let alive = true;
    const t = window.setTimeout(() => {
      action<Briefing>("assistant_briefing", { section })
        .then((b) => { if (alive) { setBrief(b); setBriefErr(undefined); } })
        .catch((e) => { if (alive) setBriefErr(String(e?.message ?? e)); });
    }, 150);
    return () => { alive = false; window.clearTimeout(t); };
  }, [section, nav?.focusId, version]);

  useEffect(() => { bottom.current?.scrollIntoView({ block: "end" }); }, [msgs]);

  const update = (id: string, f: (m: Msg) => Msg) => setMsgs((ms) => ms.map((m) => (m.id === id ? f(m) : m)));

  const ask = async (raw: string) => {
    const q = raw.trim();
    if (!q || busy) return;
    const run = ++cancel.current;
    const id = uid();
    setMsgs((ms) => [...ms, { id: uid(), role: "user", text: q, actions: [], used: new Set() }, { id, role: "assistant", text: "", actions: [], thinking: true, used: new Set() }]);
    setBusy(true);
    // 模擬思考時間,長一點的問題多想一下;等思考完才讀資料,回答反映當下的狀態
    await new Promise((r) => setTimeout(r, 550 + Math.min(q.length, 30) * 15));
    if (run !== cancel.current) return;
    let reply: { text: string; actions?: AssistAction[] };
    try {
      reply = await action("assistant_ask", { section, question: q });
    } catch (e: any) {
      reply = { text: `抱歉,現在讀不到資料:${e?.message ?? e}` };
    }
    if (run !== cancel.current) return;
    update(id, (m) => ({ ...m, thinking: false }));
    const chars = Array.from(reply.text ?? "");
    const step = chars.length > 240 ? 4 : 2;
    for (let shown = 0; shown < chars.length;) {
      if (run !== cancel.current) return;
      shown = Math.min(chars.length, shown + step);
      const partial = chars.slice(0, shown).join("");
      update(id, (m) => ({ ...m, text: partial }));
      await new Promise((r) => setTimeout(r, 14));
    }
    update(id, (m) => ({ ...m, actions: Array.isArray(reply.actions) ? reply.actions : [] }));
    setBusy(false);
  };

  const note = (text: string) => setMsgs((ms) => [...ms, { id: uid(), role: "assistant", text, actions: [], used: new Set() }]);
  const markUsed = (mid: string, ...keys: string[]) => update(mid, (m) => ({ ...m, used: new Set([...m.used, ...keys]) }));
  const approvals = arr<Approval>(apvRaw);
  const alerts = arr<{ id: string; state: string }>(alertsRaw);

  const available = (a: AssistAction, m: Msg) => {
    if (m.used.has(a.key)) return false;
    if (a.kind === "acknowledge") return alerts.find((x) => x.id === a.alertId)?.state === "firing";
    if (a.kind === "approve" || a.kind === "reject") return approvals.find((x) => x.id === a.approvalId)?.state === "pending";
    return true;
  };

  const perform = async (a: AssistAction, m: Msg) => {
    try {
      switch (a.kind) {
        case "acknowledge":
          await tool("ops_alert_update", { alert_id: a.alertId, action: "acknowledge" });
          markUsed(m.id, a.key);
          note(`已確認告警「${a.title ?? a.alertId}」,並記錄到動態牆。`);
          break;
        case "approve":
        case "reject": {
          const title = approvals.find((x) => x.id === a.approvalId)?.title ?? a.approvalId;
          await action("approval_decide", { id: a.approvalId, approve: a.kind === "approve" });
          const pair = m.actions.filter((x) => (x.kind === "approve" || x.kind === "reject") && x.approvalId === a.approvalId).map((x) => x.key);
          markUsed(m.id, a.key, ...pair);
          note(a.kind === "approve" ? `已核准「${title}」,AI 會開始執行並在完成後回報。` : `已拒絕「${title}」。`);
          break;
        }
        case "retry":
          await tool("ops_pipeline_update", { run_id: a.runId, action: "retry" });
          markUsed(m.id, a.key);
          note(`已重新執行 #${a.runId},可以到 CI/CD 頁看進度。`);
          break;
        case "go":
          if (a.section) go(a.section, a.focus ?? undefined);
          break;
        case "copy":
          await copyText(a.text ?? "");
          markUsed(m.id, a.key);
          break;
      }
    } catch (e: any) {
      note(`沒辦法完成:${e?.message ?? e}`);
    }
  };

  const clear = () => { cancel.current++; setBusy(false); setMsgs([]); };
  const send = () => {
    if (busy || !draft.trim()) return;
    const q = draft;
    setDraft("");
    ask(q);
    inputRef.current?.focus();
  };
  const sec = SECTIONS.find((s) => s.id === section);
  const pending = approvals.filter((a) => a.state === "pending").sort((a, b) => a.expiresAt.localeCompare(b.expiresAt));
  const focusLabel = brief?.focusLabel;

  return (
    <aside className="assist">
      <div className="col gap8" style={{ padding: "10px 12px", borderBottom: "1px solid var(--line)" }}>
        <div className="row gap8">
          <span className="bx-avatar bx-ai" style={{ width: 24, height: 24 }}><Icon name="sparkles" size={12} color="white" /></span>
          <span className="h3 grow" style={{ fontSize: 14 }}>AI 助理</span>
          <button className="bx-icon-btn" title="清除對話" disabled={!msgs.length} onClick={clear}><Icon name="trash" size={13} /></button>
          <button className="bx-icon-btn" title="隱藏 AI 助理(⌘J)" onClick={() => setVisible(false)}><Icon name="sidebar.trailing" size={14} /></button>
        </div>
        <span className="row gap4" title="助理會依照目前的頁面回答" style={{ alignSelf: "flex-start", maxWidth: "100%", color: "var(--indigo)", padding: "4px 8px", borderRadius: 99, background: "color-mix(in srgb, var(--indigo) 10%, transparent)" }}>
          <Icon name={sec?.icon} size={12} />
          <span style={{ fontWeight: 500, whiteSpace: "nowrap" }}>正在看:{brief?.sectionTitle ?? sec?.title}</span>
          {focusLabel && <><span className="muted">·</span><span className="ellipsis">{focusLabel}</span></>}
        </span>
      </div>
      <div className="assist-scroll">
        <div className="assist-soft">
          <div className="row"><span className="muted grow" style={{ fontWeight: 600 }}>目前狀況</span>
            <button className="btn link" style={{ color: "var(--indigo)" }} onClick={() => ask("總結目前狀況")}>詳細說明</button></div>
          {brief ? (
            <>
              <span className="row gap6"><span className="dot" style={{ width: 8, height: 8, background: color(brief.tone) }} /><span style={{ fontWeight: 600, fontSize: 13.5 }}>{brief.headline}</span></span>
              <div className="col" style={{ gap: 5 }}>
                {arr<Line>(brief.lines).map((l, i) => (
                  <span key={i} className="row gap6" style={{ alignItems: "baseline" }}><span style={{ width: 14, flex: "none" }}><Icon name={l.icon} size={12} color={color(l.color)} /></span><span>{l.text}</span></span>
                ))}
              </div>
            </>
          ) : briefErr ? <span className="error-text"><Icon name="exclamationmark.triangle" size={12} />{briefErr}</span> : <span className="muted">讀取中…</span>}
        </div>
        <div className="card" style={{ padding: 11, gap: 6 }}>
          <div className="row gap6">
            <span className="row gap4" style={{ fontWeight: 600, color: pending.length ? "var(--purple)" : "var(--fg2)" }}><Icon name="hand.raised.fill" size={12} />待你決定</span>
            {pending.length > 0 && <span className="num" style={{ fontSize: 11, fontWeight: 700, color: "white", background: "var(--purple)", borderRadius: 99, padding: "1px 6px" }}>{pending.length}</span>}
            <div className="grow" />
            <button className="btn link" style={{ color: "var(--indigo)" }} onClick={() => go("automation")}>全部查看</button>
          </div>
          {pending.length === 0 ? <span className="muted">沒有等待核准的 AI 請求</span> : pending.slice(0, 3).map((p) => (
            <button key={p.id} className="bx-row-btn" style={{ padding: "2px 0" }} title={`前往 AI 自動化查看「${p.title}」`} onClick={() => go("automation", p.id)}>
              <span className="dot" style={{ width: 6, height: 6, background: color(RISK_COLOR[p.risk]) }} />
              <span className="ellipsis grow">{p.title}</span>
              <span style={{ color: color(RISK_COLOR[p.risk]), flex: "none" }}>{RISK_LABEL[p.risk]}</span>
              <Icon name="chevron.right" size={10} className="faint" />
            </button>
          ))}
          {pending.length > 3 && <span className="muted" style={{ fontSize: 11 }}>還有 {pending.length - 3} 個</span>}
        </div>
        {brief && arr<string>(brief.suggestions).length > 0 && (
          <div className="col gap6">
            <span className="muted" style={{ fontWeight: 600 }}>可以問我</span>
            <div className="row wrap gap6">{brief.suggestions.map((p) => <button key={p} className="assist-prompt" disabled={busy} onClick={() => ask(p)}>{p}</button>)}</div>
          </div>
        )}
        {msgs.length > 0 && (
          <div className="row gap8"><div className="divider grow" /><span className="faint" style={{ fontSize: 11 }}>對話</span><div className="divider grow" /></div>
        )}
        {msgs.map((m) => m.role === "user" ? <div key={m.id} className="assist-user">{m.text}</div> : (
          <div key={m.id} className="assist-bot">
            <span className="bx-avatar bx-ai" style={{ width: 20, height: 20 }}><Icon name="sparkles" size={10} color="white" /></span>
            <div className="assist-bot-body">
              {m.thinking ? (
                <span className="row gap6"><span className="assist-dots"><span /><span /><span /></span><span className="muted">分析目前資料中…</span></span>
              ) : <div style={{ userSelect: "text" }}><Markdown text={m.text} inlineOnly /></div>}
              {m.actions.length > 0 && (
                <div className="row wrap gap6">
                  {m.actions.map((a) => {
                    const ok = available(a, m);
                    const copied = !ok && a.kind === "copy";
                    return (
                      <button key={a.key} className={"btn small" + (a.prominent ? " indigo" : "")} disabled={!ok} onClick={() => perform(a, m)}>
                        <Icon name={copied ? "checkmark" : a.icon} size={11} />{copied ? "已複製" : a.label}
                      </button>
                    );
                  })}
                </div>
              )}
            </div>
          </div>
        ))}
        <div ref={bottom} />
      </div>
      <div className="assist-composer">
        <div className="assist-input">
          <textarea ref={inputRef} rows={1} value={draft} placeholder="問問目前的狀況…" onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send(); } }} />
          <button className="bx-icon-btn" title="送出(Return)" disabled={!draft.trim() || busy} onClick={send}>
            <Icon name="arrow.up.circle.fill" size={20} color={!draft.trim() || busy ? "var(--fg3)" : "var(--indigo)"} />
          </button>
        </div>
        <span className="row gap4 faint" style={{ fontSize: 11 }}><Icon name="info.circle" size={11} />目前為模擬回應,尚未連接模型</span>
      </div>
    </aside>
  );
}
