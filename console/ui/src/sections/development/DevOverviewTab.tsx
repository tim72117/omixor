// 開發 › 概覽(Swift 的 DevOverviewTab.swift)
import { Bar as RBar, BarChart, CartesianGrid, ComposedChart, Area, Legend, Line, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { useView } from "../../api";
import { ActorAvatar } from "../../components/Avatar";
import { AIBadge } from "../../components/Badge";
import { Card, cardTitleClass } from "../../components/Card";
import { axisStyle, Metric, SERIES_COLORS } from "../../components/charts";
import { EmptyState } from "../../components/EmptyState";
import { Pill } from "../../components/Pill";
import { Bar } from "../../components/ProgressBar";
import { SIcon } from "../../components/SIcon";
import { color } from "../../lib/color";
import { useOmixor } from "../../lib/directory";
import { monthDay, now, pct, startOfDay, t } from "../../lib/format";
import { useList } from "../../lib/hooks";
import { RISK, WORK_TYPE, WORK_TYPE_ORDER, type PullRequest, type Repository, type Sprint, type WorkItem } from "../../models";
import { age, aiShare, avgCoverage, ChecksIcon, cycleTimeHours, hours, isActivePR, reviewWaitHours, sprintPoints, TypeTag, useDev } from "./devShared";
import ds from "./devShared.module.css";
import css from "./DevOverviewTab.module.css";
import { StatusDot } from "../../components/StatusDot";
import { KpiGrid } from "../../components/KpiGrid";

export function DevOverviewTab() {
  const [work] = useList<WorkItem>("work_items");
  const [prs] = useList<PullRequest>("pull_requests");
  const [repos] = useList<Repository>("repos");
  const [sprintRaw] = useView<Sprint>("sprint");
  const sprint = sprintRaw && "name" in sprintRaw ? sprintRaw : undefined;
  const pts = sprintPoints(work);
  const completion = pts.total > 0 ? (pts.done / pts.total) * 100 : 0;
  const openPRs = prs.filter(isActivePR);
  const wait = reviewWaitHours(prs);
  const coverage = avgCoverage(repos);
  const low = repos.filter((r) => r.coverage < 60).length;
  const share = aiShare(prs);
  return (
    <div className="page-scroll">
      <div className="page-inner">
        <SprintBanner sprint={sprint} work={work} />
        <KpiGrid n={6}>
          <Metric title="Sprint 完成度" value={pts.total > 0 ? pct(completion, 0) : "—"} delta={pts.total > 0 ? `${pts.done}/${pts.total} 點` : null} icon="flag.checkered" tint="green" />
          <Metric title="PR 週期時間" value={hours(cycleTimeHours(prs))} delta="開啟 → 合併" icon="timer" tint="blue" />
          <Metric title="進行中 PR" value={String(openPRs.length)} delta={openPRs.length ? `${openPRs.filter((p) => p.status === "open").length} 個待審` : null} icon="arrow.triangle.pull" tint="indigo" />
          <Metric title="平均等待審查" value={hours(wait)} delta={(wait ?? 0) > 24 ? "超過 1 天" : null} deltaGood={false} icon="hourglass" tint="orange" />
          <Metric title="AI 撰寫的 PR" value={share != null ? pct(share * 100, 0) : "—"} delta={prs.length ? `${prs.filter((p) => p.author.kind === "ai").length} 個` : null} icon="sparkles" tint="purple" />
          <Metric title="平均測試覆蓋率" value={coverage != null ? pct(coverage, 0) : "—"} delta={low > 0 ? `${low} 個低於 60%` : null} deltaGood={false} icon="checkmark.shield" tint="teal" />
        </KpiGrid>
        <div className={css.cards}>
          <div className={css.wide}><BurndownCard sprint={sprint} /></div>
          <div className={css.narrow}><MyReviewsCard prs={prs} /></div>
        </div>
        <div className={css.cards}>
          <div className={css.wide}><CommitActivityCard repos={repos} /></div>
          <div className={css.narrow}><WorkByTypeCard work={work} /></div>
        </div>
      </div>
    </div>
  );
}

function SprintBanner({ sprint: s, work }: { sprint?: Sprint; work: WorkItem[] }) {
  if (!s) return <Card><div className="muted">讀取 Sprint 中…</div></Card>;
  const pts = sprintPoints(work);
  const totalDays = Math.max(1, Math.floor((t(s.end) - t(s.start)) / 86400000));
  const left = Math.max(0, Math.floor((t(s.end) - now()) / 86400000));
  const timeProgress = Math.min(1, Math.max(0, (now() - t(s.start)) / Math.max(1, t(s.end) - t(s.start))));
  const workProgress = pts.total > 0 ? pts.done / pts.total : 0;
  const row = (label: string, v: number, c: string) => (
    <div className="row gap8">
      <span className={"caption muted " + css.barLabel}>{label}</span>
      <Bar value={v} c={c} />
      <span className={"caption muted num " + css.barPct}>{pct(v * 100, 0)}</span>
    </div>
  );
  return (
    <Card>
      <div className={css.sprint}>
        <div className="col gap4 grow">
          <div className="row gap8">
            <SIcon name="flag.2.crossed.fill" color="blue" />
            <span className={cardTitleClass}>{s.name}</span>
            <Pill text={t(s.end) > now() ? `剩 ${left} 天` : "已結束"} color={left <= 2 ? "orange" : "blue"} />
          </div>
          <div className="muted">{s.goal || "尚未設定 Sprint 目標"}</div>
          <div className="caption faint">{monthDay(s.start)} – {monthDay(s.end)}・共 {totalDays} 天</div>
        </div>
        <div className={"col gap8 " + css.sprintBars}>
          {row("時間", timeProgress, "secondary")}
          {row("工作", workProgress, workProgress + 0.1 < timeProgress ? "orange" : "green")}
        </div>
      </div>
    </Card>
  );
}

function BurndownCard({ sprint: s }: { sprint?: Sprint }) {
  const empty = !s || (s.burndown.length === 0 && s.idealBurndown.length === 0);
  const day = (iso: string) => startOfDay(t(iso));
  const rows = new Map<number, { x: number; ideal?: number; actual?: number }>();
  s?.idealBurndown.forEach((p) => rows.set(day(p.date), { ...(rows.get(day(p.date)) ?? { x: day(p.date) }), ideal: p.value }));
  s?.burndown.forEach((p) => rows.set(day(p.date), { ...(rows.get(day(p.date)) ?? { x: day(p.date) }), actual: p.value }));
  const data = [...rows.values()].sort((a, b) => a.x - b.x);
  const last = s?.burndown[s.burndown.length - 1];
  const ideal = last && s?.idealBurndown.length ? s.idealBurndown.reduce((b, p) => (Math.abs(t(p.date) - t(last.date)) < Math.abs(t(b.date) - t(last.date)) ? p : b)) : undefined;
  const diff = last && ideal ? last.value - ideal.value : 0;
  const today = startOfDay(now());
  return (
    <Card title="燃盡圖" subtitle="剩餘點數:實際與理想進度">
      {empty ? <div className={css.h200}><EmptyState title="沒有燃盡資料" icon="chart.line.downtrend.xyaxis" /></div> : (
        <>
          <div className={css.h220}>
            <ResponsiveContainer>
              <ComposedChart data={data} margin={{ top: 14, right: 10, left: -18, bottom: 0 }}>
                <CartesianGrid stroke="var(--line)" vertical={false} />
                <XAxis dataKey="x" type="number" scale="time" domain={["dataMin", "dataMax"]} tick={axisStyle} stroke="var(--line)" tickFormatter={(v) => `${new Date(v).getMonth() + 1}/${new Date(v).getDate()}`} minTickGap={24} />
                <YAxis tick={axisStyle} stroke="var(--line)" />
                <Tooltip labelFormatter={(v) => monthDay(Number(v))} contentStyle={{ fontSize: "var(--fs-sm)", background: "var(--bg2)", border: "1px solid var(--line)" }} />
                <Legend wrapperStyle={{ fontSize: "var(--fs-xs)" }} iconSize={8} />
                <Area dataKey="actual" name="實際" tooltipType="none" stroke="none" fill="var(--blue)" fillOpacity={0.1} legendType="none" connectNulls isAnimationActive={false} />
                <Line dataKey="ideal" name="理想" stroke="var(--gray)" strokeDasharray="5 4" strokeWidth={1.5} dot={false} connectNulls isAnimationActive={false} />
                <Line dataKey="actual" name="實際" stroke="var(--blue)" strokeWidth={2.5} dot={{ r: 2.5, fill: "var(--blue)" }} connectNulls isAnimationActive={false} />
                {s && t(s.start) <= now() && t(s.end) >= now() && <ReferenceLine x={today} stroke="var(--fg2)" strokeOpacity={0.5} strokeDasharray="2 3" label={{ value: "今天", position: "insideTopLeft", fontSize: 11, fill: "var(--fg2)" }} />}
              </ComposedChart>
            </ResponsiveContainer>
          </div>
          {last && (
            <div className="row gap12 caption">
              <span className={"row gap4 " + css.blue}><StatusDot c="blue" size={7} />剩 {Math.trunc(last.value)} 點</span>
              {ideal && <span className={diff > 0.5 ? ds.orange : ds.green}>{diff > 0.5 ? `落後理想 ${Math.round(diff)} 點` : diff < -0.5 ? `超前理想 ${Math.round(-diff)} 點` : "與理想進度一致"}</span>}
            </div>
          )}
        </>
      )}
    </Card>
  );
}

function MyReviewsCard({ prs }: { prs: PullRequest[] }) {
  const o = useOmixor();
  const dev = useDev();
  const mine = prs.filter((p) => p.reviewers.includes(o.me) && (p.status === "open" || p.status === "changesRequested")).sort((a, b) => t(a.createdAt) - t(b.createdAt));
  return (
    <Card title="等我審查" subtitle={mine.length ? `${mine.length} 個 PR 指定你為審查者` : "目前沒有待審的 PR"}>
      {mine.length === 0 ? (
        <div className={"col gap6 muted " + css.allDone}><SIcon name="checkmark.seal" size={26} color="green" />都審完了</div>
      ) : (
        <>
          <div className="col">
            {mine.slice(0, 6).map((pr) => (
              <button key={pr.id} className={"row " + css.review} onClick={() => dev.showPR(pr.id)}>
                <ActorAvatar actor={pr.author} size={24} plainAI />
                <div className={"col grow " + css.reviewText}>
                  <span className="ellipsis">{pr.title}</span>
                  <span className="caption muted ellipsis"><span className="num">#{pr.id}</span>&nbsp;&nbsp;{pr.repo}&nbsp;&nbsp;・等了 {age(pr.createdAt)}</span>
                </div>
                {pr.aiReview && <StatusDot c={RISK[pr.aiReview.risk].color} size={7} title={`AI 判斷:${RISK[pr.aiReview.risk].label}`} />}
                <ChecksIcon s={pr.checks} />
                <SIcon name="chevron.right" size={11} className="faint" />
              </button>
            ))}
          </div>
          {mine.length > 6 && <button className={"btn link " + css.start} onClick={() => dev.setTab("pulls")}>查看全部 {mine.length} 個</button>}
        </>
      )}
    </Card>
  );
}

function CommitActivityCard({ repos }: { repos: Repository[] }) {
  // 本週一 00:00(Calendar 的 weekOfYear 起點;zh_TW 以星期日為週首)
  const d0 = new Date(startOfDay(now()));
  const thisWeek = d0.getTime() - d0.getDay() * 86400000;
  const rows = new Map<number, any>();
  let total = 0;
  repos.forEach((r) => r.weeklyCommits.forEach((c, i) => {
    const off = r.weeklyCommits.length - 1 - i;
    const wk = thisWeek - off * 7 * 86400000;
    const row = rows.get(wk) ?? { x: wk };
    row[r.name] = c;
    rows.set(wk, row);
    total += c;
  }));
  const data = [...rows.values()].sort((a, b) => a.x - b.x);
  const empty = data.length === 0;
  return (
    <Card title="Commit 活動" subtitle={empty ? "最近 12 週" : `最近 12 週・共 ${total} 個 commit`}>
      {empty ? <div className={css.h200}><EmptyState title="沒有 commit 資料" icon="chart.bar" /></div> : (
        <div className={css.h240}>
          <ResponsiveContainer>
            <BarChart data={data} margin={{ top: 6, right: 6, left: -18, bottom: 0 }}>
              <CartesianGrid stroke="var(--line)" vertical={false} />
              <XAxis dataKey="x" tick={axisStyle} stroke="var(--line)" tickFormatter={(v) => `${new Date(v).getMonth() + 1}/${new Date(v).getDate()}`} interval={1} />
              <YAxis tick={axisStyle} stroke="var(--line)" />
              <Tooltip labelFormatter={(v) => `${monthDay(Number(v))} 那週`} contentStyle={{ fontSize: "var(--fs-sm)", background: "var(--bg2)", border: "1px solid var(--line)" }} cursor={{ fill: "color-mix(in srgb, var(--fg) 5%, transparent)" }} />
              <Legend wrapperStyle={{ fontSize: "var(--fs-xs)" }} iconSize={8} align="left" />
              {repos.map((r, i) => <RBar key={r.id} dataKey={r.name} stackId="a" fill={SERIES_COLORS[i % SERIES_COLORS.length]} isAnimationActive={false} />)}
            </BarChart>
          </ResponsiveContainer>
        </div>
      )}
    </Card>
  );
}

function WorkByTypeCard({ work }: { work: WorkItem[] }) {
  const dev = useDev();
  const items = work.filter((w) => w.status !== "backlog");
  const totals = WORK_TYPE_ORDER.map((ty) => items.filter((w) => w.type === ty).reduce((a, w) => a + w.points, 0));
  const maxPts = Math.max(0, ...totals);
  const aiCount = work.filter((w) => w.createdByAI).length;
  return (
    <Card title="工作類型分布" subtitle="本 Sprint 點數(不含 Backlog)">
      {items.length === 0 ? <div className={css.h200}><EmptyState title="沒有工作項目" icon="list.bullet.rectangle" /></div> : (
        <div className={"col " + css.types}>
          {WORK_TYPE_ORDER.map((ty, i) => {
            const of = items.filter((w) => w.type === ty);
            const total = totals[i];
            const done = of.filter((w) => w.status === "done").reduce((a, w) => a + w.points, 0);
            const wFrac = maxPts > 0 ? total / maxPts : 0;
            const c = color(WORK_TYPE[ty].color);
            return (
              <div key={ty} className="col gap4">
                <div className="row"><TypeTag type={ty} /><span className="grow" /><span className="caption muted num">{done}/{total} 點・{of.length} 項</span></div>
                <div className={css.typeBar}>
                  <div style={{ width: `${wFrac * 100}%`, background: `color-mix(in srgb, ${c} 30%, transparent)` }} />
                  <div style={{ width: `${(total > 0 ? wFrac * done / total : 0) * 100}%`, background: c }} />
                </div>
              </div>
            );
          })}
          {aiCount > 0 && (
            <>
              <div className="divider" />
              <div className="row gap8"><AIBadge /><span className="caption muted">{aiCount} 個項目由 AI 建立</span><span className="grow" /><button className="btn link caption" onClick={() => dev.setTab("board")}>在看板查看</button></div>
            </>
          )}
        </div>
      )}
    </Card>
  );
}
