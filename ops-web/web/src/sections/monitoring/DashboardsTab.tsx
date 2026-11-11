// 儀表板分頁:選一個儀表板,畫出所有面板;依全域時間範圍裁切,滑鼠移上去看數值(Swift 的 MonDashboardsTab)
import { useState } from "react";
import { Area, CartesianGrid, ComposedChart, Line, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { useView } from "../../api";
import { num } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { arr, type Dashboard, type MetricPoint, type MetricSeries } from "../../components/models";
import { EmptyState, Segmented } from "../../components/ui";
import { useTimeRange } from "./MonitoringView";

type Panel = { series: MetricSeries; points: { t: number; value: number }[]; usedFallback: boolean };

function panelData(series: MetricSeries, windowStart: number): Panel {
  const all = (series.points ?? []).map((p: MetricPoint) => ({ t: new Date(p.date).getTime(), value: p.value }));
  const inWin = all.filter((p) => p.t >= windowStart);
  if (inWin.length >= 2 || all.length === 0) return { series, points: inWin, usedFallback: false };
  return { series, points: all, usedFallback: true };
}

function stats(p: Panel) {
  const v = p.points.map((x) => x.value);
  if (!v.length) return { latest: undefined, avg: undefined, max: undefined, min: undefined };
  return { latest: v[v.length - 1], avg: v.reduce((a, b) => a + b, 0) / v.length, max: Math.max(...v), min: Math.min(...v) };
}

/** 依名稱與單位推測合理的門檻(只在資料接近門檻時顯示) */
function threshold(p: Panel): number | undefined {
  const name = (p.series.name + " " + p.series.id).toLowerCase();
  const unit = p.series.unit.toLowerCase();
  let t: number | undefined;
  if (unit === "ms") t = name.includes("p99") ? 1500 : 800;
  else if (unit === "%") {
    if (["error", "錯誤", "5xx"].some((k) => name.includes(k))) t = 1;
    else if (["cpu", "記憶體", "memory", "mem", "disk", "磁碟"].some((k) => name.includes(k))) t = 85;
    else if (["uptime", "可用", "成功"].some((k) => name.includes(k))) t = 99.9;
  }
  const { max, min } = stats(p);
  if (t == null || max == null || min == null) return undefined;
  return max >= t * 0.5 && min <= t * 1.5 ? t : undefined;
}

function fmtValue(unit: string, v: number): string {
  let s: string;
  if (Math.abs(v) >= 1000) s = num(v);
  else if (Math.abs(v) >= 100) s = v.toFixed(0);
  else if (Math.abs(v) >= 10) s = v.toFixed(1);
  else s = v.toFixed(2);
  if (!unit) return s;
  return unit === "%" ? s + "%" : `${s} ${unit}`;
}

const axisLabel = (v: number) => (Math.abs(v) >= 1000 ? num(v) : v === Math.round(v) ? v.toFixed(0) : v.toFixed(1));

export function DashboardsTab({ selectedId, setSelectedId }: { selectedId?: string; setSelectedId: (id: string) => void }) {
  const [raw, err] = useView<Dashboard[]>("dashboards");
  const tr = useTimeRange();
  const dashboards = arr<Dashboard>(raw);
  if (err) return <EmptyState title="讀不到儀表板" icon="exclamationmark.triangle" message={err} />;
  if (raw === undefined) return <div className="muted" style={{ padding: 20 }}>載入中…</div>;
  if (!dashboards.length) return <EmptyState title="沒有儀表板" icon="chart.xyaxis.line" message="尚未設定任何監控儀表板。" />;
  const d = dashboards.find((x) => x.id === selectedId) ?? dashboards[0];
  const windowStart = Date.now() - tr.seconds * 1000;
  const panels = (d.panels ?? []).map((p) => panelData(p, windowStart));
  return (
    <div className="page-scroll">
      <div className="page-inner">
        <div className="row gap8">
          {dashboards.length <= 5 ? (
            <Segmented value={d.id} onChange={setSelectedId} options={dashboards.map((x) => ({ value: x.id, label: x.name }))} />
          ) : (
            <select className="select" value={d.id} onChange={(e) => setSelectedId(e.target.value)} style={{ maxWidth: 260 }}>
              {dashboards.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
            </select>
          )}
          <div className="grow" />
          <span className="row gap4 muted"><Icon name="clock" size={12} />最近 {tr.label}</span>
        </div>
        {panels.length === 0 ? <EmptyState title="這個儀表板沒有面板" icon="square.dashed" /> : (
          <>
            <PanelCard panel={panels[0]} height={240} prominent />
            {panels.length > 1 && (
              <div className="grid" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(340px, 1fr))", gap: 16 }}>
                {panels.slice(1).map((p) => <PanelCard key={p.series.id} panel={p} height={150} />)}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function PanelCard({ panel, height, prominent }: { panel: Panel; height: number; prominent?: boolean }) {
  const [hover, setHover] = useState<{ t: number; value: number }>();
  const st = stats(panel);
  const t = threshold(panel);
  const over = t != null && st.latest != null && st.latest >= t;
  const col = over ? "var(--red)" : prominent ? "var(--accent)" : "var(--blue)";
  const span = panel.points.length ? panel.points[panel.points.length - 1].t - panel.points[0].t : 0;
  const xFmt = (v: number) => {
    const dt = new Date(v);
    return span > 2 * 86400000 ? `${dt.getMonth() + 1}/${dt.getDate()}` : dt.toLocaleTimeString("zh-TW", { hour: "2-digit", minute: "2-digit", hour12: false });
  };
  const includeZero = panel.series.unit === "%" || (st.min ?? 0) < (st.max ?? 0) * 0.3;
  const gid = "g-" + panel.series.id.replace(/[^a-zA-Z0-9]/g, "");
  return (
    <div className="card" style={{ borderRadius: 12 }}>
      <div className="row" style={{ alignItems: "flex-start" }}>
        <div className="col gap4 grow">
          <div style={{ fontWeight: 600, fontSize: prominent ? 14 : 13 }}>{panel.series.name}</div>
          {panel.usedFallback && <div className="caption" style={{ color: "var(--orange)", fontSize: 10.5 }}>選取的時間範圍內沒有資料,顯示全部</div>}
        </div>
        {hover ? (
          <div className="col" style={{ alignItems: "flex-end" }}>
            <span className="num" style={{ fontWeight: 600, fontSize: prominent ? 17 : 13.5 }}>{fmtValue(panel.series.unit, hover.value)}</span>
            <span className="caption muted" style={{ fontSize: 10.5 }}>{new Date(hover.t).toLocaleString("zh-TW", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false })}</span>
          </div>
        ) : st.latest != null ? (
          <div className="col" style={{ alignItems: "flex-end" }}>
            <span className="num" style={{ fontWeight: 600, fontSize: prominent ? 17 : 13.5, color: over ? "var(--red)" : undefined }}>{fmtValue(panel.series.unit, st.latest)}</span>
            <span className="caption muted" style={{ fontSize: 10.5 }}>目前</span>
          </div>
        ) : null}
      </div>
      {panel.points.length === 0 ? (
        <div className="muted row" style={{ height, justifyContent: "center" }}>沒有資料</div>
      ) : (
        <div style={{ height }}>
          <ResponsiveContainer width="100%" height="100%">
            <ComposedChart data={panel.points} margin={{ top: 14, right: 6, bottom: 0, left: 0 }}
              onMouseMove={(s: any) => { const p = s?.activePayload?.[0]?.payload; if (p) setHover(p); }}
              onMouseLeave={() => setHover(undefined)}>
              <defs>
                <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={col} stopOpacity={0.22} />
                  <stop offset="100%" stopColor={col} stopOpacity={0.02} />
                </linearGradient>
              </defs>
              <CartesianGrid stroke="var(--line)" strokeOpacity={0.6} vertical={false} />
              <XAxis dataKey="t" type="number" scale="time" domain={["dataMin", "dataMax"]} tickFormatter={xFmt} tick={{ fontSize: 10, fill: "var(--fg2)" }}
                tickCount={prominent ? 8 : 4} stroke="var(--line)" />
              <YAxis tickFormatter={axisLabel} tick={{ fontSize: 10, fill: "var(--fg2)" }} width={44} tickCount={prominent ? 5 : 3}
                domain={includeZero ? [0, "auto"] : ["auto", "auto"]} stroke="var(--line)" />
              <Tooltip content={() => null} cursor={{ stroke: "var(--fg3)", strokeWidth: 1 }} />
              <Area type="monotone" dataKey="value" stroke="none" fill={`url(#${gid})`} isAnimationActive={false} />
              <Line type="monotone" dataKey="value" stroke={col} strokeWidth={prominent ? 2 : 1.5} dot={false}
                activeDot={{ r: prominent ? 4 : 3, fill: col }} isAnimationActive={false} />
              {t != null && (
                <ReferenceLine y={t} stroke="var(--red)" strokeOpacity={0.7} strokeDasharray="4 3"
                  label={{ value: `門檻 ${fmtValue(panel.series.unit, t)}`, position: "insideTopLeft", fill: "var(--red)", fontSize: 10 }} />
              )}
            </ComposedChart>
          </ResponsiveContainer>
        </div>
      )}
      {(prominent || panel.points.length > 0) && (
        <div className="row" style={{ gap: 18 }}>
          {([["平均", st.avg], ["最低", st.min], ["最高", st.max]] as const).map(([k, v]) => (
            <div key={k} className="col">
              <span className="muted" style={{ fontSize: 10.5 }}>{k}</span>
              <span className="caption num" style={{ fontWeight: 500 }}>{v == null ? "—" : fmtValue(panel.series.unit, v)}</span>
            </div>
          ))}
          {t != null && (() => {
            const n = panel.points.filter((p) => p.value >= t).length;
            return (
              <div className="col">
                <span className="muted" style={{ fontSize: 10.5 }}>超過門檻</span>
                <span className="caption num" style={{ fontWeight: 500, color: n > 0 ? "var(--red)" : undefined }}>
                  {panel.points.length ? `${((n / panel.points.length) * 100).toFixed(0)}%` : "—"}
                </span>
              </div>
            );
          })()}
        </div>
      )}
    </div>
  );
}
