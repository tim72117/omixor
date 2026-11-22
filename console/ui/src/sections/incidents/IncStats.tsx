// 事件統計:MTTA / MTTR、近 30 天各 SEV 事件數、每週趨勢、每起事件的修復時間(Swift 的 IncStatsView)
import { Bar, BarChart, CartesianGrid, Cell, LabelList, Legend, ResponsiveContainer, XAxis, YAxis } from "recharts";
import { Card } from "../../components/Card";
import { MetricTile } from "../../components/MetricTile";
import { color } from "../../lib/color";
import css from "./incidents.module.css";
import { SEVS, SevBadge, ackDelay, durationOf, fmtShort, isActive, sevColor, sevLabel, useInc } from "./incSupport";
import { rowButtonClass } from "../../components/RowButton";
import { tOrNow } from "../../lib/format";

const avg = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : undefined);

export function IncStats({ onSelect }: { onSelect: (id: string) => void }) {
  const { incidents } = useInc();
  const since = Date.now() - 30 * 86400000;
  const recent = incidents.filter((i) => tOrNow(i.startedAt) >= since);
  const mtta = avg(recent.map(ackDelay).filter((x): x is number => x != null));
  const mttr = avg(recent.filter((i) => i.resolvedAt).map((i) => durationOf(i)));
  const bySev = SEVS.map((s) => ({ sev: sevLabel(s), n: recent.filter((i) => i.sev === s).length, c: color(sevColor(s)) }));
  const today = new Date(); today.setHours(0, 0, 0, 0);
  const weekly = Array.from({ length: 5 }, (_, w) => {
    const start = new Date(today); start.setDate(start.getDate() - 7 * (4 - w) - 7);
    const end = new Date(start); end.setDate(end.getDate() + 7);
    const row: Record<string, number | string> = { week: `${end.getMonth() + 1}/${end.getDate()}` };
    SEVS.forEach((s) => (row[sevLabel(s)] = incidents.filter((i) => i.sev === s && tOrNow(i.startedAt) >= start.getTime() && tOrNow(i.startedAt) < end.getTime()).length));
    return row;
  });
  const resolved = recent.filter((i) => i.resolvedAt).sort((a, b) => b.startedAt.localeCompare(a.startedAt));
  const maxD = Math.max(1, ...resolved.map((i) => durationOf(i)));
  const axis = { tick: { fontSize: 11, fill: "var(--fg2)" }, stroke: "var(--line)" };
  return (
    <div className="page-scroll">
      <div className="page-inner">
        <div className="grid g4">
          <MetricTile title="進行中" value={incidents.filter(isActive).length} icon="flame" tint="red" />
          <MetricTile title="近 30 天事件" value={recent.length} icon="calendar" tint="blue" />
          <MetricTile title="MTTA 平均回應" value={fmtShort(mtta)} icon="hand.raised" tint="orange" />
          <MetricTile title="MTTR 平均修復" value={fmtShort(mttr)} icon="wrench.and.screwdriver" tint="green" />
        </div>
        <div className={"grid g2 " + css.g2Wide}>
          <Card title="各 SEV 事件數" subtitle="近 30 天">
            {recent.length === 0 ? <div className={"muted " + css.chart}>近 30 天沒有事件</div> : (
              <div className={css.chart}>
                <ResponsiveContainer>
                  <BarChart data={bySev} margin={{ top: 16, right: 4, left: -20, bottom: 0 }}>
                    <CartesianGrid stroke="var(--line)" vertical={false} />
                    <XAxis dataKey="sev" {...axis} />
                    <YAxis allowDecimals={false} tickCount={4} {...axis} />
                    <Bar dataKey="n" radius={[4, 4, 0, 0]} isAnimationActive={false}>
                      {bySev.map((b) => <Cell key={b.sev} fill={b.c} />)}
                      <LabelList dataKey="n" position="top" style={{ fontSize: "var(--fs-xs)", fill: "var(--fg2)" }} />
                    </Bar>
                  </BarChart>
                </ResponsiveContainer>
              </div>
            )}
          </Card>
          <Card title="每週趨勢" subtitle="依 SEV 堆疊">
            <div className={css.chart}>
              <ResponsiveContainer>
                <BarChart data={weekly} margin={{ top: 4, right: 4, left: -20, bottom: 0 }}>
                  <CartesianGrid stroke="var(--line)" vertical={false} />
                  <XAxis dataKey="week" {...axis} />
                  <YAxis allowDecimals={false} tickCount={4} {...axis} />
                  <Legend iconSize={8} wrapperStyle={{ fontSize: "var(--fs-xs)" }} />
                  {SEVS.map((s) => <Bar key={s} dataKey={sevLabel(s)} stackId="a" fill={color(sevColor(s))} isAnimationActive={false} />)}
                </BarChart>
              </ResponsiveContainer>
            </div>
          </Card>
        </div>
        <Card title="每起事件的修復時間" subtitle="近 30 天已解決事件,點選查看">
          {resolved.length === 0 ? <span className="muted">近 30 天沒有已解決的事件</span> : (
            <div className="col gap4">
              {resolved.map((i) => (
                <button key={i.id} className={rowButtonClass} onClick={() => onSelect(i.id)}>
                  <SevBadge sev={i.sev} />
                  <span className={"ellipsis " + css.w260}>{i.title}</span>
                  <span className="grow">
                    <span className={css.durBar} style={{ width: `${Math.max(1, (durationOf(i) / maxD) * 100)}%`, background: `color-mix(in srgb, ${color(sevColor(i.sev))} 70%, transparent)` }} />
                  </span>
                  <span className={"caption num muted " + css.durText}>{fmtShort(durationOf(i))}</span>
                </button>
              ))}
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
