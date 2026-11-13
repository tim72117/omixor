// 右側事件詳情:頁首、狀態步驟、影響、AI 摘要、時間軸、受影響的服務、相關告警、後續工作、檢討報告
// (Swift 的 IncDetailView、IncTimeline、IncPostmortem)
import { Fragment, useEffect, useRef, useState } from "react";
import { action, tool, useRun } from "../../api";
import { AIBadge, ActorAvatar, ActorLabel, Flow, copyText, go, hm, useCopied, useDirectory, useNow } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { INC_STATUSES, INC_STATUS_COLOR, INC_STATUS_ICON, INC_STATUS_LABEL, SEV_COLOR, ALERT_STATE_LABEL, WORK_STATUS_LABEL, type Incident, type IncidentEvent, type IncidentStatus } from "../../components/models";
import { Card, EmptyState, ErrorText, MetricTile, Modal, Pill, Segmented, color } from "../../components/ui";
import { canGo } from "../../features";
import {
  KIND_COLOR, KIND_ICON, KIND_LABEL, ServiceChip, SevBadge, StatusPill, ackDelay, aiSummary, durationOf, fmtDateTime, fmtDay, fmtDuration, fmtShort,
  isAI, isActive, isKey, isPerson, nextStatus, postmortem, postmortemMarkdown, sevLabel, sortedTimeline, stepOf, t, useInc,
} from "./incSupport";

export function IncDetail({ id }: { id: string }) {
  const { incidents } = useInc();
  const inc = incidents.find((i) => i.id === id);
  const [tab, setTab] = useState<"overview" | "postmortem">("overview");
  const ref = useRef<HTMLDivElement>(null);
  const [wide, setWide] = useState(true);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWide(el.clientWidth >= 780));
    ro.observe(el);
    return () => ro.disconnect();
  }, [inc ? 1 : 0]);
  if (!inc) return <EmptyState title="找不到事件" icon="questionmark.circle" />;
  return (
    <div ref={ref} className="page-inner" style={{ maxWidth: 1100 }}>
      <Header inc={inc} />
      <Stepper inc={inc} />
      {inc.status === "resolved" && (
        <div style={{ maxWidth: 280 }}><Segmented value={tab} onChange={setTab} options={[{ value: "overview", label: "事件詳情" }, { value: "postmortem", label: "檢討報告" }]} /></div>
      )}
      {inc.status === "resolved" && tab === "postmortem" ? <PostmortemView inc={inc} /> : wide ? (
        <div className="row" style={{ alignItems: "flex-start", gap: 16 }}>
          <div className="col grow" style={{ gap: 16 }}><Impact inc={inc} /><AISummary inc={inc} /><Timeline inc={inc} /></div>
          <div className="col" style={{ gap: 16, width: 290, flex: "none" }}><Services inc={inc} /><RelatedAlerts inc={inc} /><FollowUps inc={inc} /></div>
        </div>
      ) : (
        <div className="col" style={{ gap: 16 }}><Impact inc={inc} /><AISummary inc={inc} /><Services inc={inc} /><Timeline inc={inc} /><RelatedAlerts inc={inc} /><FollowUps inc={inc} /></div>
      )}
    </div>
  );
}

function Header({ inc }: { inc: Incident }) {
  const now = useNow(30000);
  const ack = ackDelay(inc);
  const meta = (label: string, value: string, icon: string) => (
    <span className="row gap4"><Icon name={icon} size={11} className="muted" /><span className="caption muted">{label}</span><span className="num" style={{ fontWeight: 500 }}>{value}</span></span>
  );
  return (
    <div className="col gap8" style={{ gap: 10 }}>
      <div className="row gap8">
        <SevBadge sev={inc.sev} large />
        <span className="mono muted" style={{ userSelect: "text" }}>{inc.id}</span>
        <StatusPill status={inc.status} />
        <div className="grow" />
        <button className="bx-icon-btn" title="複製事件標題" onClick={() => copyText(`${inc.id} ${sevLabel(inc.sev)} ${inc.title}(${INC_STATUS_LABEL[inc.status]})`)}><Icon name="doc.on.doc" size={13} /></button>
      </div>
      <div className="h2" style={{ fontSize: 20, userSelect: "text" }}>{inc.title}</div>
      <Flow gap={18}>
        {meta("開始", fmtDateTime(inc.startedAt), "clock")}
        {meta(isActive(inc) ? "已持續" : "總時長", fmtDuration(durationOf(inc, now)), "timer")}
        {inc.resolvedAt && meta("解決", fmtDateTime(inc.resolvedAt), "checkmark.circle")}
        <span className="row gap6"><span className="caption muted">指揮官</span><span style={{ fontWeight: 500 }}><ActorLabel actor={{ kind: "person", id: inc.commander }} /></span></span>
        {ack != null && meta("首次回應", fmtDuration(ack), "hand.raised")}
      </Flow>
    </div>
  );
}

