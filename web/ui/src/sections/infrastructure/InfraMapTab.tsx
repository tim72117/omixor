// 服務地圖:依相依深度分層排列,上層呼叫下層。節點顏色代表健康狀態(Swift 的 InfraMapTab.swift)
import { useState } from "react";
import { color, EmptyState } from "../../components/ui";
import { HEALTH, HealthBadge, num, pct, SIcon, useOps, useWidth, type HealthKey, type Service } from "../../components/opsKit";
import { TierPill } from "./infraShared";

function graphModel(services: Service[]) {
  const byId = new Map(services.map((s) => [s.id, s]));
  const level = new Map<string, number>();
  const visiting = new Set<string>();
  const depth = (id: string): number => {
    if (level.has(id)) return level.get(id)!;
    const s = byId.get(id);
    if (!s || visiting.has(id)) return 0;
    visiting.add(id);
    const deps = s.dependencies.filter((d) => byId.has(d) && d !== id);
    const l = deps.length ? Math.max(...deps.map(depth)) + 1 : 0;
    visiting.delete(id);
    level.set(id, l);
    return l;
  };
  services.forEach((s) => depth(s.id));
  const maxLevel = Math.max(0, ...level.values());
  const rows: Service[][] = Array.from({ length: maxLevel + 1 }, () => []);
  services.forEach((s) => rows[maxLevel - (level.get(s.id) ?? 0)].push(s));
  const order = new Map<string, number>();
  const bary = (id: string) => {
    const callers = services.filter((s) => s.dependencies.includes(id)).map((s) => order.get(s.id)).filter((x): x is number => x != null);
    return callers.length ? callers.reduce((a, b) => a + b, 0) / callers.length : 2;
  };
  const byTier = (a: Service, b: Service) => a.tier - b.tier || a.name.localeCompare(b.name);
  rows.forEach((row, r) => {
    if (r === 0) row.sort(byTier);
    else row.sort((a, b) => bary(a.id) - bary(b.id) || byTier(a, b));
    row.forEach((s, i) => order.set(s.id, (i + 0.5) / Math.max(1, row.length)));
  });
  const layers = rows.filter((r) => r.length);
  const edges = services.flatMap((s) => s.dependencies.filter((d) => byId.has(d) && d !== s.id).map((d) => ({ from: s.id, to: d })));
  return { layers, edges };
}

const NODE_H = 56, ROW = 112;

