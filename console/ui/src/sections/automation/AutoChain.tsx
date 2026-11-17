// AI 自動化 › 組織鏈:誰可以派工作給誰(資訊流動方向)。
// 一條邊 from → to:from 可以派工作給 to(label 例如 目標、任務;空白 = 任務),to 做完回報給 from。
// 節點是虛擬角色(agent:<id>)或組織成員(user:<email>)。核心依這張圖限制派工、自動回報。
// 編輯先改在草稿,按「存檔」才整份送到核心(驗證失敗時草稿保留,錯誤原文顯示)。
import { useMemo, useState } from "react";
import { action, useRun, useView } from "../../api";
import { AgentAvatar } from "../../components/bits";
import { Icon } from "../../components/Icon";
import type { Agent, ChainEdge, OrgChain, OrgChainView } from "../../components/models";
import { Card, EmptyState, ErrorText, Pill } from "../../components/ui";
import { relative } from "../../fmt";
import { useAuto } from "./autoShared";

const NODE_W = 160;
const NODE_H = 44;
const GAP_X = 36;
const GAP_Y = 96;
const PAD = 24;
const DISPATCH = "var(--accent)";
const REPORT = "var(--green)";

const sameEdge = (a: ChainEdge, b: ChainEdge) => a.from === b.from && a.to === b.to;
const edgeKey = (e: ChainEdge) => `${e.from}→${e.to}`;
const labelOf = (e: ChainEdge) => e.label.trim() || "任務";

/** 依邊分層:每個節點的層數 = 從最上游到它的最長路徑(有循環時,剩下的節點放在最後一層之後) */
function layers(nodes: string[], edges: ChainEdge[]): string[][] {
  const level = new Map<string, number>();
  const indeg = new Map(nodes.map((n) => [n, 0]));
  for (const e of edges) indeg.set(e.to, (indeg.get(e.to) ?? 0) + 1);
  const queue = nodes.filter((n) => !indeg.get(n));
  queue.forEach((n) => level.set(n, 0));
  while (queue.length) {
    const n = queue.shift()!;
    for (const e of edges) {
      if (e.from !== n) continue;
      level.set(e.to, Math.max(level.get(e.to) ?? 0, (level.get(n) ?? 0) + 1));
      indeg.set(e.to, (indeg.get(e.to) ?? 0) - 1);
      if (!indeg.get(e.to)) queue.push(e.to);
    }
  }
  const max = Math.max(-1, ...level.values());
  for (const n of nodes) if (!level.has(n)) level.set(n, max + 1); // 循環(存檔時核心會擋):照樣畫出來
  const out: string[][] = [];
  for (const n of nodes) (out[level.get(n)!] ??= []).push(n);
  return out.filter(Boolean);
}

export function AutoChain() {
  const d = useAuto();
  const [view, viewErr] = useView<OrgChainView>("org_chain", ["chain", "agents", "orgs"]);
  const agents = d.agents?.agents ?? [];
  const agentById = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);
  const saved: ChainEdge[] = view?.chain?.edges ?? [];
  const [draft, setDraft] = useState<ChainEdge[]>();
  const [savedAt, setSavedAt] = useState<string>();
  const { busy, error, run, setError } = useRun();
  // 沒有草稿時顯示核心最新的(別處改了也會跟著更新);有草稿時以草稿為準
  const edges = draft ?? saved;
  const dirty = !!draft;

  if (viewErr || view?.error) return <EmptyState title="讀不到組織鏈" icon="exclamationmark.triangle" message={viewErr ?? view?.error} />;
  if (!view) return <div className="muted" style={{ padding: 20 }}>載入中…</div>;

  const canEdit = view.canEdit;
  const members = view.members ?? [];
  const nodeName = (n: string) => {
    if (n.startsWith("agent:")) return agentById.get(n.slice(6))?.name ?? n.slice(6);
    if (n.startsWith("user:")) {
      const m = members.find((x) => x.email.toLowerCase() === n.slice(5));
      return m?.name ? `${m.name}` : n.slice(5);
    }
    return n;
  };
  const options = [
    ...agents.map((a) => ({ value: `agent:${a.id}`, label: `角色:${a.name}` })),
    ...members.map((m) => ({ value: `user:${m.email.toLowerCase()}`, label: `成員:${m.name ? `${m.name}(${m.email})` : m.email}` })),
  ];

  const change = (next: ChainEdge[]) => { setError(undefined); setDraft(next); };
  const save = () => run(async () => {
    const r = await action<OrgChain>("chain_save", { edges: draft ?? [] });
    setDraft(undefined);
    setSavedAt(r?.updatedAt);
  });

  return (
    <div className="col gap12" style={{ padding: 16, overflowY: "auto", flex: 1, minHeight: 0 }}>
      <Card title="組織鏈" subtitle="箭頭是派工方向:上游可以派工作給下游;下游做完會自動回報給上游(角色會收到「檢視回報」任務,成員可在任務查到回報給自己的結果)"
        right={
          <div className="row gap8" style={{ alignItems: "center" }}>
            {dirty && <Pill text="尚未存檔" color="orange" />}
            {!dirty && (savedAt ?? view.chain.updatedAt) && <span className="caption muted">更新於 {relative((savedAt ?? view.chain.updatedAt)!)}</span>}
            {canEdit && dirty && <button className="btn" disabled={busy} onClick={() => { setDraft(undefined); setError(undefined); }}>還原</button>}
            {canEdit && <button className="btn primary" disabled={!dirty || busy} onClick={save}>{busy ? "存檔中…" : "存檔"}</button>}
          </div>
        }>
        <Legend />
        {error && <ErrorText>{error}</ErrorText>}
        {edges.length === 0
          ? <EmptyState title="還沒有組織鏈" icon="point.3.connected.trianglepath.dotted"
            message={canEdit ? "在下面新增一條邊,例如 CEO →(目標)→ 研發長、研發長 →(任務)→ 設計師。沒有組織鏈時只有組織管理員能派工。" : "還沒有設定;請組織管理員設定誰可以派工作給誰。"} />
          : <ChainGraph edges={edges} name={nodeName} agentById={agentById} />}
      </Card>
      <Card title="邊" subtitle={`${edges.length} 條${canEdit ? "" : "(只有組織管理員可以修改)"}`}>
        {edges.map((e) => (
          <div key={edgeKey(e)} className="row gap8" style={{ padding: "6px 0", borderBottom: "1px solid var(--line)", alignItems: "center" }}>
            <NodeChip node={e.from} name={nodeName(e.from)} agentById={agentById} />
            <Icon name="chevron.right" size={12} className="muted" />
            <Pill text={labelOf(e)} color="blue" />
            <Icon name="chevron.right" size={12} className="muted" />
            <NodeChip node={e.to} name={nodeName(e.to)} agentById={agentById} />
            <span className="grow" />
            {canEdit && <button className="btn small danger" disabled={busy} onClick={() => change(edges.filter((x) => !sameEdge(x, e)))}>刪除</button>}
          </div>
        ))}
        {canEdit && <AddEdge options={options} edges={edges} disabled={busy} onAdd={(e) => change([...edges, e])} />}
      </Card>
    </div>
  );
}

