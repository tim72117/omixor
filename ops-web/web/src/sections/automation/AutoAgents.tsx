// AI 自動化 › 虛擬角色:角色設定、例行任務、指派任務、看任務進度(Swift 的 AutoAgentsView)
import { Fragment, useEffect, useState } from "react";
import { action, tool, useRun, useView } from "../../api";
import { AgentAvatar, Markdown, go, useCopied, useDirectory } from "../../components/bits";
import { Icon } from "../../components/Icon";
import {
  DOC_ACCESS_LABEL, PRIORITIES, PRIORITY_COLOR, PRIORITY_LABEL, ROUTINE_KIND_LABEL, TASK_COLOR, TASK_ICON, TASK_LABEL, tabTitle, taskOpen,
  type Agent, type AgentRoutine, type AgentTask, type CabinetSection, type CabinetView, type DocAccess, type Priority, type RoutineKind,
} from "../../components/models";
import { Card, EmptyState, ErrorText, Modal, Pill, color } from "../../components/ui";
import { relative } from "../../fmt";
import { useAuto } from "./autoShared";

const PALETTE = ["blue", "teal", "green", "orange", "pink", "purple", "indigo", "gray"];
const SYMBOLS = ["person.crop.circle", "chart.line.uptrend.xyaxis", "waveform", "tray.full", "magnifyingglass", "wrench.and.screwdriver", "shield.lefthalf.filled", "doc.text", "megaphone", "brain.head.profile"];
const WEEKDAYS = ["日", "一", "二", "三", "四", "五", "六"];
const p2 = (n: number) => String(n).padStart(2, "0");

export function scheduleLabel(r: AgentRoutine): string {
  const tt = `${p2(r.hour)}:${p2(r.minute)}`;
  if (r.kind === "daily") return `每天 ${tt}`;
  if (r.kind === "weekly") return `每週${WEEKDAYS[Math.max(1, Math.min(7, r.weekday)) - 1]} ${tt}`;
  return `每 ${r.everyHours} 小時`;
}
/** 上次排程(沒有就從建立時間)之後的下一個時間點 */
export function nextRun(r: AgentRoutine): Date {
  const base = new Date(r.lastRunAt ?? r.createdAt ?? Date.now());
  if (r.kind === "hourly") return new Date(base.getTime() + Math.max(1, r.everyHours) * 3600000);
  const d = new Date(base);
  d.setHours(r.hour, r.minute, 0, 0);
  if (r.kind === "daily") { if (d <= base) d.setDate(d.getDate() + 1); return d; }
  while (d.getDay() !== r.weekday - 1 || d <= base) d.setDate(d.getDate() + 1);
  return d;
}
const fmtMDHM = (d: Date | string) => { const x = new Date(d); return `${x.getMonth() + 1}月${x.getDate()}日 ${p2(x.getHours())}:${p2(x.getMinutes())}`; };

const newRoutine = (): AgentRoutine => ({ id: crypto.randomUUID().toUpperCase(), title: "", instructions: "", kind: "daily", hour: 9, minute: 0, weekday: 2, everyHours: 6, priority: "normal", enabled: true, createdAt: new Date().toISOString() });
const blankAgent = (): Agent => ({ id: "", name: "", role: "", symbol: "person.crop.circle", color: "blue", instructions: "", active: true, routines: [], docAccess: {} });

