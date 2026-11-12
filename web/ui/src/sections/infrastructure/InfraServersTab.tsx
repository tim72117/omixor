// 伺服器分頁:篩選列、主機卡片格線、右側詳細面板(Swift 的 InfraServersTab.swift + InfraServerDetail.swift)
import { useEffect, useState } from "react";
import { action, tool } from "../../api";
import { Card, color, EmptyState, Pill } from "../../components/ui";
import {
  copyText, ENV, ENV_ORDER, EnvBadge, HEALTH, HEALTH_ORDER, HealthBadge, SearchField, SERIES_COLORS, SeriesChart, SIcon, Sparkline, StatusDot,
  UsageBar, useAct, useConfirm, useOps, useWidth, type EnvKey, type HealthKey, type Server,
} from "../../components/opsKit";
import { Chip, SectionHeader, usageColor } from "./infraShared";

export function InfraServersTab({ selection, setSelection, servers, onOpenService }: { selection?: string; setSelection: (id?: string) => void; servers: Server[]; onOpenService: (id: string) => void }) {
  const o = useOps();
  const env = (o.nav?.env ?? "production") as EnvKey;
  const [search, setSearch] = useState("");
  const [envFilter, setEnvFilter] = useState<EnvKey | "">(env);
  const [region, setRegion] = useState("");
  const [health, setHealth] = useState<HealthKey | "">("");
  const [drained, setDrained] = useState<Set<string>>(new Set());
  const [ref, w] = useWidth<HTMLDivElement>();
  useEffect(() => setEnvFilter(env), [env]);
  const regions = [...new Set(servers.map((s) => s.region))].sort();
  const q = search.trim().toLowerCase();
  const items = servers.filter((s) => (!envFilter || s.env === envFilter) && (!region || s.region === region) && (!health || s.health === health)
    && (!q || s.name.toLowerCase().includes(q) || s.role.toLowerCase().includes(q) || s.ip.includes(q) || s.tags.some((t) => t.toLowerCase().includes(q))))
    .sort((a, b) => a.env.localeCompare(b.env) || a.name.localeCompare(b.name));
  // 被選中的主機若被篩選藏起來,就放寬篩選讓它出現
  useEffect(() => {
    const s = servers.find((x) => x.id === selection);
    if (!s || items.some((x) => x.id === s.id)) return;
    setEnvFilter(s.env); setRegion(""); setHealth(""); setSearch("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selection, servers.length]);
  if (servers.length === 0) return <EmptyState title="還沒有主機" icon="server.rack" message="接上雲端供應商後,這裡會列出所有主機與容器。" />;
  const hasFilters = !!region || !!health || !!search || envFilter !== env;
  const down = items.filter((s) => s.health === "down").length;
  const degraded = items.filter((s) => s.health === "degraded").length;
  const avgCPU = items.length ? items.reduce((a, s) => a + s.cpu, 0) / items.length : 0;
  const sel = servers.find((s) => s.id === selection);
  return (
    <div className="ops-page">
      <div className="row wrap" style={{ gap: 10, padding: "10px 16px", borderBottom: "1px solid var(--line)" }}>
        <SearchField value={search} onChange={setSearch} placeholder="搜尋名稱、角色、IP、標籤" style={{ minWidth: 140, maxWidth: 240, flex: 1 }} />
        <select className="select" title="環境" value={envFilter} onChange={(e) => setEnvFilter(e.target.value as EnvKey | "")}>
          <option value="">全部環境</option><option disabled>──────</option>{ENV_ORDER.map((e) => <option key={e} value={e}>{ENV[e].label}</option>)}
        </select>
        <select className="select" title="區域" value={region} onChange={(e) => setRegion(e.target.value)}>
          <option value="">全部區域</option><option disabled>──────</option>{regions.map((r) => <option key={r} value={r}>{r}</option>)}
        </select>
        <select className="select" title="健康狀態" value={health} onChange={(e) => setHealth(e.target.value as HealthKey | "")}>
          <option value="">全部狀態</option><option disabled>──────</option>{HEALTH_ORDER.map((h) => <option key={h} value={h}>{HEALTH[h].label}</option>)}
        </select>
        {hasFilters && <button className="btn link" onClick={() => { setSearch(""); setRegion(""); setHealth(""); setEnvFilter(env); }}>重設</button>}
        <span className="grow" />
        <span className="row gap12 muted num">
          <span>{items.length} 台</span>
          {down > 0 && <span className="row gap4" style={{ color: "var(--red)" }}><SIcon name="xmark.octagon.fill" size={12} color="red" />{down} 中斷</span>}
          {degraded > 0 && <span className="row gap4" style={{ color: "var(--orange)" }}><SIcon name="exclamationmark.triangle.fill" size={12} color="orange" />{degraded} 效能下降</span>}
          <span>平均 CPU {Math.trunc(avgCPU)}%</span>
        </span>
      </div>
      <div ref={ref} className="ops-fill">
        {items.length === 0 ? <EmptyState title="沒有符合的主機" icon="line.3.horizontal.decrease.circle" message="調整環境、區域或狀態篩選。" /> : (
          <div className="ops-scroll" style={{ padding: 16 }}>
            <div className="ops-grid-auto" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(230px, 1fr))" }}>
              {items.map((s) => <HostCard key={s.id} s={s} selected={s.id === selection} drained={drained.has(s.id)} onClick={() => setSelection(selection === s.id ? undefined : s.id)} />)}
            </div>
          </div>
        )}
        {sel && (
          <div style={{ width: Math.min(480, Math.max(330, w * 0.45)), flex: "none", borderLeft: "1px solid var(--line)", overflowY: "auto", background: "var(--bg)" }}>
            <ServerDetail key={sel.id} s={sel} drained={drained.has(sel.id)} setDrained={(v) => setDrained((d) => { const n = new Set(d); if (v) n.add(sel.id); else n.delete(sel.id); return n; })}
              onClose={() => setSelection(undefined)} onOpenService={onOpenService} />
          </div>
        )}
      </div>
    </div>
  );
}

function HostCard({ s, selected, drained, onClick }: { s: Server; selected: boolean; drained: boolean; onClick: () => void }) {
  const bad = s.containers.filter((c) => c.status !== "healthy").length;
  const hc = color(HEALTH[s.health].color);
  return (
    <button className="ops-tile col gap8" onClick={onClick} style={{ padding: 12, borderRadius: 12, background: "var(--bg2)", border: selected ? "2px solid var(--accent)" : `1px solid ${s.health === "healthy" ? "var(--line)" : `color-mix(in srgb, ${hc} 45%, transparent)`}` }}>
      <div className="row" style={{ gap: 7, width: "100%" }}>
        <StatusDot c={HEALTH[s.health].color} pulsing={s.health === "down"} />
        <span className="card-title ellipsis">{s.name}</span><span className="grow" />
        {drained && <Pill text="已排空" color="blue" icon="drop.triangle" />}
        <EnvBadge env={s.env} />
      </div>
      <span className="caption muted ellipsis" style={{ width: "100%" }}>{s.role} · {s.provider} {s.region}</span>
      <div className="col gap4" style={{ width: "100%" }}><UsageBar value={s.cpu} label="CPU" /><UsageBar value={s.memory} label="RAM" /><UsageBar value={s.disk} label="磁碟" /></div>
      {s.cpuSeries.length > 1 && <div style={{ width: "100%", pointerEvents: "none" }}><Sparkline values={s.cpuSeries.slice(-30).map((p) => p.value)} c={usageColor(s.cpu)} /></div>}
      <div className="row caption muted" style={{ width: "100%", gap: 6 }}>
        <SIcon name="shippingbox" size={12} />{s.containers.length} 個容器
        {bad > 0 && <span style={{ color: "var(--orange)" }}>{bad} 異常</span>}
        <span className="grow" />運作 {s.uptimeDays} 天
      </div>
    </button>
  );
}

function ServerDetail({ s, drained, setDrained, onClose, onOpenService }: { s: Server; drained: boolean; setDrained: (v: boolean) => void; onClose: () => void; onOpenService: (id: string) => void }) {
  const o = useOps();
  const [dlg, ask] = useConfirm();
  const { run, toast, toastEl } = useAct();
  const ssh = `ssh ops@${s.ip}`;
  const post = (icon: string, text: string) => tool("ops_activity_post", { icon, text, section: "infrastructure" });
  const list = o.services.filter((x) => x.hostIds.includes(s.id));
  const liveStat = (title: string, v: string, c: string) => (
    <div className="col" style={{ gap: 1 }}><span className="row gap4 caption muted"><span className="dot" style={{ width: 6, height: 6, background: color(c) }} />{title}</span><span className="num" style={{ fontSize: 15, fontWeight: 500 }}>{v}</span></div>
  );
  const spec = (k: string, v: string, mono?: boolean) => <><span className="muted">{k}</span><span className={mono ? "mono" : undefined} style={{ userSelect: "text" }}>{v}</span></>;
  return (
    <div className="col" style={{ padding: 16, gap: 14 }}>
      <div className="col gap6">
        <div className="row gap8"><span className="h3 ellipsis" style={{ userSelect: "text" }}>{s.name}</span><span className="grow" /><button className="ops-icon-btn" title="關閉" onClick={onClose}><SIcon name="xmark" /></button></div>
        <div className="row gap6"><HealthBadge h={s.health} /><EnvBadge env={s.env} />{drained && <Pill text="已排空" color="blue" icon="drop.triangle" />}</div>
        <span className="muted">{s.role}</span>
      </div>
      <div className="row gap8 wrap">
        <button className="btn" title={ssh} onClick={() => { copyText(ssh); toast(`已複製「${ssh}」`); }}><SIcon name="terminal" size={12} />複製 SSH 指令</button>
        <button className="btn" onClick={async () => {
          if (drained) { const r = await run(() => post("arrow.uturn.left", `恢復 ${s.name} 接收流量`), `${s.name} 已恢復接收流量`); if (r !== undefined) setDrained(false); }
          else { const r = await run(() => post("drop.triangle", `排空 ${s.name}(停止分配新流量)`), `${s.name} 已排空,不再分配新流量`); if (r !== undefined) setDrained(true); }
        }}><SIcon name={drained ? "arrow.uturn.left" : "drop.triangle"} size={12} />{drained ? "恢復流量" : "排空"}</button>
        <button className="btn danger" disabled={s.health === "maintenance"} onClick={() => ask({
          title: `要重新開機 ${s.name} 嗎?`,
          message: `主機上 ${s.containers.length} 個容器會一起中斷,約需 1–2 分鐘恢復。${s.env === "production" ? "這是正式環境主機,建議先排空流量。" : ""}`,
          buttons: [{ label: "重新開機", danger: true, onClick: () => run(() => action("server_reboot", { id: s.id }), `${s.name} 重新開機中…`) }],
        })}><SIcon name="restart" size={12} />重新開機</button>
      </div>
      <Card>
        <div className="row"><span className="card-title">即時資源</span><span className="grow" /><span className="row gap4 caption muted"><StatusDot c="green" pulsing />每 3 秒更新</span></div>
        <div className="row" style={{ gap: 18 }}>{liveStat("CPU", `${Math.trunc(s.cpu)}%`, "blue")}{liveStat("記憶體", `${Math.trunc(s.memory)}%`, "purple")}{liveStat("網路", `${Math.trunc(s.networkMbps)} Mbps`, "teal")}</div>
        {s.cpuSeries.length > 1 || s.memorySeries.length > 1
          ? <SeriesChart series={[{ name: "CPU", points: s.cpuSeries }, { name: "記憶體", points: s.memorySeries }]} colors={[SERIES_COLORS[0], SERIES_COLORS[3]]} height={170} threshold={85} yDomain={[0, 100]} yFormat={(v) => `${v.toFixed(0)}%`} />
          : <div className="faint" style={{ minHeight: 80, textAlign: "center" }}>等待資料…</div>}
        <UsageBar value={s.disk} label="磁碟" />
      </Card>
      <Card title="規格">
        <div className="ops-kbd-row">
          {spec("供應商", s.provider)}{spec("區域", s.region)}{spec("作業系統", s.os)}{spec("IP", s.ip, true)}
          {spec("CPU", `${s.cores} 核心`)}{spec("記憶體", `${s.memoryGB} GB`)}{spec("運作時間", s.uptimeDays === 0 ? "剛重新開機" : `${s.uptimeDays} 天`)}
        </div>
      </Card>
      <Card>
        <SectionHeader title="執行的服務" trailing={`${list.length} 個`} />
        {list.length === 0 ? <span className="faint">沒有登記在這台主機上的服務</span>
          : <div className="row gap6 wrap">{list.map((x) => <Chip key={x.id} text={x.name} c={HEALTH[x.health].color} icon={HEALTH[x.health].icon} onClick={() => onOpenService(x.id)} />)}</div>}
      </Card>
      <Card>
        <SectionHeader title="容器" trailing={`${s.containers.length} 個`} />
        {s.containers.length === 0 ? <span className="faint">沒有執行中的容器</span> : (
          <table className="ops-table">
            <thead><tr><th style={{ background: "transparent" }}>名稱</th><th style={{ background: "transparent" }}>狀態</th><th style={{ background: "transparent", textAlign: "right" }}>重啟</th><th style={{ background: "transparent", textAlign: "right" }}>CPU</th><th style={{ background: "transparent", textAlign: "right" }}>記憶體</th></tr></thead>
            <tbody>
              {s.containers.map((c) => (
                <tr key={c.id}>
                  <td style={{ maxWidth: 180 }}><div className="col" style={{ gap: 1 }}><span className="ellipsis" style={{ fontWeight: 500 }}>{c.name}</span><span className="mono muted ellipsis" style={{ fontSize: 10.5 }} title={c.image}>{c.image}</span></div></td>
                  <td><HealthBadge h={c.status} compact /></td>
                  <td className="num" style={{ textAlign: "right", color: c.restarts > 3 ? "var(--orange)" : undefined, fontWeight: c.restarts > 3 ? 600 : undefined }}>{c.restarts}</td>
                  <td className="num" style={{ textAlign: "right" }}>{c.cpu.toFixed(0)}%</td>
                  <td className="num" style={{ textAlign: "right" }}>{c.memoryMB >= 1024 ? `${(c.memoryMB / 1024).toFixed(1)} GB` : `${c.memoryMB.toFixed(0)} MB`}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
      {s.tags.length > 0 && (
        <Card title="標籤">
          <div className="row gap6 wrap">{s.tags.map((t) => <span key={t} className="mono" style={{ fontSize: 11, padding: "3px 7px", borderRadius: 5, background: "color-mix(in srgb, var(--fg) 6%, transparent)" }}>{t}</span>)}</div>
        </Card>
      )}
      {dlg}{toastEl}
    </div>
  );
}