function Legend() {
  return (
    <div className="row gap12 caption muted" style={{ alignItems: "center", marginBottom: 8 }}>
      <span className="row gap4" style={{ alignItems: "center" }}>
        <svg width="34" height="10"><line x1="2" y1="5" x2="26" y2="5" stroke={DISPATCH} strokeWidth="2" /><path d="M26 1 L32 5 L26 9 Z" fill={DISPATCH} /></svg>派工(目標、任務)
      </span>
      <span className="row gap4" style={{ alignItems: "center" }}>
        <svg width="34" height="10"><line x1="2" y1="5" x2="26" y2="5" stroke={REPORT} strokeWidth="1.5" strokeDasharray="4 3" /><path d="M26 1 L32 5 L26 9 Z" fill={REPORT} /></svg>回報
      </span>
    </div>
  );
}

function NodeChip({ node, name, agentById }: { node: string; name: string; agentById: Map<string, Agent> }) {
  const a = node.startsWith("agent:") ? agentById.get(node.slice(6)) : undefined;
  return (
    <span className="row gap6" style={{ alignItems: "center", minWidth: 0 }} title={node}>
      {a ? <AgentAvatar agent={a} size={20} /> : <Icon name={node.startsWith("user:") ? "person" : "questionmark.circle"} size={16} className="muted" />}
      <span className="ellipsis">{name}</span>
    </span>
  );
}

