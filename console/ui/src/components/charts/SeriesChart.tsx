// 時間序列折線圖(recharts)
import { CartesianGrid, Legend, Line, LineChart, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { hhmm, monthDayTime, t } from "../../lib/format";
import type { MetricPoint } from "../../models";
import { axisStyle, SERIES_COLORS } from "./theme";

/** 時間序列折線圖(SeriesChart) */
export function SeriesChart({ series, height = 180, threshold, yDomain, yFormat, colors }: {
  series: { name: string; points: MetricPoint[] }[]; height?: number; threshold?: number; yDomain?: [number, number]; yFormat?: (v: number) => string; colors?: string[];
}) {
  const rows = new Map<number, any>();
  series.forEach((s) => s.points.forEach((p) => {
    const k = t(p.date);
    const r = rows.get(k) ?? { x: k };
    r[s.name] = p.value;
    rows.set(k, r);
  }));
  const data = [...rows.values()].sort((a, b) => a.x - b.x);
  const span = data.length > 1 ? data[data.length - 1].x - data[0].x : 0;
  const fmtX = (v: number) => (span > 2 * 86400000 ? `${new Date(v).getMonth() + 1}/${new Date(v).getDate()}` : hhmm(v));
  return (
    <div style={{ height, width: "100%" }}>
      <ResponsiveContainer>
        <LineChart data={data} margin={{ top: 14, right: 8, bottom: 0, left: -12 }}>
          <CartesianGrid stroke="var(--line)" vertical={false} />
          <XAxis dataKey="x" type="number" scale="time" domain={["dataMin", "dataMax"]} tickFormatter={fmtX} tick={axisStyle} stroke="var(--line)" minTickGap={30} />
          <YAxis tick={axisStyle} stroke="var(--line)" domain={yDomain ?? ["auto", "auto"]} tickFormatter={yFormat} width={48} />
          <Tooltip labelFormatter={(v) => monthDayTime(Number(v))} formatter={(v: any) => (typeof v === "number" ? (yFormat ? yFormat(v) : v.toFixed(2)) : v)} contentStyle={{ fontSize: "var(--fs-sm)", background: "var(--bg2)", border: "1px solid var(--line)" }} />
          {series.length > 1 && <Legend wrapperStyle={{ fontSize: "var(--fs-xs)" }} iconSize={8} />}
          {series.map((s, i) => <Line key={s.name} dataKey={s.name} type="monotone" dot={false} strokeWidth={1.6} stroke={colors?.[i] ?? SERIES_COLORS[i % SERIES_COLORS.length]} isAnimationActive={false} />)}
          {threshold != null && <ReferenceLine y={threshold} stroke="var(--red)" strokeOpacity={0.6} strokeDasharray="4 3" label={{ value: "門檻", position: "insideTopRight", fontSize: 11, fill: "var(--red)" }} />}
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}