function Stepper({ inc }: { inc: Incident }) {
  const [pending, setPending] = useState<IncidentStatus>();
  const [note, setNote] = useState("");
  const { busy, error, run } = useRun();
  const cur = stepOf(inc.status);
  const next = nextStatus(inc.status);
  const confirm = () => run(async () => {
    if (!pending) return;
    const tt = note.trim();
    await tool("incident_update", { incident_id: inc.id, status: pending, ...(tt ? { note: `狀態改為「${INC_STATUS_LABEL[pending]}」:${tt}` } : {}) });
    setPending(undefined);
  });
  return (
    <div className="card row wrap" style={{ gap: 14, borderRadius: 12, padding: 12, flexDirection: "row", alignItems: "center" }}>
      <div className="row grow" style={{ minWidth: 0, overflowX: "auto" }}>
        {INC_STATUSES.map((s, i) => {
          const c = color(INC_STATUS_COLOR[s]);
          return (
            <Fragment key={s}>
              {i > 0 && <div style={{ flex: 1, minWidth: 12, height: 2, background: i <= cur ? `color-mix(in srgb, ${c} 80%, transparent)` : "color-mix(in srgb, var(--fg) 12%, transparent)" }} />}
              <button className="row gap6" style={{ border: 0, background: "none", padding: "0 4px" }} title={s === inc.status ? "目前狀態" : `把狀態改為「${INC_STATUS_LABEL[s]}」`}
                onClick={() => { if (s !== inc.status) { setPending(s); setNote(""); } }}>
                <span style={{ width: 22, height: 22, borderRadius: 22, display: "inline-flex", alignItems: "center", justifyContent: "center", flex: "none",
                  background: i <= cur ? c : "transparent", border: `1.5px solid ${i <= cur ? c : "color-mix(in srgb, var(--fg) 25%, transparent)"}` }}>
                  {i < cur ? <Icon name="checkmark" size={11} color="white" /> : i === cur ? <Icon name={INC_STATUS_ICON[s]} size={11} color="white" /> : <span className="muted" style={{ fontSize: 10, fontWeight: 600 }}>{i + 1}</span>}
                </span>
                <span style={{ whiteSpace: "nowrap", fontWeight: i === cur ? 600 : 400, color: i === cur ? c : i < cur ? undefined : "var(--fg2)" }}>{INC_STATUS_LABEL[s]}</span>
              </button>
            </Fragment>
          );
        })}
      </div>
      {next && (
        <button className="btn small" style={{ background: color(INC_STATUS_COLOR[next]), borderColor: color(INC_STATUS_COLOR[next]), color: "white" }} onClick={() => { setPending(next); setNote(""); }}>
          <Icon name="arrow.right.circle.fill" size={12} />推進到{INC_STATUS_LABEL[next]}
        </button>
      )}
      <Modal open={!!pending} onClose={() => setPending(undefined)} width={400} title={pending ? `把狀態改為「${INC_STATUS_LABEL[pending]}」?` : ""}>
        <span className="muted">{pending === "resolved" ? "解決後會開始計算 MTTR,並可產生檢討報告。" : "變更會記錄在時間軸上。"}</span>
        <input className="input" autoFocus placeholder="補充說明(選填)" value={note} onChange={(e) => setNote(e.target.value)} onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && confirm()} />
        <ErrorText>{error}</ErrorText>
        <div className="row gap8" style={{ justifyContent: "flex-end" }}>
          <button className="btn" onClick={() => setPending(undefined)}>取消</button>
          <button className="btn primary" disabled={busy} onClick={confirm}>{pending === "resolved" ? "標記為已解決" : "更新狀態"}</button>
        </div>
      </Modal>
    </div>
  );
}

