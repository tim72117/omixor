// 右側的 AI 維運助理:依目前頁面給出狀況摘要、建議問題、對話與可直接執行的動作
// (Swift 的 AssistantPanel、AssistChat;回答由後端的 assistant_briefing / assistant_ask 產生)
import { useEffect, useRef, useState } from "react";
import { action, tool, useVersion, useView } from "../api";
import { AutoTextarea } from "../components/AutoTextarea";
import { cardClass } from "../components/Card";
import { ErrorText } from "../components/ErrorText";
import { Icon } from "../components/Icon";
import { IconButton, iconButtonClass } from "../components/IconButton";
import { Markdown } from "../components/Markdown";
import { copyText } from "../lib/clipboard";
import { color } from "../lib/color";
import { go } from "../lib/nav";
import { RISK_COLOR, RISK_LABEL, arr, type Approval } from "../models";
import s from "./AssistantPanel.module.css";
import { SECTIONS, type Nav, type SectionId } from "../types";
import { useAssistantVisible } from "./store";
import { canGo } from "../features";
import { AIAvatar } from "../components/Avatar";
import { rowButtonClass } from "../components/RowButton";

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
          await tool("alert_update", { alert_id: a.alertId, action: "acknowledge" });
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
          await tool("pipeline_update", { run_id: a.runId, action: "retry" });
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
  const sec = SECTIONS.find((x) => x.id === section);
  const pending = approvals.filter((a) => a.state === "pending").sort((a, b) => a.expiresAt.localeCompare(b.expiresAt));
  const focusLabel = brief?.focusLabel;

  return (
    <aside className={s.assist}>
      <div className={"col gap8 " + s.head}>
        <div className="row gap8">
          <AIAvatar size={24} />
          <span className={"h3 grow " + s.title}>AI 助理</span>
          <IconButton icon="trash" size={13} title="清除對話" disabled={!msgs.length} onClick={clear} />
          <IconButton icon="sidebar.trailing" title="隱藏 AI 助理(⌘J)" onClick={() => setVisible(false)} />
        </div>
        <span className={"row gap4 " + s.context} title="助理會依照目前的頁面回答">
          <Icon name={sec?.icon} size={12} />
          <span className={s.contextLabel}>正在看:{brief?.sectionTitle ?? sec?.title}</span>
          {focusLabel && <><span className="muted">·</span><span className="ellipsis">{focusLabel}</span></>}
        </span>
      </div>
      <div className={s.scroll}>
        <div className={s.soft}>
          <div className="row"><span className={"muted grow " + s.bold}>目前狀況</span>
            <button className={"btn link " + s.indigoLink} onClick={() => ask("總結目前狀況")}>詳細說明</button></div>
          {brief ? (
            <>
              <span className="row gap6"><span className={s.toneDot} style={{ background: color(brief.tone) }} /><span className={s.headline}>{brief.headline}</span></span>
              <div className={"col " + s.lines}>
                {arr<Line>(brief.lines).map((l, i) => (
                  <span key={i} className={"row gap6 " + s.line}><span className={s.lineIcon}><Icon name={l.icon} size={12} color={color(l.color)} /></span><span>{l.text}</span></span>
                ))}
              </div>
            </>
          ) : briefErr ? <ErrorText>{briefErr}</ErrorText> : <span className="muted">讀取中…</span>}
        </div>
        <div className={cardClass + " " + s.pending}>
          <div className="row gap6">
            <span className={"row gap4 " + s.pendingTitle + (pending.length ? " " + s.has : "")}><Icon name="hand.raised.fill" size={12} />待你決定</span>
            {pending.length > 0 && <span className={"num " + s.badge}>{pending.length}</span>}
            <div className="grow" />
            {canGo("automation") && <button className={"btn link " + s.indigoLink} onClick={() => go("automation")}>全部查看</button>}
          </div>
          {pending.length === 0 ? <span className="muted">沒有等待核准的 AI 請求</span> : pending.slice(0, 3).map((p) => (
            <button key={p.id} className={rowButtonClass + " " + s.padY2} title={`前往 AI 自動化查看「${p.title}」`} onClick={canGo("automation") ? () => go("automation", p.id) : undefined}>
              <span className={s.riskDot} style={{ background: color(RISK_COLOR[p.risk]) }} />
              <span className="ellipsis grow">{p.title}</span>
              <span className={s.none} style={{ color: color(RISK_COLOR[p.risk]) }}>{RISK_LABEL[p.risk]}</span>
              <Icon name="chevron.right" size={10} className="faint" />
            </button>
          ))}
          {pending.length > 3 && <span className={"muted " + s.xs}>還有 {pending.length - 3} 個</span>}
        </div>
        {brief && arr<string>(brief.suggestions).length > 0 && (
          <div className="col gap6">
            <span className={"muted " + s.bold}>可以問我</span>
            <div className="row wrap gap6">{brief.suggestions.map((p) => <button key={p} className={s.prompt} disabled={busy} onClick={() => ask(p)}>{p}</button>)}</div>
          </div>
        )}
        {msgs.length > 0 && (
          <div className="row gap8"><div className="divider grow" /><span className={"faint " + s.xs}>對話</span><div className="divider grow" /></div>
        )}
        {msgs.map((m) => m.role === "user" ? <div key={m.id} className={s.user}>{m.text}</div> : (
          <div key={m.id} className={s.bot}>
            <AIAvatar size={20} />
            <div className={s.botBody}>
              {m.thinking ? (
                <span className="row gap6"><span className={s.dots}><span /><span /><span /></span><span className="muted">分析目前資料中…</span></span>
              ) : <div className={s.selectable}><Markdown text={m.text} inlineOnly /></div>}
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
      <div className={s.composer}>
        <div className={s.input}>
          <AutoTextarea ref={inputRef} className={s.field} minRows={1} maxRows={5} value={draft} placeholder="問問目前的狀況…" onChange={setDraft}
            onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send(); } }} />
          <button className={iconButtonClass.normal} title="送出(Return)" disabled={!draft.trim() || busy} onClick={send}>
            <Icon name="arrow.up.circle.fill" size={20} color={!draft.trim() || busy ? "var(--fg3)" : "var(--indigo)"} />
          </button>
        </div>
        <span className={"row gap4 faint " + s.xs}><Icon name="info.circle" size={11} />目前為模擬回應,尚未連接模型</span>
      </div>
    </aside>
  );
}
