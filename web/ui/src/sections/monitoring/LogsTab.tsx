// 日誌分頁:查詢列(支援 level:error service:api-server trace:abc 語法)、等級切換、服務篩選、
// 依等級堆疊的量體直方圖、可展開的日誌列,以及「即時追蹤」(Swift 的 MonLogsTab)
import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import { Bar, BarChart, CartesianGrid, Cell, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { useView } from "../../api";
import { SearchField, StatusDot, ToggleChip, copyText, useContextMenu } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { LOG_COLOR, LOG_LEVELS, arr, type LogEntry, type LogLevel, type Server, type Service } from "../../components/models";
import { EmptyState, color } from "../../components/ui";
import { useTimeRange } from "./MonitoringView";

const label = (l: LogLevel) => l.toUpperCase();

// MARK: 查詢解析
class LogQuery {
  levels = new Set<LogLevel>(); services: string[] = []; hosts: string[] = []; traces: string[] = []; terms: string[] = []; excluded: string[] = [];
  constructor(raw: string) {
    for (const tok of raw.split(/\s+/).filter(Boolean)) {
      const lower = tok.toLowerCase();
      if (lower.startsWith("-") && lower.length > 1) { this.excluded.push(lower.slice(1)); continue; }
      const c = lower.indexOf(":");
      if (c >= 0) {
        const key = lower.slice(0, c), value = lower.slice(c + 1);
        if (!value) continue;
        if (["level", "lvl", "等級"].includes(key)) {
          for (let v of value.split(",")) { v = v === "warning" ? "warn" : v === "err" ? "error" : v; if ((LOG_LEVELS as string[]).includes(v)) this.levels.add(v as LogLevel); }
          continue;
        }
        if (["service", "svc", "服務"].includes(key)) { this.services.push(value); continue; }
        if (["host", "主機"].includes(key)) { this.hosts.push(value); continue; }
        if (["trace", "traceid"].includes(key)) { this.traces.push(value); continue; }
      }
      this.terms.push(lower);
    }
  }
  matches(e: LogEntry): boolean {
    if (this.levels.size && !this.levels.has(e.level)) return false;
    if (this.services.length && !this.services.some((s) => e.service.toLowerCase().includes(s))) return false;
    if (this.hosts.length && !this.hosts.some((s) => e.host.toLowerCase().includes(s))) return false;
    if (this.traces.length) { const t = e.traceId?.toLowerCase(); if (!t || !this.traces.some((x) => t.startsWith(x))) return false; }
    if (!this.terms.length && !this.excluded.length) return true;
    const hay = `${e.message} ${e.service} ${e.host}`.toLowerCase();
    return this.terms.every((t) => hay.includes(t)) && !this.excluded.some((t) => hay.includes(t));
  }
}

// MARK: 模擬新日誌
const pick = <T,>(xs: T[]) => xs[Math.floor(Math.random() * xs.length)];
const rint = (a: number, b: number) => a + Math.floor(Math.random() * (b - a + 1));
function synthMessage(level: LogLevel): string {
  const path = pick(["/api/v1/courses", "/api/v1/lessons/42", "/api/v1/auth/session", "/api/v1/progress", "/healthz", "/api/v1/billing/webhook"]);
  const ms = rint(4, 240);
  switch (level) {
    case "debug": return pick([`cache hit key=lesson:${rint(100, 999)}`, `db pool idle=${rint(2, 12)} busy=${rint(1, 8)}`, "feature flag evaluated name=new_player value=true", `gc pause ${rint(1, 9)}ms`]);
    case "info": return pick([`GET ${path} 200 ${ms}ms`, `POST ${path} 201 ${ms}ms`, "user session refreshed", `job completed queue=email id=${rint(10000, 99999)} took=${ms}ms`]);
    case "warn": return pick([`slow query took=${rint(500, 2400)}ms table=progress`, `GET ${path} 200 ${rint(800, 2500)}ms (slow)`, "retrying upstream request attempt=2", `rate limit near threshold client=${rint(1, 40)}`]);
    default: return pick([`GET ${path} 502 upstream connect timeout`, "unhandled exception: connection reset by peer", `POST ${path} 500 database deadlock detected`, "payment webhook signature mismatch"]);
  }
}

const timeMs = (iso: string) => {
  const d = new Date(iso);
  return d.toLocaleTimeString("zh-TW", { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0");
};

export function LogsTab() {
  const [logsRaw, err] = useView<LogEntry[]>("logs");
  const [servicesRaw] = useView<Service[]>("services");
  const [serversRaw] = useView<Server[]>("servers");
  const tr = useTimeRange();
  const [query, setQuery] = useState("");
  const [levels, setLevels] = useState<Set<LogLevel>>(new Set(LOG_LEVELS));
  const [service, setService] = useState<string>("");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [live, setLive] = useState(false);
  const [tailed, setTailed] = useState<LogEntry[]>([]);
  const [slice, setSlice] = useState<[number, number]>();
  const [helpOpen, setHelpOpen] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);
  const services = arr<Service>(servicesRaw);
  const svcName = (id: string) => services.find((s) => s.id === id)?.name ?? id;

  const all = useMemo(() => [...arr<LogEntry>(logsRaw), ...tailed], [logsRaw, tailed]);
  const start = Date.now() - tr.seconds * 1000;
  const windowLogs = all.filter((e) => new Date(e.date).getTime() >= start);
  const svcList = [...new Set(all.map((e) => e.service))].sort();
  const parsed = useMemo(() => new LogQuery(query), [query]);
  const base = windowLogs.filter((e) => (!service || e.service === service) && levels.has(e.level) && parsed.matches(e));
  const rows = (slice ? base.filter((e) => { const t = new Date(e.date).getTime(); return t >= slice[0] && t <= slice[1]; }) : base)
    .sort((a, b) => b.date.localeCompare(a.date));
  const counts: Record<string, number> = {};
  windowLogs.filter((e) => (!service || e.service === service) && parsed.matches(e)).forEach((e) => (counts[e.level] = (counts[e.level] ?? 0) + 1));

  // 即時追蹤:每 0.6–1.6 秒加一筆模擬日誌
  const allRef = useRef(all);
  allRef.current = all;
  useEffect(() => {
    if (!live) return;
    let timer = 0;
    const tick = () => {
      const svcs = svcList.length ? svcList : services.length ? services.map((s) => s.id) : ["api-server", "web", "worker"];
      const svc = pick(svcs);
      const hosts = [...new Set(allRef.current.filter((e) => e.service === svc).map((e) => e.host))];
      const s = services.find((x) => x.id === svc);
      const host = hosts.length ? pick(hosts) : (s?.hostIds?.length ? arr<Server>(serversRaw).find((x) => x.id === pick(s.hostIds))?.name : undefined) ?? `${svc}-${rint(1, 3)}`;
      const roll = Math.random();
      const level: LogLevel = roll < 0.06 ? "error" : roll < 0.18 ? "warn" : roll < 0.8 ? "info" : "debug";
      const trace = level === "debug" ? null : crypto.randomUUID().replace(/-/g, "").slice(0, 16);
      setTailed((t) => [...t, { id: crypto.randomUUID(), date: new Date().toISOString(), level, service: svc, host, message: synthMessage(level), traceId: trace }].slice(-2000));
      listRef.current?.scrollTo({ top: 0 });
      timer = window.setTimeout(tick, 600 + Math.random() * 1000);
    };
    timer = window.setTimeout(tick, 600 + Math.random() * 1000);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [live]);

  const addToken = (token: string) => {
    const trimmed = query.trim();
    const existing = new Set(trimmed.split(" "));
    const add = token.split(" ").filter((t) => !existing.has(t));
    if (!add.length) return;
    setQuery([trimmed, ...add].filter(Boolean).join(" "));
  };
  const toggle = (id: string) => setExpanded((s) => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n; });
  const levelMenu = useContextMenu();

  if (err) return <EmptyState title="讀不到日誌" icon="exclamationmark.triangle" message={err} />;

  return (
    <div className="col" style={{ flex: 1, minHeight: 0, padding: 20, gap: 12 }}>
      <div className="row gap8">
        <div className="grow row" style={{ position: "relative" }}>
          <div style={{ flex: 1 }}><SearchField value={query} onChange={setQuery} mono placeholder="搜尋日誌,例如 level:error service:api-server timeout" /></div>
          <button className="bx-icon-btn" title="查詢語法" onClick={() => setHelpOpen(!helpOpen)} style={{ position: "absolute", right: query ? 30 : 8 }}>
            <Icon name="questionmark.circle" size={14} />
          </button>
          {helpOpen && (
            <div className="bx-menu" style={{ right: 0, top: 32 }} onMouseLeave={() => setHelpOpen(false)}>
              <button className="menu-item" onClick={() => { addToken("level:error"); setHelpOpen(false); }}>只看錯誤 level:error</button>
              <button className="menu-item" onClick={() => { addToken("level:warn level:error"); setHelpOpen(false); }}>警告以上 level:warn level:error</button>
              <div className="menu-sep" />
              {svcList.map((s) => <button key={s} className="menu-item" onClick={() => { addToken(`service:${s}`); setHelpOpen(false); }}>service:{s}</button>)}
              <div className="menu-sep" />
              <div className="caption muted" style={{ padding: "4px 8px", maxWidth: 300 }}>語法:level:、service:、host:、trace:,其餘文字比對訊息;前面加 - 代表排除</div>
            </div>
          )}
        </div>
        <button className={"btn" + (live ? " primary" : "")} style={live ? { background: "var(--green)", borderColor: "var(--green)" } : undefined}
          title={live ? "停止即時追蹤" : "持續接收新日誌"} onClick={() => setLive(!live)}>
          {live ? <StatusDot c="white" pulsing /> : <Icon name="dot.radiowaves.left.and.right" size={13} />} 即時追蹤
        </button>
      </div>
      <div className="row gap8">
        {LOG_LEVELS.map((l) => (
          <ToggleChip key={l} title={label(l)} count={counts[l] ?? 0} c={LOG_COLOR[l]} on={levels.has(l)}
            onClick={() => setLevels((s) => { const n = new Set(s); if (n.has(l)) n.delete(l); else n.add(l); return n; })}
            onContextMenu={levelMenu.open([{ label: `只看 ${label(l)}`, onClick: () => setLevels(new Set([l])) }])} />
        ))}
        {levelMenu.node}
        <div className="grow" />
        <select className="select" value={service} onChange={(e) => setService(e.target.value)}>
          <option value="">全部服務</option>
          {svcList.map((s) => <option key={s} value={s}>{svcName(s)}</option>)}
        </select>
      </div>
      <div style={{ height: 110, flex: "none" }}>
        <Histogram entries={base} selection={slice} onSelect={setSlice} />
      </div>
      <div className="row gap8 caption">
        <span className="muted num">{rows.length} 筆</span>
        {slice ? (
          <span className="pill" style={{ background: "color-mix(in srgb, var(--accent) 12%, transparent)" }}>
            <Icon name="clock" size={11} />
            {new Date(slice[0]).toLocaleTimeString("zh-TW", { hour12: false })}–{new Date(slice[1]).toLocaleTimeString("zh-TW", { hour12: false })}
            <button className="bx-icon-btn" style={{ padding: 0 }} title="清除時間篩選" onClick={() => setSlice(undefined)}><Icon name="xmark.circle.fill" size={11} /></button>
          </span>
        ) : <span className="faint">點直方圖可只看該時段</span>}
        <div className="grow" />
        {expanded.size > 0 && <button className="btn link caption" onClick={() => setExpanded(new Set())}>全部收合</button>}
        {live && <span style={{ color: "var(--green)" }}>已接收 {tailed.length} 筆新日誌</span>}
      </div>
      <div ref={listRef} style={{ flex: 1, minHeight: 0, overflowY: "auto", background: "var(--bg2)", borderRadius: 10, border: "1px solid var(--line)" }}>
        {logsRaw === undefined ? <div className="muted" style={{ padding: 20 }}>載入中…</div>
          : rows.length === 0 ? (
            <EmptyState title={all.length === 0 ? "沒有日誌" : "沒有符合條件的日誌"} icon="text.magnifyingglass"
              message={all.length === 0 ? "開啟「即時追蹤」開始接收日誌。" : "調整查詢、等級或時間範圍試試看。"} />
          ) : rows.slice(0, 1500).map((e) => (
            <LogRow key={e.id} e={e} expanded={expanded.has(e.id)} serviceName={svcName(e.service)} onToggle={() => toggle(e.id)} onFilter={addToken} />
          ))}
      </div>
    </div>
  );
}

function Histogram({ entries, selection, onSelect }: { entries: LogEntry[]; selection?: [number, number]; onSelect: (r?: [number, number]) => void }) {
  if (!entries.length) {
    return <div className="row caption muted" style={{ height: "100%", justifyContent: "center", borderRadius: 10, background: "color-mix(in srgb, var(--fg) 3%, transparent)" }}>這段時間沒有日誌</div>;
  }
  const ts = entries.map((e) => new Date(e.date).getTime());
  const lo = Math.min(...ts), hi = Math.max(...ts);
  const total = Math.max(60000, hi - lo);
  const width = total / 48;
  const bins = Array.from({ length: 48 }, (_, i) => ({ i, start: lo + i * width, ERROR: 0, WARN: 0, INFO: 0, DEBUG: 0 }));
  entries.forEach((e, k) => {
    const i = Math.min(47, Math.floor((ts[k] - lo) / width));
    (bins[i] as any)[label(e.level)]++;
  });
  const sel = (b: { start: number }) => !selection || (b.start >= selection[0] && b.start < selection[1]);
  const click = (s: any) => {
    const i = s?.activeTooltipIndex;
    if (i == null) return;
    const r: [number, number] = [lo + i * width, lo + (i + 1) * width];
    onSelect(selection && selection[0] === r[0] ? undefined : r);
  };
  return (
    <div style={{ height: "100%" }} title="點一下長條只看該時段,再點一次取消">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={bins} onClick={click} barCategoryGap={1} margin={{ top: 0, right: 4, left: 0, bottom: 0 }}>
          <CartesianGrid stroke="var(--line)" strokeOpacity={0.5} vertical={false} />
          <XAxis dataKey="start" tickFormatter={(v) => new Date(v).toLocaleTimeString("zh-TW", { hour: "2-digit", minute: "2-digit", hour12: false })}
            tick={{ fontSize: 10, fill: "var(--fg2)" }} interval={7} stroke="var(--line)" />
          <YAxis tick={{ fontSize: 10, fill: "var(--fg2)" }} width={30} tickCount={3} allowDecimals={false} stroke="var(--line)" />
          <Tooltip cursor={{ fill: "color-mix(in srgb, var(--fg) 6%, transparent)" }} content={() => null} />
          <Legend verticalAlign="top" align="right" height={18} iconSize={8} wrapperStyle={{ fontSize: 10 }} />
          {(["error", "warn", "info", "debug"] as LogLevel[]).map((l) => (
            <Bar key={l} dataKey={label(l)} stackId="a" fill={color(LOG_COLOR[l])} isAnimationActive={false}>
              {bins.map((b) => <Cell key={b.i} fillOpacity={sel(b) ? 1 : 0.3} />)}
            </Bar>
          ))}
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

function LogRow({ e, expanded, serviceName, onToggle, onFilter }: { e: LogEntry; expanded: boolean; serviceName: string; onToggle: () => void; onFilter: (t: string) => void }) {
  const lc = color(LOG_COLOR[e.level]);
  const line = `${e.date} ${label(e.level)} [${e.service}@${e.host}] ${e.message}` + (e.traceId ? ` trace=${e.traceId}` : "");
  return (
    <div onClick={onToggle} className="mono" style={{
      padding: "5px 10px", borderBottom: "1px solid color-mix(in srgb, var(--line) 40%, transparent)", fontSize: 12,
      background: expanded ? "color-mix(in srgb, var(--accent) 7%, transparent)" : e.level === "error" ? "color-mix(in srgb, var(--red) 4%, transparent)" : undefined,
    }}>
      <div className="row" style={{ gap: 10, alignItems: "baseline" }}>
        <span style={{ transform: expanded ? "rotate(90deg)" : undefined, display: "inline-flex", width: 10 }}><Icon name="chevron.right" size={9} className="faint" /></span>
        <span className="muted" style={{ flex: "none" }}>{timeMs(e.date)}</span>
        <span style={{ width: 44, flex: "none", textAlign: "center", fontSize: 10, fontWeight: 700, color: lc, background: `color-mix(in srgb, ${lc} 12%, transparent)`, borderRadius: 3, padding: "1px 0" }}>{label(e.level)}</span>
        <span className="ellipsis" style={{ width: 110, flex: "none", color: "var(--teal)" }}>{e.service}</span>
        <span className={expanded ? "grow" : "grow ellipsis"} style={{ color: e.level === "error" ? "var(--red)" : e.level === "debug" ? "var(--fg2)" : undefined, whiteSpace: expanded ? "pre-wrap" : undefined }}>{e.message}</span>
      </div>
      {expanded && (
        <div className="col gap6" style={{ padding: "6px 0 4px 20px" }} onClick={(ev) => ev.stopPropagation()}>
          <div className="bx-grid-table" style={{ gridTemplateColumns: "auto 1fr", rowGap: 4 }}>
            {([["時間", e.date], ["服務", `${e.service}(${serviceName})`], ["主機", e.host], ["Trace ID", e.traceId ?? "—"]] as const).map(([k, v]) => (
              <Fragment key={k}><span className="muted" style={{ fontSize: 11 }}>{k}</span><span style={{ userSelect: "text" }}>{v}</span></Fragment>
            ))}
          </div>
          <div className="row gap8 caption" style={{ fontFamily: "inherit" }}>
            {e.traceId && <button className="btn link caption" onClick={() => onFilter(`trace:${e.traceId}`)}><Icon name="point.3.connected.trianglepath.dotted" size={11} />看同一個 trace</button>}
            <button className="btn link caption" onClick={() => onFilter(`service:${e.service}`)}><Icon name="shippingbox" size={11} />只看這個服務</button>
            <button className="btn link caption" onClick={() => onFilter(`host:${e.host}`)}><Icon name="server.rack" size={11} />只看這台主機</button>
            <button className="btn link caption" onClick={() => copyText(line)}><Icon name="doc.on.doc" size={11} />複製</button>
          </div>
        </div>
      )}
    </div>
  );
}
