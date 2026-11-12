// CI/CD › 環境:DORA 指標、服務 × 環境版本矩陣、每日執行(Swift 的 PipeEnvironmentsTab.swift)
import { Area, Bar as RBar, BarChart, CartesianGrid, ComposedChart, Legend, Line, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { action } from "../../api";
import { Card, color, EmptyState } from "../../components/ui";
import {
  axisStyle, Check, dur, ENV, HealthBadge, Metric, monthDay, now, pct, RUN, SIcon, startOfDay, t, useAct, useConfirm, useList, useOps,
  type Deployment, type EnvKey, type Incident, type PipelineRun, type Service,
} from "../../components/opsKit";
import { useState } from "react";
import { depIsActive, depIsRollback, StatusIcon } from "./pipeShared";

const ENV_ORDER: EnvKey[] = ["development", "staging", "production"];
const previous = (e: EnvKey): EnvKey | undefined => (e === "staging" ? "development" : e === "production" ? "staging" : undefined);

type Rating = { label: string; good: boolean };
const R = { elite: { label: "菁英", good: true }, high: { label: "高", good: true }, medium: { label: "中", good: false }, low: { label: "低", good: false } };
const lead = (s: number): Rating => (s < 86400 ? R.elite : s < 7 * 86400 ? R.high : s < 30 * 86400 ? R.medium : R.low);
const cfr = (r: number): Rating => (r <= 0.05 ? R.elite : r <= 0.15 ? R.high : r <= 0.3 ? R.medium : R.low);
const mttrR = (s: number): Rating => (s < 3600 ? R.elite : s < 86400 ? R.high : s < 7 * 86400 ? R.medium : R.low);
const avg = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : 0);