export function InfraMapTab({ selection, setSelection, onOpenService }: { selection?: string; setSelection: (id?: string) => void; onOpenService: (id: string) => void }) {
  const o = useOps();
  const [hovered, setHovered] = useState<string>();
  const [ref, w] = useWidth<HTMLDivElement>();
  if (o.services.length === 0) return <EmptyState title="沒有可以畫的服務" icon="point.3.connected.trianglepath.dotted" message="服務目錄有資料後,這裡會畫出服務之間的相依關係。" />;
  const { layers, edges } = graphModel(o.services);
  const maxCount = Math.max(1, ...layers.map((l) => l.length));
  const width = Math.max(w, maxCount * 150 + 40);
  const height = Math.max(420, layers.length * ROW + 24);
  const nodeW = Math.min(176, width / maxCount - 18);
  const rowH = (height - 24) / Math.max(1, layers.length);
  const pos = new Map<string, { x: number; y: number }>();
  layers.forEach((layer, r) => layer.forEach((s, i) => pos.set(s.id, { x: (width / layer.length) * (i + 0.5), y: 12 + rowH * (r + 0.5) })));
  const focus = hovered ?? selection;
  const related = new Set<string>();
  const fs = focus ? o.service(focus) : undefined;
  if (fs) { related.add(fs.id); fs.dependencies.forEach((d) => related.add(d)); o.services.filter((s) => s.dependencies.includes(fs.id)).forEach((s) => related.add(s.id)); }
  const health = new Map(o.services.map((s) => [s.id, s.health]));
  const half = NODE_H / 2;
  const sel = selection ? o.service(selection) : undefined;

  return (
    <div className="ops-page">
      <div className="row caption muted" style={{ gap: 14, padding: "8px 16px", borderBottom: "1px solid var(--line)" }}>
        {(["healthy", "degraded", "down", "maintenance"] as HealthKey[]).map((h) => <span key={h} className="row" style={{ gap: 5 }}><span style={{ width: 10, height: 10, borderRadius: 3, background: color(HEALTH[h].color) }} />{HEALTH[h].label}</span>)}
        <span className="grow" />
        <span className="row gap4"><SIcon name="arrow.down" size={11} />箭頭由呼叫方指向被依賴的服務</span>
      </div>
      <div ref={ref} className="ops-scroll" style={{ background: "color-mix(in srgb, var(--bg2) 35%, transparent)" }}>
        <div style={{ position: "relative", width, height }} onClick={() => setSelection(undefined)}>
          <svg width={width} height={height} style={{ position: "absolute", inset: 0, pointerEvents: "none" }}>
            {edges.map((e) => {
              const a = pos.get(e.from), b = pos.get(e.to);
              if (!a || !b) return null;
              const active = !!focus && (e.from === focus || e.to === focus);
              const dim = !!focus && !active;
              const target = health.get(e.to) ?? "unknown";
              const c = active ? "var(--accent)" : target === "healthy" ? "var(--fg2)" : color(HEALTH[target].color);
              const op = dim ? 0.12 : active ? 0.95 : target === "healthy" ? 0.4 : 0.8;
              const start = { x: a.x, y: a.y + half }, end = { x: b.x, y: b.y - half - 2 };
              let d: string;
              if (Math.abs(end.y - start.y) < 4 || end.y < start.y) {
                const lift = Math.max(40, Math.abs(b.x - a.x) * 0.25);
                d = `M${a.x},${a.y - half} C${a.x},${a.y - half - lift} ${b.x},${b.y - half - lift} ${b.x},${b.y - half - 2}`;
              } else {
                const dy = (end.y - start.y) * 0.5;
                d = `M${start.x},${start.y} C${start.x},${start.y + dy} ${end.x},${end.y - dy} ${end.x},${end.y}`;
              }
              return (
                <g key={`${e.from}>${e.to}`} opacity={op}>
                  <path d={d} fill="none" stroke={c} strokeWidth={active ? 2 : 1.3} strokeLinecap="round" strokeDasharray={target === "down" && !active ? "5 4" : undefined} />
                  <path d={`M${end.x},${end.y} L${end.x - 4.5},${end.y - 8} L${end.x + 4.5},${end.y - 8} Z`} fill={c} />
                </g>
              );
            })}
          </svg>
          {layers.flat().map((s) => {
            const p = pos.get(s.id)!;
            const c = color(HEALTH[s.health].color);
            const selected = selection === s.id;
            const dimmed = !!focus && !related.has(s.id);
            return (
              <div key={s.id} className="ops-map-node" title={`${s.name}:${HEALTH[s.health].label}。點兩下查看詳情`}
                style={{
                  left: p.x, top: p.y, width: nodeW, height: NODE_H, opacity: dimmed ? 0.4 : 1,
                  background: `linear-gradient(color-mix(in srgb, ${c} ${s.health === "healthy" ? 4 : 12}%, transparent), color-mix(in srgb, ${c} ${s.health === "healthy" ? 4 : 12}%, transparent)), var(--bg2)`,
                  border: selected ? "2.5px solid var(--accent)" : `1.2px solid color-mix(in srgb, ${c} 70%, transparent)`,
                  boxShadow: selected ? "0 2px 10px rgba(0,0,0,.18)" : "0 1px 3px rgba(0,0,0,.06)",
                }}
                onClick={(e) => { e.stopPropagation(); setSelection(selection === s.id ? undefined : s.id); }}
                onDoubleClick={(e) => { e.stopPropagation(); onOpenService(s.id); }}
                onMouseEnter={() => setHovered(s.id)} onMouseLeave={() => setHovered((h) => (h === s.id ? undefined : h))}>
                <div className="row" style={{ gap: 5 }}>
                  <SIcon name={HEALTH[s.health].icon} color={HEALTH[s.health].color} size={12} />
                  <span className="ellipsis" style={{ fontWeight: 600 }}>{s.name}</span><span className="grow" />
                  <span className="muted" style={{ fontSize: 10, fontWeight: 600 }}>T{s.tier}</span>
                </div>
                <span className="muted num ellipsis" style={{ fontSize: 10.5 }}>p95 {Math.trunc(s.latencyP95)}ms · {pct(s.errorRate, 2)}</span>
              </div>
            );
          })}
        </div>
      </div>
      {sel && (
        <div className="row" style={{ gap: 12, padding: "10px 16px", borderTop: "1px solid var(--line)", background: "var(--bg2)" }}>
          <HealthBadge h={sel.health} />
          <span className="card-title ellipsis">{sel.name}</span>
          <TierPill tier={sel.tier} />
          <span className="row muted num ellipsis" style={{ gap: 12 }}>
            <span>依賴 {sel.dependencies.length} · 被依賴 {o.services.filter((s) => s.dependencies.includes(sel.id)).length}</span>
            <span>p95 {Math.trunc(sel.latencyP95)}ms</span><span>錯誤 {pct(sel.errorRate, 2)}</span><span>{num(sel.rps)} rps</span>
          </span>
          <span className="grow" />
          <button className="btn link" onClick={() => setSelection(undefined)}>取消選取</button>
          <button className="btn primary" onClick={() => onOpenService(sel.id)}><SIcon name="arrow.right.circle" size={12} />查看詳情</button>
        </div>
      )}
    </div>
  );
}