function AddEdge({ options, edges, disabled, onAdd }: { options: { value: string; label: string }[]; edges: ChainEdge[]; disabled: boolean; onAdd: (e: ChainEdge) => void }) {
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [label, setLabel] = useState("");
  const dup = !!from && !!to && edges.some((e) => e.from === from && e.to === to);
  const problem = from && to && from === to ? "不能指向自己" : dup ? "這條邊已經有了" : "";
  return (
    <div className="col gap6" style={{ paddingTop: 10 }}>
      <div className="row gap8" style={{ alignItems: "center", flexWrap: "wrap" }}>
        <select className="select" value={from} onChange={(e) => setFrom(e.target.value)}>
          <option value="">From(派工的人)</option>{options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
        </select>
        <Icon name="chevron.right" size={12} className="muted" />
        <input className="input" style={{ width: 120 }} placeholder="Label(預設 任務)" value={label} onChange={(e) => setLabel(e.target.value)} />
        <Icon name="chevron.right" size={12} className="muted" />
        <select className="select" value={to} onChange={(e) => setTo(e.target.value)}>
          <option value="">To(接工作的人)</option>{options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
        </select>
        <button className="btn" disabled={disabled || !from || !to || !!problem}
          onClick={() => { onAdd({ from, to, label: label.trim() }); setTo(""); setLabel(""); }}>
          <Icon name="plus" size={12} />新增邊
        </button>
      </div>
      {problem && <span className="caption" style={{ color: "var(--orange)" }}>{problem}</span>}
      <span className="caption muted">新增或刪除後按上方「存檔」才會生效;核心會檢查節點存在、不重複、不能有循環。</span>
    </div>
  );
}

/** 由上而下分層的圖:節點是 HTML(頭像、名稱),連線用 SVG(實線 = 派工,虛線 = 回報) */
function ChainGraph({ edges, name, agentById }: { edges: ChainEdge[]; name: (n: string) => string; agentById: Map<string, Agent> }) {
  const nodes = useMemo(() => {
    const s: string[] = [];
    for (const e of edges) for (const n of [e.from, e.to]) if (!s.includes(n)) s.push(n);
    return s;
  }, [edges]);
  const rows = useMemo(() => layers(nodes, edges), [nodes, edges]);
  const widest = Math.max(1, ...rows.map((r) => r.length));
  const width = PAD * 2 + widest * NODE_W + (widest - 1) * GAP_X;
  const height = PAD * 2 + rows.length * NODE_H + (rows.length - 1) * GAP_Y;
  const pos = new Map<string, { x: number; y: number }>();
  rows.forEach((row, li) => {
    const rowW = row.length * NODE_W + (row.length - 1) * GAP_X;
    const x0 = (width - rowW) / 2;
    row.forEach((n, i) => pos.set(n, { x: x0 + i * (NODE_W + GAP_X), y: PAD + li * (NODE_H + GAP_Y) }));
  });

  return (
    <div style={{ overflowX: "auto" }}>
      <div style={{ position: "relative", width, height, margin: "0 auto" }}>
        <svg width={width} height={height} style={{ position: "absolute", inset: 0 }}>
          <defs>
            <marker id="chain-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0 L10 5 L0 10 Z" fill={DISPATCH} /></marker>
            <marker id="chain-report" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path d="M0 0 L10 5 L0 10 Z" fill={REPORT} /></marker>
          </defs>
          {edges.map((e) => {
            const a = pos.get(e.from), b = pos.get(e.to);
            if (!a || !b) return null;
            // 派工:From 底部中間 → To 頂部中間(偏左一點);回報:To 頂部 → From 底部(偏右一點、虛線)
            const down = b.y > a.y;
            const off = 12;
            const x1 = a.x + NODE_W / 2 - off, y1 = down ? a.y + NODE_H : a.y;
            const x2 = b.x + NODE_W / 2 - off, y2 = down ? b.y : b.y + NODE_H;
            const my = (y1 + y2) / 2;
            const dispatch = `M${x1} ${y1} C${x1} ${my}, ${x2} ${my}, ${x2} ${y2}`;
            const rx1 = b.x + NODE_W / 2 + off, ry1 = y2;
            const rx2 = a.x + NODE_W / 2 + off, ry2 = y1;
            const report = `M${rx1} ${ry1} C${rx1} ${my}, ${rx2} ${my}, ${rx2} ${ry2}`;
            const lx = (x1 + x2) / 2 - off, ly = my;
            const text = labelOf(e);
            const tw = Math.max(28, [...text].length * 12 + 12);
            return (
              <g key={edgeKey(e)}>
                <title>{`${name(e.from)} 派「${text}」給 ${name(e.to)};${name(e.to)} 做完回報給 ${name(e.from)}`}</title>
                <path d={report} fill="none" stroke={REPORT} strokeWidth={1.4} strokeDasharray="5 4" markerEnd="url(#chain-report)" opacity={0.85} />
                <path d={dispatch} fill="none" stroke={DISPATCH} strokeWidth={2} markerEnd="url(#chain-arrow)" />
                <rect x={lx - tw / 2} y={ly - 10} width={tw} height={20} rx={10} fill="var(--bg2)" stroke={DISPATCH} strokeWidth={1} />
                <text x={lx} y={ly + 4} textAnchor="middle" fontSize={11.5} fill="var(--fg)">{text}</text>
              </g>
            );
          })}
        </svg>
        {nodes.map((n) => {
          const p = pos.get(n)!;
          const a = n.startsWith("agent:") ? agentById.get(n.slice(6)) : undefined;
          const isUser = n.startsWith("user:");
          return (
            <div key={n} title={n} className="row gap6"
              style={{
                position: "absolute", left: p.x, top: p.y, width: NODE_W, height: NODE_H, boxSizing: "border-box", padding: "0 10px",
                alignItems: "center", background: "var(--bg2)", border: `1px solid ${a || isUser ? "var(--line)" : "var(--orange)"}`, borderRadius: 10,
                boxShadow: "0 1px 2px rgba(0,0,0,.06)",
              }}>
              {a ? <AgentAvatar agent={a} size={26} />
                : <span className="bx-avatar" style={{ width: 26, height: 26, background: "var(--gray)" }}><Icon name={isUser ? "person" : "questionmark.circle"} size={13} color="white" /></span>}
              <div className="col" style={{ minWidth: 0 }}>
                <span className="ellipsis" style={{ fontWeight: 500 }}>{name(n)}</span>
                <span className="caption muted ellipsis">{a ? a.role || "虛擬角色" : isUser ? n.slice(5) : "不存在的節點"}</span>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