export function PipeEnvironmentsTab({ openDeployments }: { openDeployments: (id: string) => void }) {
  const o = useOps();
  const [deps] = useList<Deployment>("deployments");
  const [runs] = useList<PipelineRun>("pipeline_runs");
  const [incidents] = useList<Incident>("incidents");
  const [onlyDrift, setOnlyDrift] = useState(false);
  const [dlg, ask] = useConfirm();
  const { run, toastEl } = useAct();
  const env = (o.nav?.env ?? "production") as EnvKey;

  // DORA
  const weekAgo = now() - 7 * 86400000;
  const prod = deps.filter((d) => d.env === "production");
  const prodOK = prod.filter((d) => d.status === "succeeded" && t(d.startedAt) > weekAgo);
  const perDay = prodOK.length / 7;
  const today = startOfDay(now());
  const daily = [6, 5, 4, 3, 2, 1, 0].map((b) => prodOK.filter((d) => startOfDay(t(d.startedAt)) === today - b * 86400000).length);
  const ci = runs.filter((r) => r.status === "succeeded" && r.duration != null).map((r) => r.duration!);
  const dd = prod.filter((d) => d.status === "succeeded" && d.finishedAt).map((d) => (t(d.finishedAt) - t(d.startedAt)) / 1000);
  const leadTime = ci.length || dd.length ? avg(ci) + avg(dd) : undefined;
  const finished = prod.filter((d) => d.status === "succeeded" || d.status === "failed");
  const bad = finished.filter((d) => d.status === "failed").length + prod.filter(depIsRollback).length;
  const failureRate = finished.length ? Math.min(1, bad / finished.length) : undefined;
  const resolved = incidents.filter((i) => i.resolvedAt).map((i) => (t(i.resolvedAt) - t(i.startedAt)) / 1000);
  const mttr = resolved.length ? avg(resolved) : undefined;
  const freqRating = perDay >= 1 ? R.elite : prodOK.length >= 1 ? R.high : R.medium;

  // 版本矩陣
  const isBehind = (s: Service, e: EnvKey) => {
    const p = previous(e);
    const pv = p ? s.versions?.[p] : undefined;
    const v = s.versions?.[e];
    return e === "production" && !!pv && !!v && pv !== v;
  };
  const drift = (s: Service) => isBehind(s, "production");
  const active = (s: Service, e: EnvKey) => deps.find((d) => d.service === s.id && d.env === e && depIsActive(d));
  const canPromote = (s: Service, e: EnvKey) => {
    const p = previous(e);
    const pv = p ? s.versions?.[p] : undefined;
    return !!pv && pv !== s.versions?.[e] && !active(s, e);
  };
  const promote = (s: Service, to: EnvKey) => {
    const from: EnvKey = to === "production" ? "staging" : "development";
    const fromV = s.versions?.[from] ?? "—", toV = s.versions?.[to] ?? "—";
    ask({
      title: `把 ${s.name} 推進到 ${ENV[to].label}?`,
      message: to === "production" ? `${toV} → ${fromV},以 Canary(10% 流量)開始。建立後需要核准才會真正部署。` : `${toV} → ${fromV},以 Rolling 方式部署到 Staging。`,
      buttons: [{
        label: to === "production" ? "建立部署(需核准)" : "開始部署", primary: true,
        onClick: async () => {
          const r: any = await run(() => action("deployment_promote", { service: s.id, to }));
          if (to === "production" && r?.id) openDeployments(r.id);
        },
      }],
    });
  };
  const services = o.services.filter((s) => !onlyDrift || drift(s)).sort((a, b) => a.tier - b.tier || a.name.localeCompare(b.name));
  const driftCount = o.services.filter(drift).length;

  // 圖表
  const days = Array.from({ length: 14 }, (_, i) => today - (13 - i) * 86400000);
  const cat = (s: string) => (s === "succeeded" ? "成功" : s === "failed" ? "失敗" : "其他");
  const buckets = days.map((d) => {
    const todays = runs.filter((r) => startOfDay(t(r.startedAt)) === d);
    return { x: d, 成功: todays.filter((r) => cat(r.status) === "成功").length, 失敗: todays.filter((r) => cat(r.status) === "失敗").length, 其他: todays.filter((r) => cat(r.status) === "其他").length };
  });
  const total = buckets.reduce((a, b) => a + b.成功 + b.失敗 + b.其他, 0);
  const rates = days.map((d) => {
    const done = runs.filter((r) => startOfDay(t(r.startedAt)) === d && (r.status === "succeeded" || r.status === "failed"));
    return done.length ? { x: d, rate: (done.filter((r) => r.status === "succeeded").length / done.length) * 100 } : null;
  }).filter((x): x is { x: number; rate: number } => !!x);
  const fin = runs.filter((r) => r.status === "succeeded" || r.status === "failed");
  const overall = fin.length ? fin.filter((r) => r.status === "succeeded").length / fin.length : undefined;
  const tickDay = (v: number) => `${new Date(v).getMonth() + 1}/${new Date(v).getDate()}`;
  const tip = { contentStyle: { fontSize: 12, background: "var(--bg2)", border: "1px solid var(--line)" }, labelFormatter: (v: any) => monthDay(Number(v)) };

  return (
    <div className="page-scroll">
      <div className="page-inner">
        <div className="col gap8">
          <div className="row gap8" style={{ alignItems: "baseline" }}><span className="card-title">DORA 指標</span><span className="caption muted">Production · 最近 7 天</span></div>
          <div className="ops-grid-auto" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(180px, 1fr))" }}>
            <Metric title="部署頻率" value={perDay >= 1 ? `${perDay.toFixed(1)} 次/天` : `${prodOK.length} 次/週`} delta={freqRating.label} deltaGood={freqRating.good} icon="shippingbox" tint="blue" series={daily} />
            <Metric title="變更前置時間" value={dur(leadTime)} delta={leadTime != null ? lead(leadTime).label : null} deltaGood={leadTime != null ? lead(leadTime).good : true} icon="hourglass" tint="teal" />
            <Metric title="變更失敗率" value={failureRate != null ? pct(failureRate * 100, 0) : "—"} delta={failureRate != null ? cfr(failureRate).label : null} deltaGood={failureRate != null ? cfr(failureRate).good : true} icon="exclamationmark.triangle" tint="orange" />
            <Metric title="平均恢復時間" value={dur(mttr)} delta={mttr != null ? mttrR(mttr).label : null} deltaGood={mttr != null ? mttrR(mttr).good : true} icon="cross.case" tint="pink" />
          </div>
        </div>
        <Card>
          <div className="row" style={{ alignItems: "baseline" }}>
            <div className="col" style={{ gap: 2 }}>
              <span className="card-title">版本矩陣</span>
              <span className="caption" style={{ color: driftCount === 0 ? "var(--fg2)" : "var(--orange)" }}>{driftCount === 0 ? "所有服務的 Staging 與 Production 版本一致" : `${driftCount} 個服務的 Production 落後 Staging`}</span>
            </div>
            <span className="grow" />
            <Check on={onlyDrift} onChange={setOnlyDrift}><span className="caption">只看版本不一致</span></Check>
          </div>
          {o.services.length === 0 ? <div style={{ minHeight: 160, display: "flex" }}><EmptyState title="沒有服務" icon="square.stack.3d.up.slash" /></div>
            : services.length === 0 ? <div className="muted" style={{ padding: "8px 0" }}>沒有版本不一致的服務</div> : (
              <div style={{ overflowX: "auto" }}>
                <table className="ops-table" style={{ fontSize: 12.5 }}>
                  <thead><tr>
                    <th style={{ background: "transparent" }}>服務</th>
                    {ENV_ORDER.map((e) => <th key={e} style={{ background: "transparent", fontWeight: e === env ? 700 : 400, color: e === env ? "var(--fg)" : undefined }}><span className="row gap4"><span className="dot" style={{ width: 6, height: 6, background: color(ENV[e].color) }} />{ENV[e].label}</span></th>)}
                    <th style={{ background: "transparent" }} />
                  </tr></thead>
                  <tbody>
                    {services.map((s) => (
                      <tr key={s.id}>
                        <td style={{ minWidth: 130 }}><span className="row gap6"><HealthBadge h={s.health} compact /><span className="col" style={{ gap: 1 }}><span style={{ fontWeight: 500 }}>{s.name}</span><span className="muted" style={{ fontSize: 10.5 }}>Tier {s.tier} · {s.team}</span></span></span></td>
                        {ENV_ORDER.map((e) => {
                          const v = s.versions?.[e];
                          const behind = isBehind(s, e);
                          const d = active(s, e);
                          return (
                            <td key={e} style={{ minWidth: 110 }}>
                              <div className="col" style={{ gap: 3, padding: "5px 8px", borderRadius: 6, background: behind ? "color-mix(in srgb, var(--orange) 10%, transparent)" : e === env ? "color-mix(in srgb, var(--accent) 6%, transparent)" : undefined }}>
                                <span className="row gap4 mono" style={{ color: !v ? "var(--fg2)" : behind ? "var(--orange)" : undefined }}>
                                  {v ?? "—"}{behind && <span title={`落後上一個環境(${s.versions?.[previous(e)!] ?? "—"})`}><SIcon name="arrow.down.circle.fill" size={12} color="orange" /></span>}
                                </span>
                                {d && (
                                  <button className="ops-plain row gap4 num" style={{ fontSize: 10.5, color: color(RUN[d.status].color) }} title="查看部署" onClick={() => openDeployments(d.id)}>
                                    <StatusIcon s={d.status} size={10} />{d.status === "waitingApproval" ? `${d.version} 待核准` : `→ ${d.version} ${Math.trunc(d.progress * 100)}%`}
                                  </button>
                                )}
                              </div>
                            </td>
                          );
                        })}
                        <td style={{ textAlign: "right" }}>
                          <span className="row gap6" style={{ justifyContent: "flex-end" }}>
                            {canPromote(s, "staging") && <button className="btn small" title={`把 ${s.versions?.development ?? ""} 部署到 Staging`} onClick={() => promote(s, "staging")}><SIcon name="arrow.up.forward" size={11} />推進到 stg</button>}
                            {canPromote(s, "production") && <button className="btn small primary" title={`把 ${s.versions?.staging ?? ""} 推進到 Production(需核准)`} onClick={() => promote(s, "production")}><SIcon name="arrow.up.forward.circle.fill" size={11} />推進到 prod</button>}
                            {!canPromote(s, "staging") && !canPromote(s, "production") && <span className="caption muted row gap4"><SIcon name="checkmark" size={11} />一致</span>}
                          </span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
        </Card>
        <div className="ops-grid-auto" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(360px, 1fr))", gap: 16 }}>
          <Card title="每日執行次數" subtitle={`最近 14 天 · 共 ${total} 次`}>
            {total === 0 ? <div className="muted" style={{ height: 180 }}>最近 14 天沒有執行紀錄</div> : (
              <div style={{ height: 200 }}>
                <ResponsiveContainer>
                  <BarChart data={buckets} margin={{ top: 6, right: 6, left: -22, bottom: 0 }}>
                    <CartesianGrid stroke="var(--line)" vertical={false} />
                    <XAxis dataKey="x" tick={axisStyle} stroke="var(--line)" tickFormatter={tickDay} interval={1} />
                    <YAxis tick={axisStyle} stroke="var(--line)" allowDecimals={false} />
                    <Tooltip {...tip} cursor={{ fill: "color-mix(in srgb, var(--fg) 5%, transparent)" }} />
                    <Legend wrapperStyle={{ fontSize: 11 }} iconSize={8} />
                    <RBar dataKey="成功" stackId="a" fill="var(--green)" isAnimationActive={false} />
                    <RBar dataKey="失敗" stackId="a" fill="var(--red)" isAnimationActive={false} />
                    <RBar dataKey="其他" stackId="a" fill="color-mix(in srgb, var(--gray) 50%, transparent)" isAnimationActive={false} />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            )}
          </Card>
          <Card title="成功率" subtitle={overall != null ? `整體 ${pct(overall * 100, 0)},不含取消與執行中` : "還沒有完成的執行"}>
            {rates.length === 0 ? <div className="muted" style={{ height: 180 }}>沒有資料</div> : (
              <div style={{ height: 200 }}>
                <ResponsiveContainer>
                  <ComposedChart data={rates} margin={{ top: 14, right: 10, left: -18, bottom: 0 }}>
                    <CartesianGrid stroke="var(--line)" vertical={false} />
                    <XAxis dataKey="x" type="number" scale="time" domain={[days[0], days[13]]} ticks={days.filter((_, i) => i % 2 === 1)} tick={axisStyle} stroke="var(--line)" tickFormatter={tickDay} />
                    <YAxis domain={[0, 100]} tick={axisStyle} stroke="var(--line)" tickFormatter={(v) => `${v}%`} />
                    <Tooltip {...tip} formatter={(v: any) => `${Number(v).toFixed(0)}%`} />
                    <Area dataKey="rate" tooltipType="none" type="monotone" stroke="none" fill="var(--green)" fillOpacity={0.12} isAnimationActive={false} />
                    <Line dataKey="rate" name="成功率" type="monotone" stroke="var(--green)" dot={{ r: 2.5, fill: "var(--green)" }} isAnimationActive={false} />
                    <ReferenceLine y={90} stroke="var(--fg2)" strokeOpacity={0.6} strokeDasharray="4 3" label={{ value: "目標 90%", position: "insideTopLeft", fontSize: 10, fill: "var(--fg2)" }} />
                  </ComposedChart>
                </ResponsiveContainer>
              </div>
            )}
          </Card>
        </div>
      </div>
      {dlg}{toastEl}
    </div>
  );
}