const Impact = ({ inc }: { inc: Incident }) => (
  <Card title="影響範圍"><span style={{ color: inc.impact ? undefined : "var(--fg2)", userSelect: "text", lineHeight: 1.5 }}>{inc.impact || "尚未描述影響範圍。"}</span></Card>
);

function AISummary({ inc }: { inc: Incident }) {
  const { services } = useInc();
  const dir = useDirectory();
  const { error, run } = useRun();
  const draft = aiSummary(inc, services, dir);
  return (
    <div className="col gap8" style={{ padding: 14, borderRadius: 12, background: "linear-gradient(135deg, color-mix(in srgb, var(--purple) 12%, transparent), color-mix(in srgb, var(--indigo) 6%, transparent))", border: "1px solid color-mix(in srgb, var(--purple) 25%, transparent)" }}>
      <div className="row gap6">
        <Icon name="sparkles" size={14} color="var(--purple)" />
        <span className="h3" style={{ fontSize: 13.5 }}>AI 摘要</span>
        <div className="grow" />
        <span className="caption" style={{ color: "color-mix(in srgb, var(--purple) 80%, transparent)" }}>由 AI 依時間軸整理</span>
        <button className="bx-icon-btn" title="依最新時間軸重新整理" onClick={() => run(() => tool("incident_update", { incident_id: inc.id, ai_summary: draft }))}><Icon name="arrow.clockwise" size={13} /></button>
      </div>
      <div style={{ whiteSpace: "pre-wrap", lineHeight: 1.6, userSelect: "text" }}>{inc.aiSummary ?? draft}</div>
      {!inc.aiSummary && <span className="caption muted">尚未儲存摘要,以上為即時草稿。</span>}
      <ErrorText>{error}</ErrorText>
    </div>
  );
}

function Services({ inc }: { inc: Incident }) {
  return (
    <Card title="受影響的服務" subtitle={inc.services.length ? "點選查看服務狀態" : undefined}>
      {inc.services.length === 0 ? <span className="muted">未指定</span> : <Flow>{inc.services.map((s) => <ServiceChip key={s} id={s} />)}</Flow>}
    </Card>
  );
}

function RelatedAlerts({ inc }: { inc: Incident }) {
  const { alerts } = useInc();
  const lo = t(inc.startedAt) - 3600000, hi = inc.resolvedAt ? t(inc.resolvedAt) : Date.now();
  const list = alerts
    .filter((a) => a.incidentId === inc.id || (!a.incidentId && inc.services.includes(a.service) && t(a.startedAt) >= lo && t(a.startedAt) <= hi))
    .sort((a, b) => a.startedAt.localeCompare(b.startedAt));
  return (
    <Card title="相關告警" subtitle={list.length ? `${list.length} 則` : undefined}>
      {list.length === 0 ? <span className="muted">沒有關聯的告警</span> : (
        <div className="col" style={{ gap: 2 }}>
          {list.map((a) => (
            <button key={a.id} className="bx-row-btn" style={{ alignItems: "flex-start" }} title="到「監控與告警」查看" onClick={canGo("monitoring") ? () => go("monitoring", a.id) : undefined}>
              <Icon name={a.state === "firing" ? "bell.badge.fill" : "bell"} size={13} color={color(SEV_COLOR[a.severity])} />
              <span className="col grow" style={{ gap: 2 }}>
                <span>{a.title}</span>
                <span className="caption muted">{ALERT_STATE_LABEL[a.state]} · {hm(a.startedAt)} · <span className="num">{a.value}</span></span>
              </span>
              <Icon name="chevron.right" size={10} className="faint" />
            </button>
          ))}
        </div>
      )}
    </Card>
  );
}

