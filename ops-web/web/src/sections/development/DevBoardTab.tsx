// 開發 › Sprint 看板(Swift 的 DevBoardTab.swift)。拖拉卡片移欄 = ops_work_item_move。
import { useEffect, useState } from "react";
import { action, tool } from "../../api";
import { color, EmptyState, Modal, Pill } from "../../components/ui";
import {
  ActorAvatar, ActorLabel, AIBadge, copyText, HealthBadge, PR, SIcon, ToggleButton, useAct, useContextMenu, useList, useOps,
  WORK_STATUS, WORK_STATUS_ORDER, WORK_TYPE, WORK_TYPE_ORDER, sameActor,
  type Actor, type PullRequest, type WorkItem, type WorkStatusKey, type WorkTypeKey,
} from "../../components/opsKit";
import { ChecksIcon, TypeTag, useDev } from "./devShared";

export function DevBoardTab() {
  const o = useOps();
  const [work, err, ready] = useList<WorkItem>("work_items");
  const [prs] = useList<PullRequest>("pull_requests");
  const [assignee, setAssignee] = useState("all");
  const [type, setType] = useState<WorkTypeKey | "">("");
  const [onlyAI, setOnlyAI] = useState(false);
  const [showBacklog, setShowBacklog] = useState(true);
  const [query, setQuery] = useState("");
  const [targeted, setTargeted] = useState<WorkStatusKey>();
  const { run, toastEl } = useAct();
  const me: Actor = { kind: "person", id: o.me };

  const columns = showBacklog ? WORK_STATUS_ORDER : WORK_STATUS_ORDER.filter((s) => s !== "backlog");
  const filtered = work.filter((it) => {
    if (type && it.type !== type) return false;
    if (onlyAI && !it.createdByAI) return false;
    if (assignee === "me" && !sameActor(it.assignee, me)) return false;
    if (assignee === "ai" && it.assignee?.kind !== "ai") return false;
    if (assignee === "none" && it.assignee) return false;
    if (assignee.startsWith("p:") && !(it.assignee?.kind === "person" && it.assignee.id === assignee.slice(2))) return false;
    if (query) {
      const q = query.toLowerCase();
      if (!it.title.toLowerCase().includes(q) && !it.id.toLowerCase().includes(q)) return false;
    }
    return true;
  });
  const hasFilter = assignee !== "all" || !!type || onlyAI || !!query;
  const pts = filtered.reduce((a, i) => a + i.points, 0);
  const move = (id: string, status: WorkStatusKey) => run(() => tool("ops_work_item_move", { id, status }));

  return (
    <div className="ops-page">
      <div className="ops-filterbar">
        <input className="input" style={{ minWidth: 120, maxWidth: 200, flex: 1 }} placeholder="搜尋標題或編號" value={query} onChange={(e) => setQuery(e.target.value)} />
        <select className="select" value={assignee} onChange={(e) => setAssignee(e.target.value)} title="負責人">
          <option value="all">所有人</option><option value="me">我</option><option value="ai">AI</option><option value="none">未指派</option>
          {o.people.length > 0 && <option disabled>──────</option>}
          {o.people.map((p) => <option key={p.id} value={`p:${p.id}`}>{p.name}</option>)}
        </select>
        <select className="select" value={type} onChange={(e) => setType(e.target.value as WorkTypeKey | "")} title="類型">
          <option value="">全部類型</option><option disabled>──────</option>
          {WORK_TYPE_ORDER.map((t) => <option key={t} value={t}>{WORK_TYPE[t].label}</option>)}
        </select>
        <ToggleButton on={onlyAI} onChange={setOnlyAI} title="只顯示 AI 建立的項目"><SIcon name="sparkles" size={12} />AI 建立</ToggleButton>
        <ToggleButton on={showBacklog} onChange={setShowBacklog} title="顯示 Backlog 欄"><SIcon name="tray" size={12} />Backlog</ToggleButton>
        {hasFilter && <button className="btn link" onClick={() => { setAssignee("all"); setType(""); setOnlyAI(false); setQuery(""); }}>清除</button>}
        <span className="grow" />
        <span className="muted num">{filtered.length} 項・{pts} 點</span>
      </div>
      {err && work.length === 0 ? <EmptyState title="讀不到工作項目" icon="exclamationmark.triangle" message={err} />
        : !ready ? null : work.length === 0 ? <EmptyState title="看板是空的" icon="rectangle.split.3x1" message="這個 Sprint 還沒有工作項目" /> : (
          <div className="ops-board">
            {columns.map((status) => {
              const items = filtered.filter((i) => i.status === status);
              const isTarget = targeted === status;
              return (
                <div key={status} className={"ops-col" + (isTarget ? " target" : "")}
                  onDragOver={(e) => { e.preventDefault(); if (targeted !== status) setTargeted(status); }}
                  onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setTargeted((t) => (t === status ? undefined : t)); }}
                  onDrop={(e) => {
                    e.preventDefault();
                    setTargeted(undefined);
                    const id = e.dataTransfer.getData("text/plain");
                    const it = work.find((w) => w.id === id);
                    if (it && it.status !== status) move(id, status);
                  }}>
                  <div className="row gap6" style={{ padding: "9px 10px" }}>
                    <SIcon name={WORK_STATUS[status].icon} color={WORK_STATUS[status].color} />
                    <span className="card-title">{WORK_STATUS[status].label}</span>
                    <span className="ops-count">{items.length}</span>
                    <span className="grow" />
                    <span className="caption muted num">{items.reduce((a, i) => a + i.points, 0)} 點</span>
                  </div>
                  <div className="ops-col-body">
                    {items.map((it) => <WorkCard key={it.id} item={it} prs={prs} onMove={move} />)}
                    {items.length === 0 && <div className="ops-dashed">{isTarget ? `放開以移到「${WORK_STATUS[status].label}」` : "沒有項目"}</div>}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      {toastEl}
    </div>
  );
}

function WorkCard({ item, prs, onMove }: { item: WorkItem; prs: PullRequest[]; onMove: (id: string, s: WorkStatusKey) => void }) {
  const o = useOps();
  const dev = useDev();
  const [detail, setDetail] = useState(false);
  const menu = useContextMenu();
  const { run, toastEl } = useAct();
  const highlighted = dev.highlightedItem === item.id;
  const linked = prs.filter((p) => p.linkedItem === item.id);
  const me: Actor = { kind: "person", id: o.me };
  const assign = (a: Actor | null) => run(() => action("work_item_assign", { id: item.id, assignee: a?.id ?? "" }));

  useEffect(() => {
    if (highlighted) {
      const id = window.setTimeout(() => setDetail(true), 300);
      return () => window.clearTimeout(id);
    }
  }, [highlighted]);
  const close = () => { setDetail(false); if (highlighted) dev.setHighlightedItem(undefined); };

  const svc = item.service ? o.service(item.service) : undefined;
  return (
    <>
      <div className={"ops-work" + (highlighted ? " hl" : "")} draggable onDragStart={(e) => { e.dataTransfer.setData("text/plain", item.id); e.dataTransfer.effectAllowed = "move"; }}
        onClick={() => setDetail(true)}
        onContextMenu={menu.open([
          { header: "移到…" },
          ...WORK_STATUS_ORDER.map((s) => ({ label: <><SIcon name={WORK_STATUS[s].icon} size={12} />&nbsp;{WORK_STATUS[s].label}</>, disabled: s === item.status, onClick: () => onMove(item.id, s) })),
          "sep",
          ...(!sameActor(item.assignee, me) ? [{ label: "指派給我", onClick: () => assign(me) }] : []),
          ...(item.assignee ? [{ label: "取消指派", onClick: () => assign(null) }] : []),
          "sep",
          { label: "查看詳細資料", onClick: () => setDetail(true) },
          ...(item.service ? [{ label: `前往服務「${svc?.name ?? item.service}」`, onClick: () => o.go("infrastructure", item.service) }] : []),
          { label: "複製編號", onClick: () => copyText(item.id) },
        ])}>
        <div style={{ width: 3, flex: "none", background: color(WORK_TYPE[item.type]?.color) }} />
        <div className="col grow" style={{ padding: 10, gap: 7, minWidth: 0 }}>
          <div className="row gap6">
            <span className="caption mono muted">{item.id}</span>
            {item.createdByAI && <span title="由 AI 建立"><SIcon name="sparkles" size={11} color="purple" /></span>}
            <span className="grow" />
            <span className="ops-count" title={`${item.points} 點`} style={{ minWidth: 18, textAlign: "center" }}>{item.points}</span>
          </div>
          <div style={{ display: "-webkit-box", WebkitLineClamp: 3, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{item.title}</div>
          <div className="row gap6">
            <TypeTag type={item.type} />
            {item.service && <span className="ellipsis" style={{ fontSize: 10.5, padding: "1px 5px", borderRadius: 4, background: "color-mix(in srgb, var(--fg) 6%, transparent)" }}>{svc?.name ?? item.service}</span>}
            <span className="grow" />
            {linked.length > 0 && <span className="row muted" style={{ gap: 2, fontSize: 10.5 }} title={`${linked.length} 個相關 PR`}><SIcon name="arrow.triangle.pull" size={10} />{linked.length}</span>}
            <ActorAvatar actor={item.assignee} size={20} />
          </div>
        </div>
      </div>
      {menu.el}{toastEl}
      <Modal open={detail} onClose={close} width={380}>
        <WorkDetail item={item} prs={linked} onMove={onMove} onAssign={assign} close={close} />
      </Modal>
    </>
  );
}

function WorkDetail({ item: w, prs, onMove, onAssign, close }: { item: WorkItem; prs: PullRequest[]; onMove: (id: string, s: WorkStatusKey) => void; onAssign: (a: Actor | null) => void; close: () => void }) {
  const o = useOps();
  const dev = useDev();
  const me: Actor = { kind: "person", id: o.me };
  const svc = w.service ? o.service(w.service) : undefined;
  return (
    <div className="col gap12">
      <div className="row gap8">
        <span className="mono muted">{w.id}</span>
        <Pill text={WORK_TYPE[w.type].label} color={WORK_TYPE[w.type].color} />
        {w.createdByAI && <AIBadge text="AI 建立" />}
        <span className="grow" />
        <span className="muted num">{w.points} 點</span>
      </div>
      <div className="card-title" style={{ fontSize: 15 }}>{w.title}</div>
      <div className="ops-kbd-row">
        <span className="muted">狀態</span>
        <select className="select" style={{ justifySelf: "start" }} value={w.status} onChange={(e) => onMove(w.id, e.target.value as WorkStatusKey)}>
          {WORK_STATUS_ORDER.map((s) => <option key={s} value={s}>{WORK_STATUS[s].label}</option>)}
        </select>
        <span className="muted">負責人</span>
        <span className="row gap8">
          {w.assignee ? <ActorLabel actor={w.assignee} /> : <span className="faint">未指派</span>}
          {!sameActor(w.assignee, me) && <button className="btn link caption" onClick={() => onAssign(me)}>指派給我</button>}
        </span>
        {w.service && (
          <>
            <span className="muted">服務</span>
            <button className="btn link row gap4" style={{ justifySelf: "start" }} onClick={() => { close(); o.go("infrastructure", w.service); }}>
              {svc && <HealthBadge h={svc.health} compact />}{svc?.name ?? w.service}<SIcon name="arrow.up.forward" size={10} />
            </button>
          </>
        )}
      </div>
      {prs.length > 0 && (
        <>
          <div className="divider" />
          <div style={{ fontWeight: 500 }}>相關 Pull Request</div>
          {prs.map((pr) => (
            <button key={pr.id} className="ops-plain row gap6" onClick={() => { close(); dev.showPR(pr.id); }}>
              <span className="dot" style={{ background: color(PR[pr.status].color) }} />
              <span className="muted num">#{pr.id}</span>
              <span className="ellipsis grow">{pr.title}</span>
              <ChecksIcon s={pr.checks} />
            </button>
          ))}
        </>
      )}
    </div>
  );
}
