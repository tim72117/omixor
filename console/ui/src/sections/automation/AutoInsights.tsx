// 成效:AI 自動化幫團隊省了多少時間、花了多少錢、可不可靠(Swift 的 AutoInsightsView)
import { useState } from "react";
import { Area, Bar, BarChart, CartesianGrid, Cell, ComposedChart, LabelList, Legend, Line, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { IconTile, percent } from "../../components/bits";
import { Icon } from "../../components/Icon";
import type { Automation } from "../../components/models";
import { Card, EmptyState, Segmented } from "../../components/ui";
import { AutonomyBadge, seedUnit, useAuto } from "./autoShared";

type Sort = "hours" | "runs" | "success" | "cost";
const SORTS: { value: Sort; label: string }[] = [{ value: "hours", label: "省下時間" }, { value: "runs", label: "執行次數" }, { value: "success", label: "成功率" }, { value: "cost", label: "成本" }];
const PALETTE = ["var(--indigo)", "var(--purple)", "var(--teal)", "var(--orange)", "var(--pink)", "var(--gray)"];
const axis = { tick: { fontSize: 10, fill: "var(--fg2)" }, stroke: "var(--line)" };

export function AutoInsights({ openAutomation }: { openAutomation: (id: string) => void }) {
  const d = useAuto();
  const [sort, setSort] = useState<Sort>("hours");
  const autos = d.automations;
  if (d.loaded && !autos.length) return <div className="page-inner"><Card><EmptyState title="還沒有成效資料" icon="chart.bar.xaxis" message="啟用 AI 自動化流程後,這裡會統計執行次數、成功率與省下的時間。" /></Card></div>;

  const totalRuns = autos.reduce((s, a) => s + a.runs30d, 0);
  const totalHours = autos.reduce((s, a) => s + a.timeSavedHours, 0);
  const weighted = totalRuns ? autos.reduce((s, a) => s + a.successRate * a.runs30d, 0) / totalRuns : 0;
  const avgCost = (a: Automation) => {
    const mine = d.runs.filter((r) => r.automation === a.id);
    return mine.length ? mine.reduce((s, r) => s + r.costUSD, 0) / mine.length : 0.05 + seedUnit(a.id + "cost") * 0.3;
  };
  const cost30 = (a: Automation) => avgCost(a) * a.runs30d;
  const totalCost = autos.reduce((s, a) => s + cost30(a), 0);
  const today = new Date(); today.setHours(0, 0, 0, 0);
  const days = Array.from({ length: 14 }, (_, i) => { const x = new Date(today); x.setDate(x.getDate() - (13 - i)); return x; });
  const daily = (a: Automation, i: number) => {
    const base = a.runs30d / 30;
    const noise = 0.55 + seedUnit(`${a.id}-${i}`) * 0.9;
    const actual = d.runs.filter((r) => r.automation === a.id && new Date(r.startedAt).toDateString() === days[i].toDateString()).length;
    return Math.max(actual, Math.round(base * noise));
  };
  const top = [...autos].sort((x, y) => y.runs30d - x.runs30d).slice(0, 5);
  const topIds = new Set(top.map((a) => a.id));
  const runsByDay = days.map((dd, i) => {
    const row: Record<string, number | string> = { day: `${dd.getMonth() + 1}/${dd.getDate()}` };
    let other = 0;
    autos.forEach((a) => { const n = daily(a, i); if (topIds.has(a.id)) row[a.name] = n; else other += n; });
    if (other > 0) row["其他"] = other;
    return row;
  });
  const series = [...top.map((a) => a.name), ...(autos.length > 5 ? ["其他"] : [])];
  const costByDay = days.map((dd, i) => ({ day: `${dd.getMonth() + 1}/${dd.getDate()}`, cost: autos.reduce((s, a) => s + daily(a, i) * avgCost(a), 0) }));
  const decided = d.approvals.filter((a) => a.state !== "pending");
  const approvalRate = decided.length ? decided.filter((a) => a.state === "approved").length / decided.length : undefined;
  const hoursData = [...autos].sort((x, y) => y.timeSavedHours - x.timeSavedHours).slice(0, 8);
  const successData = [...autos].sort((x, y) => x.successRate - y.successRate).slice(0, 8).map((a) => ({ name: a.name, v: a.successRate * 100 }));
  const sorted = [...autos].sort((x, y) => sort === "hours" ? y.timeSavedHours - x.timeSavedHours : sort === "runs" ? y.runs30d - x.runs30d : sort === "success" ? y.successRate - x.successRate : cost30(y) - cost30(x));
  const spark = days.map((_, i) => ({ i, v: autos.reduce((s, a) => s + daily(a, i), 0) }));

  return (
    <div className="page-scroll">
      <div className="page-inner">
        <div className="grid" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(170px, 1fr))" }}>
          <Tile title="30 天執行" value={String(totalRuns)} icon="play.circle" tint="indigo" spark={spark} />
          <Tile title="整體成功率" value={percent(weighted * 100)} icon="checkmark.circle" tint="green" />
          <Tile title="省下人力" value={`${totalHours.toFixed(0)} 小時`} delta={`≈ ${(totalHours / 8).toFixed(1)} 人日`} icon="clock.arrow.circlepath" tint="purple" />
          <Tile title="AI 成本(30 天)" value={`$${totalCost.toFixed(2)}`} delta={totalHours > 0 ? `每省 1 小時 $${(totalCost / totalHours).toFixed(2)}` : undefined} icon="dollarsign.circle" tint="orange" />
          <Tile title="人工核准通過率" value={approvalRate == null ? "—" : percent(approvalRate * 100, 0)} delta={decided.length ? `${decided.length} 件已處理` : undefined} icon="hand.raised" tint="purple" />
        </div>
        <div className="grid" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(380px, 1fr))", gap: 16, alignItems: "start" }}>
          <Card title="每日執行次數" subtitle="近 14 天,依流程分色">
            <div style={{ height: 230 }}>
              <ResponsiveContainer>
                <BarChart data={runsByDay} margin={{ top: 4, right: 4, left: -20, bottom: 0 }}>
                  <CartesianGrid stroke="var(--line)" vertical={false} />
                  <XAxis dataKey="day" interval={1} {...axis} />
                  <YAxis allowDecimals={false} {...axis} />
                  <Tooltip contentStyle={{ background: "var(--bg2)", border: "1px solid var(--line)", fontSize: 11 }} />
                  <Legend iconSize={8} wrapperStyle={{ fontSize: 10.5 }} />
                  {series.map((s, i) => <Bar key={s} dataKey={s} stackId="a" fill={PALETTE[i % PALETTE.length]} isAnimationActive={false} />)}
                </BarChart>
              </ResponsiveContainer>
            </div>
          </Card>
          <Card title="省下的時間" subtitle="30 天,小時">
            <div style={{ height: Math.max(150, hoursData.length * 28) }}>
              <ResponsiveContainer>
                <BarChart data={hoursData} layout="vertical" margin={{ top: 0, right: 30, left: 0, bottom: 0 }}>
                  <defs><linearGradient id="aiGrad" x1="0" y1="0" x2="1" y2="0"><stop offset="0%" stopColor="var(--purple)" /><stop offset="100%" stopColor="var(--indigo)" /></linearGradient></defs>
                  <CartesianGrid stroke="var(--line)" horizontal={false} />
                  <XAxis type="number" {...axis} />
                  <YAxis type="category" dataKey="name" width={120} {...axis} />
                  <Bar dataKey="timeSavedHours" fill="url(#aiGrad)" radius={[0, 4, 4, 0]} isAnimationActive={false}>
                    <LabelList dataKey="timeSavedHours" position="right" formatter={(v: number) => v.toFixed(1)} style={{ fontSize: 10, fill: "var(--fg2)" }} />
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </div>
          </Card>
          <Card title="成功率" subtitle="低於 90% 的流程建議改回「需核准」">
            <div style={{ height: Math.max(150, successData.length * 28) }}>
              <ResponsiveContainer>
                <BarChart data={successData} layout="vertical" margin={{ top: 0, right: 30, left: 0, bottom: 0 }}>
                  <CartesianGrid stroke="var(--line)" horizontal={false} />
                  <XAxis type="number" domain={[0, 105]} ticks={[0, 25, 50, 75, 100]} {...axis} />
                  <YAxis type="category" dataKey="name" width={120} {...axis} />
                  <ReferenceLine x={90} stroke="var(--fg2)" strokeDasharray="4 3" />
                  <Bar dataKey="v" radius={[0, 4, 4, 0]} isAnimationActive={false}>
                    {successData.map((x) => <Cell key={x.name} fill={x.v >= 95 ? "var(--green)" : x.v >= 90 ? "var(--orange)" : "var(--red)"} />)}
                    <LabelList dataKey="v" position="right" formatter={(v: number) => `${v.toFixed(0)}%`} style={{ fontSize: 10, fill: "var(--fg2)" }} />
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </div>
          </Card>
          <Card title="每日 AI 成本" subtitle="美元,依 tokens 用量估算">
            <div style={{ height: 230 }}>
              <ResponsiveContainer>
                <ComposedChart data={costByDay} margin={{ top: 4, right: 4, left: -10, bottom: 0 }}>
                  <CartesianGrid stroke="var(--line)" vertical={false} />
                  <XAxis dataKey="day" interval={1} {...axis} />
                  <YAxis tickFormatter={(v) => `$${(+v).toFixed(1)}`} {...axis} />
                  <Area type="monotone" dataKey="cost" stroke="none" fill="var(--purple)" fillOpacity={0.15} isAnimationActive={false} />
                  <Line type="monotone" dataKey="cost" stroke="var(--purple)" dot={{ r: 2, fill: "var(--purple)" }} isAnimationActive={false} />
                </ComposedChart>
              </ResponsiveContainer>
            </div>
          </Card>
        </div>
        <Card>
          <div className="row"><span className="h3 grow" style={{ fontSize: 14 }}>流程排行</span><div style={{ width: 320 }}><Segmented value={sort} onChange={setSort} options={SORTS} /></div></div>
          <table className="table">
            <thead><tr><th>流程</th><th>自主程度</th><th style={{ textAlign: "right" }}>執行</th><th style={{ textAlign: "right" }}>成功率</th><th style={{ textAlign: "right" }}>省下</th><th style={{ textAlign: "right" }}>成本</th></tr></thead>
            <tbody>
              {sorted.map((a) => (
                <tr key={a.id} className="click" onClick={() => openAutomation(a.id)}>
                  <td><span className="row gap8"><IconTile icon={a.icon} size={22} dimmed={!a.enabled} />{a.name}{!a.enabled && <span className="caption muted">已停用</span>}</span></td>
                  <td><AutonomyBadge autonomy={a.autonomy} /></td>
                  <td className="num" style={{ textAlign: "right" }}>{a.runs30d}</td>
                  <td className="num" style={{ textAlign: "right", color: a.successRate >= 0.95 ? undefined : a.successRate >= 0.9 ? "var(--orange)" : "var(--red)" }}>{percent(a.successRate * 100)}</td>
                  <td className="num" style={{ textAlign: "right" }}>{a.timeSavedHours.toFixed(1)} h</td>
                  <td className="num" style={{ textAlign: "right" }}>${cost30(a).toFixed(2)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      </div>
    </div>
  );
}

function Tile({ title, value, delta, icon, tint, spark }: { title: string; value: string; delta?: string; icon: string; tint: string; spark?: { i: number; v: number }[] }) {
  const c = `var(--${tint})`;
  return (
    <div className="card metric" style={{ gap: 6, borderRadius: 12 }}>
      <div className="caption muted row gap4"><IconInline name={icon} c={c} />{title}</div>
      <div className="row gap8" style={{ alignItems: "baseline" }}>
        <span className="metric-value" style={{ fontSize: 24 }}>{value}</span>
        {delta && <span className="caption" style={{ color: "var(--green)", fontWeight: 500 }}>{delta}</span>}
      </div>
      {spark && (
        <div style={{ height: 28 }}>
          <ResponsiveContainer>
            <ComposedChart data={spark} margin={{ top: 2, right: 0, left: 0, bottom: 0 }}>
              <YAxis hide domain={["dataMin", "dataMax"]} />
              <Area type="monotone" dataKey="v" stroke={c} fill={c} fillOpacity={0.15} strokeWidth={1.5} isAnimationActive={false} />
            </ComposedChart>
          </ResponsiveContainer>
        </div>
      )}
    </div>
  );
}

const IconInline = ({ name, c }: { name: string; c: string }) => <Icon name={name} size={12} color={c} />;