function FollowUps({ inc }: { inc: Incident }) {
  const { workItems } = useInc();
  const dir = useDirectory();
  const [adding, setAdding] = useState(false);
  const [title, setTitle] = useState("");
  const { busy, error, run } = useRun();
  const items = inc.followUps.map((id) => workItems.find((w) => w.id === id)).filter((w): w is NonNullable<typeof w> => !!w);
  const add = () => run(async () => {
    const tt = title.trim();
    if (!tt) return;
    await tool("work_item_create", { title: tt, type: "incident", status: "todo", incident_id: inc.id, points: 2, ...(dir.me ? { assignee: dir.me } : {}), ...(inc.services[0] ? { service: inc.services[0] } : {}) });
    setTitle(""); setAdding(false);
  });
  return (
    <Card title="後續工作" subtitle={items.length ? `${items.filter((w) => w.status === "done").length}/${items.length} 完成` : undefined}>
      <div className="col gap4">
        {items.length === 0 && !adding && <span className="muted">還沒有後續工作</span>}
        {items.map((w) => (
          <button key={w.id} className="bx-row-btn" title={`到「開發進度」查看 ${w.id}`} onClick={canGo("development") ? () => go("development", w.id) : undefined}>
            <Icon name={w.status === "done" ? "checkmark.circle.fill" : "circle"} size={13} color={w.status === "done" ? "var(--green)" : "var(--fg2)"} />
            <span className="col grow" style={{ gap: 1 }}>
              <span style={{ textDecoration: w.status === "done" ? "line-through" : undefined }}>{w.title}</span>
              <span className="caption muted"><span className="mono">{w.id}</span> · {WORK_STATUS_LABEL[w.status]}{w.assignee ? ` · ${dir.name(w.assignee)}` : ""}</span>
            </span>
            {w.createdByAI && <AIBadge />}
          </button>
        ))}
        {adding ? (
          <div className="row gap6">
            <input className="input grow" autoFocus placeholder="工作標題" value={title} onChange={(e) => setTitle(e.target.value)} onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && add()} />
            <button className="btn small" disabled={!title.trim() || busy} onClick={add}>新增</button>
            <button className="btn small" onClick={() => { setAdding(false); setTitle(""); }}>取消</button>
          </div>
        ) : <button className="btn link" style={{ alignSelf: "flex-start", marginTop: 4 }} onClick={() => setAdding(true)}><Icon name="plus" size={12} />新增後續工作</button>}
        <ErrorText>{error}</ErrorText>
      </div>
    </Card>
  );
}

// MARK: - 時間軸

type Filter = "all" | "people" | "ai" | "key";

function Timeline({ inc }: { inc: Incident }) {
  const dir = useDirectory();
  const [filter, setFilter] = useState<Filter>("all");
  const [newest, setNewest] = useState(false);
  const [draft, setDraft] = useState("");
  const { busy, error, run } = useRun();
  const base = sortedTimeline(inc).filter((e) => filter === "all" || (filter === "people" ? isPerson(e.actor) : filter === "ai" ? isAI(e.actor) || e.kind === "aiFinding" : isKey(e.kind)));
  const events = newest ? [...base].reverse() : base;
  const submit = () => run(async () => {
    const tt = draft.trim();
    if (!tt) return;
    await action("incident_note", { id: inc.id, text: tt });
    setDraft("");
  });
  return (
    <Card>
      <div className="row gap8">
        <span className="h3" style={{ fontSize: 13.5 }}>時間軸</span>
        <span className="caption muted">{inc.timeline.length} 筆</span>
        <div className="grow" />
        <select className="select" value={filter} onChange={(e) => setFilter(e.target.value as Filter)}>
          <option value="all">全部</option><option value="people">人員</option><option value="ai">AI</option><option value="key">重要節點</option>
        </select>
        <button className="bx-icon-btn" title={newest ? "目前最新在上" : "目前依時間先後"} onClick={() => setNewest(!newest)}><Icon name={newest ? "arrow.up" : "arrow.down"} size={13} /></button>
      </div>
      <div className="row gap12 muted" style={{ fontSize: 10.5 }}>
        <span className="row gap4"><span className="dot" style={{ background: "var(--accent)" }} />人員</span>
        <span className="row gap4"><span className="dot" style={{ background: "var(--purple)" }} />AI</span>
        <span className="row gap4"><span className="dot" style={{ background: "var(--gray)" }} />系統</span>
      </div>
      {events.length === 0 ? (
        <div className="muted" style={{ textAlign: "center", padding: "16px 0" }}>{inc.timeline.length ? "這個篩選條件下沒有紀錄。" : "時間軸還沒有任何紀錄。"}</div>
      ) : (
        <div className="col">
          {events.map((e, i) => (
            <TimelineRow key={e.id} e={e} start={inc.startedAt} first={i === 0} last={i === events.length - 1}
              showDay={i === 0 || new Date(e.date).toDateString() !== new Date(events[i - 1].date).toDateString()} />
          ))}
        </div>
      )}
      <div className="row gap8" style={{ alignItems: "flex-end", paddingTop: 4 }}>
        <ActorAvatar actor={{ kind: "person", id: dir.me }} size={24} />
        <textarea className="input grow" rows={1} style={{ resize: "vertical", borderRadius: 8 }} value={draft}
          placeholder="新增備註,例如目前處置或觀察…(⌘↩ 送出)" onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter" && !e.nativeEvent.isComposing && (e.metaKey || e.ctrlKey)) { e.preventDefault(); submit(); } }} />
        <button className="btn primary" disabled={!draft.trim() || busy} onClick={submit}><Icon name="paperplane.fill" size={12} />送出</button>
      </div>
      <ErrorText>{error}</ErrorText>
    </Card>
  );
}