export function AutoAgents({ selection, setSelection }: { selection?: string; setSelection: (id?: string) => void }) {
  const d = useAuto();
  const [editing, setEditing] = useState<Agent>();
  const agents = d.agents?.agents ?? [];
  useEffect(() => {
    if ((!selection || !agents.some((a) => a.id === selection)) && agents.length) setSelection(agents[0].id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agents.map((a) => a.id).join(",")]);
  if (!d.agents) {
    return d.agentsError ? <EmptyState title="讀不到虛擬角色" icon="exclamationmark.triangle" message={d.agentsError} /> : <div className="muted" style={{ padding: 20 }}>載入中…</div>;
  }
  const sel = agents.find((a) => a.id === selection);
  return (
    <div className="split">
      <div className="split-list" style={{ width: 250 }}>
        <div className="grow" style={{ overflowY: "auto" }}>
          {agents.map((a) => {
            const open = d.agents!.tasks.filter((x) => x.agentId === a.id && taskOpen(x.status));
            return (
              <div key={a.id} className={"bx-list-row" + (selection === a.id ? " on" : "")} style={{ alignItems: "center", gap: 10, opacity: a.active ? 1 : 0.5 }} onClick={() => setSelection(a.id)}>
                <AgentAvatar agent={a} size={28} />
                <div className="col grow" style={{ minWidth: 0 }}><span className="ellipsis">{a.name}</span><span className="caption muted ellipsis">{a.role}</span></div>
                {open.some((x) => x.status === "blocked") && <Icon name="hand.raised.fill" size={12} color="var(--orange)" />}
                {open.length > 0 && <span className="caption muted num">{open.length}</span>}
              </div>
            );
          })}
        </div>
        <div style={{ padding: 8, borderTop: "1px solid var(--line)" }}>
          <button className="btn link" onClick={() => setEditing(blankAgent())}><Icon name="plus" size={12} />新增角色</button>
        </div>
      </div>
      <div className="split-detail">
        {sel ? <AgentDetail key={sel.id} agent={sel} tasks={d.agents.tasks.filter((x) => x.agentId === sel.id)} edit={() => setEditing(sel)} />
          : <EmptyState title="選一個角色" icon="person.2" message="每個角色有自己的角色設定與例行任務。任務指派後,在 Claude Code 請 AI 以角色身分透過 MCP 領取並回報。" />}
      </div>
      {editing && <EditSheet original={editing} onClose={() => setEditing(undefined)} onSaved={(id) => id && setSelection(id)} />}
    </div>
  );
}

function AgentDetail({ agent, tasks, edit }: { agent: Agent; tasks: AgentTask[]; edit: () => void }) {
  const [cab] = useView<CabinetView>("cabinet");
  const dir = useDirectory();
  const [title, setTitle] = useState("");
  const [instructions, setInstructions] = useState("");
  const [priority, setPriority] = useState<Priority>("normal");
  const [selectedTask, setSelectedTask] = useState<string>();
  const [copied, copy] = useCopied();
  const { busy, error, run } = useRun();
  const layout = Array.isArray(cab?.layout) ? cab!.layout : [];
  const sorted = [...tasks].sort((a, b) => (taskOpen(a.status) ? 0 : 1) - (taskOpen(b.status) ? 0 : 1) || b.updatedAt.localeCompare(a.updatedAt));
  const copyPrompt = (key: string, task?: string) => run(async () => {
    const r = await action<{ text?: string } | string>("agent_prompt", { agent: agent.id, ...(task ? { task } : {}) });
    copy(key, typeof r === "string" ? r : r?.text ?? "");
  });
  const assign = () => run(async () => {
    const r = await tool<{ id?: string; task?: { id?: string } }>("ops_agent_task_create", { assign_to: agent.id, title: title.trim(), instructions, priority });
    const id = r?.id ?? r?.task?.id;
    if (id) setSelectedTask(id);
    setTitle(""); setInstructions(""); setPriority("normal");
  });
  const scope = Object.entries(agent.docAccess ?? {}).sort(([a], [b]) => a.localeCompare(b)).flatMap(([k, v]) => {
    const s = layout.find((x) => x.id === k || x.label === k);
    if (s) return [[s.label, v] as [string, DocAccess]];
    for (const sec of layout) {
      const t = sec.tabs.find((x) => x.path === k);
      if (t) return [[`${sec.label} › ${tabTitle(t)}`, v] as [string, DocAccess]];
    }
    return [];
  });
  return (
    <div className="page-inner" style={{ maxWidth: 900 }}>
      <div className="row gap12">
        <AgentAvatar agent={agent} size={44} />
        <div className="col grow">
          <span className="row gap8"><span className="page-title">{agent.name}</span><span className="mono caption muted">{agent.id}</span>{!agent.active && <Pill text="停用" color="gray" />}</span>
          <span className="muted">{agent.role}</span>
        </div>
        <button className="btn" title="複製一段話,貼到 Claude Code 就會以這個角色領取所有待領取任務" onClick={() => copyPrompt("all")}>
          <Icon name="doc.on.clipboard" size={12} />{copied === "all" ? "已複製" : "複製給 Claude Code"}
        </button>
        <button className="btn" onClick={edit}>編輯…</button>
      </div>
      <ErrorText>{error}</ErrorText>
      <Card title="角色設定" subtitle="AI 領取任務時會一起拿到這段">
        {agent.instructions ? <Markdown text={agent.instructions} /> : <span className="muted">還沒寫角色設定</span>}
      </Card>
      <Card title="例行任務" subtitle="到時間 omixor 會替角色排一筆任務;上一筆沒做完不會重複排">
        {agent.routines.length === 0 && <span className="muted">沒有例行任務,按「編輯…」新增</span>}
        {agent.routines.map((r) => (
          <div key={r.id} className="row gap8">
            <Icon name="clock.arrow.circlepath" size={14} color={r.enabled ? "var(--blue)" : "var(--fg2)"} />
            <div className="col grow"><span>{r.title}</span><span className="caption muted">{scheduleLabel(r)}{r.enabled ? ` · 下次 ${fmtMDHM(nextRun(r))}` : " · 已停用"}</span></div>
            <button className="btn link" disabled={busy} onClick={() => run(() => action("agent_routine_run", { agent: agent.id, routine: r.id }))}>現在排一筆</button>
          </div>
        ))}
      </Card>
      <Card title="文件櫃存取範圍" subtitle="按「編輯…」調整">
        {scope.length === 0 ? <span className="muted">不能存取文件櫃</span> : (
          <div className="col gap6">
            {scope.map(([label, acc]) => (
              <span key={label} className="row gap6">
                <Icon name={acc === "write" ? "pencil.circle" : acc === "read" ? "eye.circle" : "nosign"} size={13} color={acc === "write" ? "var(--blue)" : "var(--fg2)"} />
                {label}<span className="caption muted">{DOC_ACCESS_LABEL[acc]}</span>
              </span>
            ))}
          </div>
        )}
      </Card>
      <Card title="指派任務">
        <div className="row gap8">
          <input className="input grow" value={title} placeholder={`要${agent.name}做什麼?`} onChange={(e) => setTitle(e.target.value)} />
          <select className="select" style={{ width: 90 }} value={priority} onChange={(e) => setPriority(e.target.value as Priority)}>{PRIORITIES.map((p) => <option key={p} value={p}>{PRIORITY_LABEL[p]}</option>)}</select>
        </div>
        <textarea className="input" rows={2} value={instructions} placeholder="補充說明(選填,Markdown)" onChange={(e) => setInstructions(e.target.value)} />
        <div className="row" style={{ justifyContent: "flex-end" }}><button className="btn primary" disabled={!title.trim() || busy} onClick={assign}>指派</button></div>
      </Card>
      <Card title="任務" subtitle={tasks.length ? `${tasks.filter((x) => taskOpen(x.status)).length} 件未完成` : undefined}>
        {tasks.length === 0 && <span className="muted">還沒有任務</span>}
        {sorted.map((tk) => (
          <div key={tk.id} className="col gap8" style={{ padding: "4px 0", borderBottom: "1px solid var(--line)" }}>
            <button className="bx-row-btn" onClick={() => setSelectedTask(selectedTask === tk.id ? undefined : tk.id)}>
              <Icon name={TASK_ICON[tk.status]} size={13} color={color(TASK_COLOR[tk.status])} />
              <span className="mono caption muted">{tk.id}</span>
              <span className="ellipsis grow" style={{ color: taskOpen(tk.status) ? undefined : "var(--fg2)" }}>{tk.title}</span>
              {tk.routineId && <Icon name="clock.arrow.circlepath" size={11} className="muted" />}
              {(tk.priority === "urgent" || tk.priority === "high") && <Pill text={PRIORITY_LABEL[tk.priority]} color={PRIORITY_COLOR[tk.priority]} />}
              <Pill text={TASK_LABEL[tk.status]} color={TASK_COLOR[tk.status]} />
              <span className="caption muted">{relative(tk.updatedAt)}</span>
            </button>
            {selectedTask === tk.id && (
              <div className="col gap8" style={{ paddingLeft: 24 }}>
                <span className="caption muted">{dir.authorName(tk.createdBy)} 建立 · {fmtMDHM(tk.createdAt)}</span>
                {tk.instructions && <Markdown text={tk.instructions} />}
                {(tk.notes ?? []).map((n) => (
                  <span key={n.id} className="row gap6" style={{ alignItems: "baseline" }}>
                    <span className="caption" style={{ fontWeight: 600 }}>{dir.authorName(n.author)}</span>
                    <span className="caption muted">{p2(new Date(n.at).getHours())}:{p2(new Date(n.at).getMinutes())}</span>
                    <span style={{ userSelect: "text" }}>{n.text}</span>
                  </span>
                ))}
                {tk.result && (
                  <div className="col gap4" style={{ padding: 10, borderRadius: 8, background: "color-mix(in srgb, var(--green) 8%, transparent)" }}>
                    <span className="row gap4 caption" style={{ fontWeight: 600, color: "var(--green)" }}><Icon name="checkmark.seal" size={12} />結果</span>
                    <Markdown text={tk.result} />
                  </div>
                )}
                {(tk.links ?? []).length > 0 && (
                  <div className="row gap8 wrap">
                    {tk.links.map((l) => /^ISS-\d+$/i.test(l) ? <button key={l} className="btn link" onClick={() => go("development", l)}>{l}</button> : <span key={l} className="caption" style={{ userSelect: "text" }}>{l}</span>)}
                  </div>
                )}
                <div className="row gap8">
                  {taskOpen(tk.status) ? (
                    <>
                      <button className="btn small" onClick={() => copyPrompt(tk.id, tk.id)}><Icon name="doc.on.clipboard" size={11} />{copied === tk.id ? "已複製" : "複製給 Claude Code"}</button>
                      <button className="btn small danger" disabled={busy} onClick={() => run(() => action("agent_task_cancel", { id: tk.id }))}>取消任務</button>
                      {tk.status !== "queued" && <button className="btn small" disabled={busy} onClick={() => run(() => action("agent_task_requeue", { id: tk.id }))}>退回待領取</button>}
                    </>
                  ) : <button className="btn small" disabled={busy} onClick={() => run(() => action("agent_task_requeue", { id: tk.id }))}>重新排入</button>}
                </div>
              </div>
            )}
          </div>
        ))}
      </Card>
    </div>
  );
}

// MARK: - 編輯角色

function EditSheet({ original, onClose, onSaved }: { original: Agent; onClose: () => void; onSaved: (id?: string) => void }) {
  const [draft, setDraft] = useState<Agent>({ ...original, routines: original.routines.map((r) => ({ ...r })), docAccess: { ...(original.docAccess ?? {}) } });
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [cab] = useView<CabinetView>("cabinet");
  const { busy, error, run } = useRun();
  const isNew = !original.id;
  const set = (p: Partial<Agent>) => setDraft((d) => ({ ...d, ...p }));
  const setRoutine = (id: string, p: Partial<AgentRoutine>) => setDraft((d) => ({ ...d, routines: d.routines.map((r) => (r.id === id ? { ...r, ...p } : r)) }));
  const save = () => run(async () => {
    const a = { ...draft, name: draft.name.trim(), routines: draft.routines.filter((r) => r.title.trim()) };
    const r = await action<Agent | { id?: string }>("agent_upsert", a as unknown as Record<string, unknown>);
    onSaved((r as { id?: string })?.id ?? a.id);
    onClose();
  });
  const del = () => run(async () => { await action("agent_delete", { id: original.id }); onClose(); });
  return (
    <Modal open onClose={onClose} width={640}>
      <span className="modal-title">{isNew ? "新增角色" : `編輯「${original.name}」`}</span>
      <div className="grid g2">
        <label className="bx-field"><span>名稱</span><input className="input" value={draft.name} placeholder="例如 訪客分析師" onChange={(e) => set({ name: e.target.value })} /></label>
        <label className="bx-field"><span>職責</span><input className="input" value={draft.role} placeholder="一句話說明" onChange={(e) => set({ role: e.target.value })} /></label>
      </div>
      <div className="bx-field"><span>圖示</span>
        <div className="row gap4 wrap">{SYMBOLS.map((s) => (
          <button key={s} className="btn" style={{ padding: 5, borderColor: draft.symbol === s ? "var(--accent)" : undefined, background: draft.symbol === s ? "color-mix(in srgb, var(--accent) 14%, transparent)" : undefined }} onClick={() => set({ symbol: s })}><Icon name={s} size={15} /></button>
        ))}</div>
      </div>
      <div className="bx-field"><span>顏色</span>
        <div className="row gap6">{PALETTE.map((c) => (
          <button key={c} title={c} onClick={() => set({ color: c })} style={{ width: 20, height: 20, borderRadius: 20, background: color(c), border: draft.color === c ? "2px solid var(--fg)" : "2px solid transparent" }} />
        ))}</div>
      </div>
      <label className="row gap6"><input type="checkbox" checked={draft.active} onChange={(e) => set({ active: e.target.checked })} />啟用(停用時例行任務不會排)</label>
      <label className="bx-field"><span>角色設定(給 AI 的說明,Markdown)</span><textarea className="textarea" style={{ minHeight: 140 }} value={draft.instructions} onChange={(e) => set({ instructions: e.target.value })} /></label>
      <div className="bx-field">
        <span>文件櫃存取範圍</span>
        <DocAccessEditor layout={Array.isArray(cab?.layout) ? cab!.layout : []} access={draft.docAccess} onChange={(docAccess) => set({ docAccess })} />
        <span className="caption muted">對應研發基地的區塊與頁籤。頁籤沒設定時跟著區塊。角色用 MCP 讀寫文件時只能碰這些範圍。</span>
      </div>
      <div className="bx-field">
        <span>例行任務</span>
        {draft.routines.map((r) => (
          <div key={r.id} className="col gap6" style={{ padding: 8, borderRadius: 8, border: "1px solid var(--line)" }}>
            <div className="row gap6">
              <input type="checkbox" checked={r.enabled} onChange={(e) => setRoutine(r.id, { enabled: e.target.checked })} />
              <input className="input grow" placeholder="例行任務標題" value={r.title} onChange={(e) => setRoutine(r.id, { title: e.target.value })} />
              <button className="bx-icon-btn" title="刪除" onClick={() => setDraft((d) => ({ ...d, routines: d.routines.filter((x) => x.id !== r.id) }))}><Icon name="trash" size={13} color="var(--red)" /></button>
            </div>
            <div className="row gap6">
              <select className="select" style={{ width: 120 }} value={r.kind} onChange={(e) => setRoutine(r.id, { kind: e.target.value as RoutineKind })}>
                {(Object.keys(ROUTINE_KIND_LABEL) as RoutineKind[]).map((k) => <option key={k} value={k}>{ROUTINE_KIND_LABEL[k]}</option>)}
              </select>
              {r.kind === "weekly" && (
                <select className="select" value={r.weekday} onChange={(e) => setRoutine(r.id, { weekday: +e.target.value })}>
                  {WEEKDAYS.map((w, i) => <option key={i} value={i + 1}>週{w}</option>)}
                </select>
              )}
              {r.kind === "hourly" ? (
                <label className="row gap4">每 <input className="input" type="number" min={1} max={48} style={{ width: 60 }} value={r.everyHours} onChange={(e) => setRoutine(r.id, { everyHours: Math.max(1, Math.min(48, +e.target.value || 1)) })} /> 小時</label>
              ) : (
                <span className="row gap4">
                  <input className="input" type="number" min={0} max={23} style={{ width: 56 }} value={r.hour} onChange={(e) => setRoutine(r.id, { hour: Math.max(0, Math.min(23, +e.target.value || 0)) })} />:
                  <input className="input" type="number" min={0} max={55} step={5} style={{ width: 56 }} value={r.minute} onChange={(e) => setRoutine(r.id, { minute: Math.max(0, Math.min(55, +e.target.value || 0)) })} />
                </span>
              )}
            </div>
            <textarea className="input" rows={1} placeholder="要做什麼(給 AI 的任務說明)" value={r.instructions} onChange={(e) => setRoutine(r.id, { instructions: e.target.value })} />
          </div>
        ))}
        <button className="btn link" style={{ alignSelf: "flex-start" }} onClick={() => setDraft((d) => ({ ...d, routines: [...d.routines, newRoutine()] }))}><Icon name="plus" size={12} />新增例行任務</button>
      </div>
      <ErrorText>{error}</ErrorText>
      <div className="row gap8">
        {!isNew && <button className="btn danger" onClick={() => setConfirmDelete(true)}>刪除角色</button>}
        <div className="grow" />
        <button className="btn" onClick={onClose}>取消</button>
        <button className="btn primary" disabled={!draft.name.trim() || busy} onClick={save}>儲存</button>
      </div>
      <Modal open={confirmDelete} onClose={() => setConfirmDelete(false)} width={400} title={`刪除「${original.name}」?未完成的任務會被取消。`}>
        <div className="row gap8" style={{ justifyContent: "flex-end" }}>
          <button className="btn" onClick={() => setConfirmDelete(false)}>取消</button>
          <button className="btn red" disabled={busy} onClick={del}>刪除</button>
        </div>
      </Modal>
    </Modal>
  );
}

/** 角色的文件櫃存取範圍編輯器(Swift 的 DocAccessEditor) */
export function DocAccessEditor({ layout, access, onChange }: { layout: CabinetSection[]; access: Record<string, DocAccess>; onChange: (a: Record<string, DocAccess>) => void }) {
  const picker = (title: string, key: string, inherited?: DocAccess, indent?: boolean) => (
    <div key={key} className="row gap8" style={{ padding: "3px 0", paddingLeft: indent ? 20 : 0 }}>
      <span className="grow">{title}</span>
      <select className="select" value={access[key] ?? ""} onChange={(e) => {
        const n = { ...access };
        if (e.target.value) n[key] = e.target.value as DocAccess; else delete n[key];
        onChange(n);
      }}>
        <option value="">{inherited ? `跟區塊(${DOC_ACCESS_LABEL[inherited]})` : key.startsWith("/") ? "跟區塊(不能存取)" : "不能存取"}</option>
        {key.startsWith("/") && inherited && inherited !== "none" && <option value="none">不能存取</option>}
        <option value="read">可讀</option>
        <option value="write">可讀寫</option>
      </select>
    </div>
  );
  if (!layout.length) return <span className="caption muted">讀不到文件櫃結構</span>;
  return (
    <div className="col" style={{ padding: 8, borderRadius: 8, border: "1px solid var(--line)" }}>
      {layout.map((s) => s.tabs.length === 0 ? picker(s.label, s.id) : (
        <Fragment key={s.id}>
          <details className="bx-disclosure">
            <summary style={{ flex: 1 }}>{picker(s.label + "(整個區塊)", s.id)}</summary>
            {s.tabs.map((t) => picker(tabTitle(t), t.path, access[s.id], true))}
          </details>
        </Fragment>
      ))}
    </div>
  );
}