function TimelineRow({ e, start, first, last, showDay }: { e: IncidentEvent; start: string; first: boolean; last: boolean; showDay: boolean }) {
  const dir = useDirectory();
  const ai = isAI(e.actor) || e.kind === "aiFinding";
  const node = color(ai ? "purple" : KIND_COLOR[e.kind]);
  const d = (t(e.date) - t(start)) / 1000;
  const offset = d >= 0 ? `+${fmtShort(d)}` : `−${fmtShort(-d)}`;
  return (
    <div className="row" style={{ alignItems: "stretch", gap: 10 }}>
      <div className="col" style={{ width: 54, alignItems: "flex-end", paddingTop: 2, flex: "none" }}>
        {showDay && <span className="faint" style={{ fontSize: 10.5 }}>{fmtDay(new Date(e.date))}</span>}
        <span className="caption num" style={{ fontWeight: 500 }}>{hm(e.date)}</span>
        <span className="faint num" style={{ fontSize: 10.5 }}>{offset}</span>
      </div>
      <div className="col" style={{ width: 24, alignItems: "center", flex: "none" }}>
        <div style={{ width: 2, height: 4, background: first ? "transparent" : "color-mix(in srgb, var(--fg) 12%, transparent)" }} />
        <span style={{ width: 24, height: 24, borderRadius: 24, background: node, display: "inline-flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
          <Icon name={KIND_ICON[e.kind]} size={11} color="white" />
        </span>
        <div style={{ width: 2, flex: 1, background: last ? "transparent" : "color-mix(in srgb, var(--fg) 12%, transparent)" }} />
      </div>
      <div className="col grow" style={{ paddingBottom: last ? 0 : 12 }}>
        <div className="col gap4" style={ai ? {
          padding: 10, borderRadius: 8,
          background: `color-mix(in srgb, var(--purple) ${e.kind === "aiFinding" ? 12 : 6}%, transparent)`,
          border: `1px solid color-mix(in srgb, var(--purple) ${e.kind === "aiFinding" ? 35 : 15}%, transparent)`,
        } : { padding: 2 }}>
          <div className="row gap6">
            <ActorAvatar actor={e.actor} size={16} />
            <span className="caption ellipsis" style={{ fontWeight: 600, color: ai ? "var(--purple)" : undefined }}>{dir.name(e.actor)}</span>
            {e.kind === "aiFinding" ? <AIBadge text="AI 發現" /> : (
              <>
                <Pill text={KIND_LABEL[e.kind]} color={KIND_COLOR[e.kind]} />
                {isAI(e.actor) && <AIBadge />}
              </>
            )}
          </div>
          <div style={{ fontWeight: e.kind === "status" ? 500 : undefined, userSelect: "text", lineHeight: 1.5, whiteSpace: "pre-wrap" }}>{e.text}</div>
        </div>
      </div>
    </div>
  );
}

// MARK: - 檢討報告

function PostmortemView({ inc }: { inc: Incident }) {
  const { services, workItems } = useInc();
  const dir = useDirectory();
  const pm = postmortem(inc, services, workItems, dir);
  const [copied, copy] = useCopied();
  const [created, setCreated] = useState<Set<string>>(new Set());
  const { error, run } = useRun();
  const create = (title: string) => run(async () => {
    await tool("work_item_create", { title, type: "incident", status: "todo", incident_id: inc.id, points: 3, ...(inc.services[0] ? { service: inc.services[0] } : {}) });
    setCreated((s) => new Set([...s, title]));
  });
  const section = (title: string, icon: string, body: React.ReactNode) => (
    <Card><span className="row gap6 h3" style={{ fontSize: 13.5 }}><Icon name={icon} size={14} />{title}</span>{body}</Card>
  );
  const para = (s: string) => <div style={{ whiteSpace: "pre-wrap", lineHeight: 1.6, userSelect: "text" }}>{s}</div>;
  const bullets = (xs: string[], c: string) => (
    <div className="col gap4">{xs.map((x) => <span key={x} className="row gap6" style={{ alignItems: "baseline" }}><span className="dot" style={{ width: 5, height: 5, background: color(c), flex: "none" }} />{x}</span>)}</div>
  );
  return (
    <div className="col" style={{ gap: 16 }}>
      <div className="row gap8" style={{ padding: 14, borderRadius: 12, background: "color-mix(in srgb, var(--purple) 8%, transparent)", border: "1px solid color-mix(in srgb, var(--purple) 20%, transparent)" }}>
        <Icon name="sparkles" size={18} color="var(--purple)" />
        <div className="col grow"><span className="h3" style={{ fontSize: 13.5 }}>檢討報告草稿</span><span className="caption muted">由 AI 依時間軸、告警與後續工作起草,請指揮官確認後再發布。</span></div>
        <button className="btn" onClick={() => copy("md", postmortemMarkdown(pm, `${inc.id} ${inc.title}`))}>
          <Icon name={copied ? "checkmark" : "doc.on.clipboard"} size={12} />{copied ? "已複製" : "複製為 Markdown"}
        </button>
      </div>
      <div className="grid g3">
        <MetricTile title="總時長" value={fmtShort(durationOf(inc))} icon="timer" tint="blue" />
        <MetricTile title="首次回應" value={fmtShort(ackDelay(inc))} icon="hand.raised" tint="orange" />
        <MetricTile title="AI 參與" value={`${inc.timeline.filter((e) => isAI(e.actor)).length} 則`} icon="sparkles" tint="purple" />
      </div>
      {section("摘要", "doc.text", para(pm.summary))}
      {section("影響", "person.3", para(pm.impact))}
      {section("時間軸", "clock", pm.timeline.length === 0 ? para("沒有時間軸紀錄。") : (
        <div className="col gap6">
          {pm.timeline.map(([d, who, text], i) => (
            <div key={i} className="row gap8" style={{ alignItems: "baseline" }}>
              <span className="caption num muted" style={{ width: 92, flex: "none" }}>{fmtDateTime(d)}</span>
              <span className="caption ellipsis" style={{ width: 90, flex: "none", fontWeight: 600 }}>{who}</span>
              <span>{text}</span>
            </div>
          ))}
        </div>
      ))}
      {section("根本原因", "scope", para(pm.rootCause))}
      <div className="grid g2" style={{ alignItems: "start", gap: 16 }}>
        {section("做得好的地方", "hand.thumbsup", bullets(pm.wentWell, "green"))}
        {section("可以改進的地方", "arrow.up.forward.circle", bullets(pm.improve, "orange"))}
      </div>
      {section("行動項目", "checklist", (
        <div className="col gap6">
          {pm.actions.map(([title, st, exists]) => (
            <div key={title} className="row gap8">
              <Icon name={exists ? "checkmark.square" : "square"} size={13} className="muted" />
              <span className="grow">{title}</span>
              {exists ? <Pill text={st} color="blue" /> : (
                <>
                  <button className="btn small" disabled={created.has(title)} onClick={() => create(title)}>建立工作項目</button>
                  <AIBadge text="AI 建議" />
                </>
              )}
            </div>
          ))}
          <ErrorText>{error}</ErrorText>
        </div>
      ))}
    </div>
  );
}
