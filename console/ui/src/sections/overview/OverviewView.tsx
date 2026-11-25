// 總覽:一眼看完服務狀態、需要處理的事、交付進度與最近動態。對應 Swift 版 Features/Overview/。
import { useState, type CSSProperties } from "react";
import { action, tool, useView } from "../../api";
import { ActorAvatar } from "../../components/Avatar";
import { AIBadge, EnvBadge, RunStatusBadge } from "../../components/Badge";
import { Card, cardClass, CardHeader, cardTitleClass } from "../../components/Card";
import { Sparkline } from "../../components/charts";
import { useConfirm } from "../../components/Confirm";
import { InlineEmpty } from "../../components/EmptyState";
import { ErrorText } from "../../components/ErrorText";
import { useContextMenu } from "../../components/Menu";
import { Pill } from "../../components/Pill";
import { Bar, ProgressBar } from "../../components/ProgressBar";
import { Segmented } from "../../components/Segmented";
import { SIcon } from "../../components/SIcon";
import { StatusDot } from "../../components/StatusDot";
import { ViewLoading } from "../../components/ViewState";
import { useAct } from "../../components/Toast";
import { isAI } from "../../lib/actor";
import { color } from "../../lib/color";
import { OmixorProvider, useOmixor } from "../../lib/directory";
import { dur, isToday, monthDayTime, now, pct, t } from "../../lib/format";
import { useList, useWidth } from "../../lib/hooks";
import {
  ENV, HEALTH, HEALTH_ORDER, healthRank, INCIDENT_STATUS, RISK, RUN, SEVERITY, TIME_RANGE, WORK_STATUS,
  type ActivityItem, type Alert, type ApprovalRequest, type AutomationRun, type Deployment, type EnvKey, type Incident,
  type OnCallShift, type PipelineRun, type PullRequest, type Service, type Sprint, type WorkItem,
} from "../../models";
import css from "./overview.module.css";

/** 顏色給 module CSS 的 var(--c) */
const tint = (c: string) => ({ "--c": c }) as CSSProperties;
import { relative } from "../../fmt";
import type { SectionProps } from "..";
import { canGo, useCanGo } from "../../features";
import { plainClass, tileClass } from "../../components/Tile";
import { KpiGrid } from "../../components/KpiGrid";

export default function OverviewView(_: SectionProps) {
  return <OmixorProvider><Overview /></OmixorProvider>;
}

type Data = {
  services: Service[]; alerts: Alert[]; incidents: Incident[]; approvals: ApprovalRequest[]; deployments: Deployment[];
  runs: PipelineRun[]; aiRuns: AutomationRun[]; work: WorkItem[]; sprint?: Sprint; prs: PullRequest[]; onCall: OnCallShift[];
  activity: ActivityItem[]; env: EnvKey;
  firing: Alert[]; open: Incident[]; pending: ApprovalRequest[];
  show: Show;
};

// 總覽的區塊跟著對應頁面的功能開關:頁面關了,它的卡片與 KPI 也不顯示(資料也不算進狀態橫幅)
type Show = { monitoring: boolean; infrastructure: boolean; pipelines: boolean; incidents: boolean; automation: boolean; development: boolean };

const sevColor = (sev: number) => (sev === 1 ? "red" : sev === 2 ? "orange" : sev === 3 ? "yellow" : "blue");

function Overview() {
  const o = useOmixor();
  const [services, servicesErr, servicesOk] = useList<Service>("services");
  const [alerts, alertsErr, alertsOk] = useList<Alert>("alerts");
  const [incidents, incidentsErr, incidentsOk] = useList<Incident>("incidents");
  const [approvals, approvalsErr] = useList<ApprovalRequest>("approvals");
  const [deployments, deploymentsErr] = useList<Deployment>("deployments");
  const [runs, runsErr] = useList<PipelineRun>("pipeline_runs");
  const [aiRuns, aiRunsErr] = useList<AutomationRun>("automation_runs");
  const [work, workErr] = useList<WorkItem>("work_items");
  const [sprint, sprintErr] = useView<Sprint>("sprint");
  const [prs, prsErr] = useList<PullRequest>("pull_requests");
  const [onCall, onCallErr] = useList<OnCallShift>("on_call");
  const [activity, activityErr] = useList<ActivityItem>("activity");
  // 讀不到的資料:卡片照常畫(空的),上方列出哪些讀不到,免得把「讀不到」看成「沒有」
  const failed = ([
    ["服務", servicesErr], ["告警", alertsErr], ["事件", incidentsErr], ["核准請求", approvalsErr], ["部署", deploymentsErr],
    ["Pipeline", runsErr], ["AI 執行紀錄", aiRunsErr], ["工作項目", workErr], ["Sprint", sprintErr], ["Pull Request", prsErr],
    ["值班", onCallErr], ["最近動態", activityErr],
  ] as const).filter(([, e]) => e);
  const env = (o.nav?.env ?? "production") as EnvKey;
  const tr = TIME_RANGE[o.nav?.timeRange ?? "24h"]?.label ?? o.nav?.timeRange;
  const [ref, w] = useWidth<HTMLDivElement>();
  const can = useCanGo();
  const show: Show = {
    monitoring: can("monitoring"), infrastructure: can("infrastructure"), pipelines: can("pipelines"),
    incidents: can("incidents"), automation: can("automation"), development: can("development"),
  };
  // 關掉的功能,它的資料不算進狀態橫幅與 KPI
  const svc = show.monitoring || show.infrastructure ? services : [];
  const al = show.monitoring ? alerts : [];
  const inc = show.incidents ? incidents : [];
  const ap = show.automation ? approvals : [];
  const d: Data = {
    services: svc, alerts: al, incidents: inc, approvals: ap, deployments, runs, aiRuns, work, sprint: sprint && "name" in sprint ? sprint : undefined, prs, onCall, activity, env,
    firing: al.filter((a) => a.state === "firing").sort((a, b) => SEVERITY[a.severity].rank - SEVERITY[b.severity].rank),
    open: inc.filter((i) => i.status !== "resolved"),
    pending: ap.filter((a) => a.state === "pending"),
    show,
  };
  const banner = show.monitoring || show.infrastructure || show.incidents;
  const left = [
    show.infrastructure && <ServicesCard key="svc" d={d} />,
    show.pipelines && <DeliveryCard key="cd" d={d} />,
    (show.development || show.monitoring) && (
      <div key="pair" className={"row " + css.columns}>
        {show.development && <div className="grow"><SprintCard d={d} /></div>}
        {show.monitoring && <div className="grow"><ErrorBudgetCard d={d} /></div>}
      </div>
    ),
    <ActivityCard key="act" d={d} />,
  ].filter(Boolean);
  // 值班跟著事件(輪值是事件處理用的)
  const right = [
    show.automation && <ApprovalsCard key="ap" d={d} />,
    show.monitoring && <AlertsCard key="al" d={d} />,
    show.incidents && <IncidentsCard key="inc" d={d} />,
    show.incidents && <OnCallCard key="oc" d={d} />,
  ].filter(Boolean);
  const two = w - 40 >= 940 && right.length > 0;
  const side = Math.min(420, Math.max(320, (w - 40) * 0.32));
  return (
    <div className="page-scroll" ref={ref}>
      <div className="page-inner">
        <div className={"row " + css.baseline}>
          <div className="col gap4">
            <div className="page-title">總覽</div>
            <div className="muted">{ENV[env].label} 環境 · 最近 {tr}</div>
          </div>
          <span className="grow" />
          <div className="row gap8">
            <EnvBadge env={env} />
            <button className="btn" title="開啟指令面板" onClick={() => window.dispatchEvent(new CustomEvent("omixor-command-palette"))}><SIcon name="magnifyingglass" /> 搜尋</button>
          </div>
        </div>
        {failed.length > 0 && <ErrorText>讀不到{failed.map(([n]) => n).join("、")}:{failed[0][1]}</ErrorText>}
        {/* 狀態橫幅要服務、告警、事件都讀到才畫,否則載入中會短暫顯示「尚未取得監控資料」 */}
        {banner && (servicesErr || alertsErr || incidentsErr ? null : servicesOk && alertsOk && incidentsOk ? <StatusBanner d={d} /> : <ViewLoading variant="inline" />)}
        <KPIRow d={d} />
        {two ? (
          <div className={"row gap12 " + css.columns}>
            <div className={"col grow " + css.gap16}>{left}</div>
            <div className={"col " + css.gap16 + " " + css.none} style={{ width: side }}>{right}</div>
          </div>
        ) : (
          <>
            {show.monitoring && <AlertsCard d={d} />}
            {show.automation && <ApprovalsCard d={d} />}
            {show.incidents && <IncidentsCard d={d} />}
            {show.infrastructure && <ServicesCard d={d} />}
            {show.pipelines && <DeliveryCard d={d} />}
            {(show.development || show.monitoring) && (
              <div className={css.autoGrid + " " + css.grid280}>
                {show.development && <SprintCard d={d} />}
                {show.monitoring && <ErrorBudgetCard d={d} />}
              </div>
            )}
            {show.incidents && <OnCallCard d={d} />}
            <ActivityCard d={d} />
          </>
        )}
      </div>
    </div>
  );
}

// MARK: - 狀態橫幅

function StatusBanner({ d }: { d: Data }) {
  const o = useOmixor();
  const down = d.services.filter((s) => s.health === "down");
  const degraded = d.services.filter((s) => s.health === "degraded");
  const maint = d.services.filter((s) => s.health === "maintenance");
  const critical = d.firing.filter((a) => a.severity === "critical").length;
  const major = d.open.filter((i) => i.sev <= 2).length;
  const level = d.services.length === 0 && d.alerts.length === 0 && d.incidents.length === 0 ? "unknown"
    : down.length || critical || major ? "major"
      : degraded.length || d.firing.length || d.open.length ? "degraded" : "ok";
  const c = { unknown: "gray", ok: "green", degraded: "orange", major: "red" }[level];
  const icon = { unknown: "questionmark.circle.fill", ok: "checkmark.seal.fill", degraded: "exclamationmark.triangle.fill", major: "xmark.octagon.fill" }[level];
  let title = "", detail = "";
  if (level === "unknown") { title = "尚未取得監控資料"; detail = "連上監控整合後,這裡會顯示所有服務的即時狀態。"; }
  else if (level === "ok") { title = "所有系統運作正常"; detail = `${d.services.length} 個服務全部正常${maint.length ? `,${maint.length} 個維護中` : ""}。`; }
  else {
    if (level === "degraded") title = degraded.length ? `${degraded.length} 個服務效能下降` : "有待處理的告警";
    else title = down.length ? `${down.length} 個服務中斷` : major ? "重大事件處理中" : "有 Critical 告警觸發中";
    const parts: string[] = [];
    const affected = [...down, ...degraded].map((s) => s.name);
    if (affected.length) parts.push("受影響:" + affected.slice(0, 4).join("、") + (affected.length > 4 ? " 等" : ""));
    if (d.firing.length) parts.push(`${d.firing.length} 則告警觸發中`);
    if (d.open.length) parts.push(`${d.open.length} 起事件未結案`);
    detail = parts.join(" · ");
  }
  const col = color(c);
  return (
    <div className={"row gap12 " + css.banner} style={tint(col)}>
      <SIcon name={icon} size={26} color={c} />
      <div className="col gap4 grow">
        <div className={cardTitleClass}>{title}</div>
        <div className={"muted " + css.base}>{detail}</div>
      </div>
      {(level === "major" || level === "degraded") && (
        <>
          {d.open.length > 0 && canGo("incidents") && <button className="btn" onClick={() => o.go("incidents", d.open[0]?.id)}>查看事件</button>}
          {canGo("monitoring") && <button className={"btn " + css.white} style={{ background: col, borderColor: col }} onClick={() => o.go("monitoring", d.firing[0]?.id)}>查看告警</button>}
        </>
      )}
      {level === "ok" && canGo("infrastructure") && <button className="btn" onClick={() => o.go("infrastructure")}>服務列表</button>}
    </div>
  );
}

// MARK: - KPI

function KPIRow({ d }: { d: Data }) {
  const o = useOmixor();
  const s = d.services;
  const avgUptime = s.length ? s.reduce((a, x) => a + x.uptime30d, 0) / s.length : undefined;
  const slo = s.length ? Math.max(...s.map((x) => x.sloTarget)) : 99.9;
  const total = s.reduce((a, x) => a + x.rps, 0);
  const weighted = (f: (x: Service) => number) => !s.length ? undefined : total > 0 ? s.reduce((a, x) => a + f(x) * x.rps, 0) / total : s.reduce((a, x) => a + f(x), 0) / s.length;
  const p95 = weighted((x) => x.latencyP95);
  const err = weighted((x) => x.errorRate);
  const deploysToday = d.deployments.filter((x) => x.env === d.env && isToday(x.startedAt));
  const aiToday = d.aiRuns.filter((r) => isToday(r.startedAt));
  const aiHours = aiToday.reduce((sum, r) => {
    const a = o.automations.find((x) => x.id === r.automation);
    return a && a.runs30d > 0 ? sum + a.timeSavedHours / a.runs30d : sum;
  }, 0);
  const failed = deploysToday.filter((x) => x.status === "failed").length;
  const running = deploysToday.filter((x) => x.status === "running").length;
  const deployCaption = failed ? `${failed} 次失敗` : running ? `${running} 個進行中` : deploysToday.length ? "全部成功" : "今天還沒有部署";
  const minSev = d.open.length ? Math.min(...d.open.map((i) => i.sev)) : undefined;
  const sh = d.show;
  if (!sh.monitoring && !sh.pipelines && !sh.incidents && !sh.automation) return null;
  const n = (sh.monitoring ? 3 : 0) + (sh.pipelines ? 1 : 0) + (sh.incidents ? 1 : 0) + (sh.automation ? 1 : 0);
  return (
    <KpiGrid n={n}>
      {sh.monitoring && <>
      <KPITile title="可用性(30 天)" value={avgUptime != null ? pct(avgUptime, 2) : "—"} caption={`SLO ${pct(slo, 2)}`} captionColor={(avgUptime ?? 100) >= slo ? "green" : "red"} icon="checkmark.shield" tint="green" onClick={canGo("monitoring") ? () => o.go("monitoring") : undefined} />
      <KPITile title="p95 延遲" value={p95 != null ? `${Math.round(p95)} ms` : "—"} caption={p95 != null ? (p95 > 500 ? "高於 500 ms 目標" : "在 500 ms 目標內") : "尚無資料"} captionColor={(p95 ?? 0) > 500 ? "orange" : "secondary"} icon="speedometer" tint="blue" onClick={canGo("monitoring") ? () => o.go("monitoring") : undefined} />
      <KPITile title="錯誤率" value={err != null ? pct(err, 2) : "—"} caption={err != null ? (err > 1 ? "超過 1% 警戒" : "低於 1% 警戒") : "尚無資料"} captionColor={(err ?? 0) > 1 ? "red" : "secondary"} icon="exclamationmark.bubble" tint="orange" onClick={canGo("monitoring") ? () => o.go("monitoring") : undefined} />
      </>}
      {sh.pipelines && <KPITile title="今日部署" value={String(deploysToday.length)} caption={deployCaption} captionColor={failed ? "red" : "secondary"} icon="shippingbox" tint="indigo" onClick={canGo("pipelines") ? () => o.go("pipelines") : undefined} />}
      {sh.incidents && <KPITile title="進行中事件" value={String(d.open.length)} caption={minSev != null ? `最高 SEV${minSev}` : "目前沒有事件"} captionColor={d.open.length ? "red" : "secondary"} icon="flame" tint={d.open.length ? "red" : "secondary"} onClick={canGo("incidents") ? () => o.go("incidents", d.open[0]?.id) : undefined} />}
      {sh.automation && <KPITile title="AI 今日動作" value={String(aiToday.length)} caption={aiHours > 0 ? `約省下 ${aiHours.toFixed(1)} 小時` : `${d.pending.length} 項等你決定`} captionColor="purple" icon="sparkles" tint="purple" onClick={canGo("automation") ? () => o.go("automation") : undefined} />}
    </KpiGrid>
  );
}

function KPITile({ title, value, caption, captionColor, icon, tint, onClick }: { title: string; value: string; caption: string; captionColor: string; icon: string; tint: string; onClick?: () => void }) {
  return (
    <button className={cardClass + " " + tileClass + " " + css.gap6} onClick={onClick}>
      <div className={"row gap6 muted ellipsis " + css.base}><SIcon name={icon} color={tint} />{title}</div>
      <div className={"num " + css.big}>{value}</div>
      <div className={"caption ellipsis " + css.medium} style={{ color: color(captionColor) }}>{caption}</div>
    </button>
  );
}

// MARK: - 服務健康

function ServicesCard({ d }: { d: Data }) {
  const o = useOmixor();
  const sorted = [...d.services].sort((a, b) => healthRank(a.health) - healthRank(b.health) || a.tier - b.tier || a.name.localeCompare(b.name));
  const healthy = d.services.filter((s) => s.health === "healthy").length;
  return (
    <Card>
      <CardHeader title="服務健康" linkTitle="伺服器與服務" onLink={canGo("infrastructure") ? () => o.go("infrastructure") : undefined} />
      {d.services.length === 0 ? <InlineEmpty icon="server.rack" text="尚未登錄任何服務" /> : (
        <>
          <div className="row gap12 caption muted">
            {HEALTH_ORDER.map((h) => {
              const n = d.services.filter((s) => s.health === h).length;
              return n > 0 ? <span key={h} className="row gap4"><StatusDot c={HEALTH[h].color} size={7} />{HEALTH[h].label} {n}</span> : null;
            })}
            <span className="grow" />
            <span className="num">{healthy}/{d.services.length} 正常</span>
          </div>
          <div className={css.autoGrid + " " + css.grid180}>
            {sorted.map((s) => {
              const c = color(HEALTH[s.health].color);
              const ok = s.health === "healthy";
              return (
                <button key={s.id} className={tileClass + " col gap4 " + css.svcTile + (ok ? "" : " " + css.bad)} title={`${s.name}:${s.summary}`} onClick={canGo("infrastructure") ? () => o.go("infrastructure", s.id) : undefined}
                  style={tint(c)}>
                  <div className={"row gap6 " + css.wfull}>
                    <SIcon name={HEALTH[s.health].icon} color={HEALTH[s.health].color} />
                    <span className={"ellipsis " + css.medium}>{s.name}</span>
                    <span className="grow" />
                    {s.tier === 1 && <span className={"muted " + css.tier} title="第 1 級(最關鍵)服務">T1</span>}
                  </div>
                  <div className="caption muted num ellipsis">p95 {Math.trunc(s.latencyP95)} ms&nbsp;&nbsp;錯誤 {pct(s.errorRate, 2)}</div>
                  <div className={"row gap4 faint mono ellipsis " + css.xs}><SIcon name="tag" size={10} />{s.versions?.[d.env] ?? `未部署到 ${ENV[d.env].short}`}</div>
                </button>
              );
            })}
          </div>
        </>
      )}
    </Card>
  );
}

// MARK: - 告警

function AlertsCard({ d }: { d: Data }) {
  const o = useOmixor();
  const { run, toastEl } = useAct();
  const menu = useContextMenu();
  const upd = (id: string, a: string) => run(() => tool("alert_update", { alert_id: id, action: a }));
  return (
    <Card>
      <CardHeader title="觸發中的告警" count={d.firing.length} countColor={d.firing.some((a) => a.severity === "critical") ? "red" : "orange"} onLink={canGo("monitoring") ? () => o.go("monitoring") : undefined} />
      {d.firing.length === 0 ? <InlineEmpty icon="bell.slash" text="沒有觸發中的告警" c="green" /> : (
        <>
          <div className="col">
            {d.firing.slice(0, 5).map((a, i) => (
              <div key={a.id} className={"row gap8 " + css.alertRow + (i ? " " + css.sepTop : "")}
                onContextMenu={menu.open([
                  { label: "確認", onClick: () => upd(a.id, "acknowledge") },
                  { label: "靜音", onClick: () => upd(a.id, "silence") },
                  { label: "解除", onClick: () => upd(a.id, "resolve") },
                  "sep",
                  ...(canGo("monitoring") ? [{ label: "在監控頁開啟", onClick: () => o.go("monitoring", a.id) }] : []),
                  ...(a.incidentId && canGo("incidents") ? [{ label: `開啟事件 ${a.incidentId}`, onClick: () => o.go("incidents", a.incidentId) }] : []),
                ])}>
                <span className={css.pt5}><StatusDot c={SEVERITY[a.severity].color} pulsing={a.severity === "critical"} /></span>
                <div className="col gap4 grow">
                  <button className={plainClass + " " + css.medium} onClick={canGo("monitoring") ? () => o.go("monitoring", a.id) : undefined}>{a.title}</button>
                  <div className="caption muted ellipsis">{o.service(a.service)?.name ?? a.service} · <span className="num">{a.value}</span> · {relative(a.startedAt)}</div>
                  {a.aiNote && <div className={"caption muted row gap4 " + css.top}><SIcon name="sparkles" color="purple" size={12} /><span className={css.clamp2}>{a.aiNote}</span></div>}
                </div>
                <button className="btn small" title="確認這則告警,表示你正在處理" onClick={() => upd(a.id, "acknowledge")}>確認</button>
              </div>
            ))}
          </div>
          {d.firing.length > 5 && <div className="caption muted">還有 {d.firing.length - 5} 則告警</div>}
        </>
      )}
      {menu.el}{toastEl}
    </Card>
  );
}

// MARK: - AI 待核准

function ApprovalsCard({ d }: { d: Data }) {
  const o = useOmixor();
  const pending = [...d.pending].sort((a, b) => t(a.expiresAt) - t(b.expiresAt));
  return (
    <Card>
      <CardHeader icon={<SIcon name="sparkles" color="purple" />} title="AI 需要你決定" count={pending.length} countColor="purple" onLink={canGo("automation") ? () => o.go("automation") : undefined} />
      {pending.length === 0 ? <InlineEmpty icon="checkmark.seal" text="沒有等待核准的 AI 動作" c="green" /> : (
        <>
          <div className={"col gap8 " + css.gap10}>{pending.slice(0, 3).map((r) => <ApprovalRow key={r.id} r={r} />)}</div>
          {pending.length > 3 && <div className="caption muted">還有 {pending.length - 3} 項等待核准</div>}
        </>
      )}
    </Card>
  );
}

function ApprovalRow({ r }: { r: ApprovalRequest }) {
  const o = useOmixor();
  const [expanded, setExpanded] = useState(false);
  const [dlg, ask] = useConfirm();
  const { run, toastEl } = useAct();
  const decide = (approve: boolean) => run(() => action("approval_decide", { id: r.id, approve }));
  const left = t(r.expiresAt) - now();
  const clamp = expanded ? "" : " " + css.clamp2;
  return (
    <div className={"col gap8 " + css.approval}>
      <div className="row gap6">
        <Pill text={RISK[r.risk].label} color={RISK[r.risk].color} />
        <span className="caption muted ellipsis">{o.automations.find((a) => a.id === r.automation)?.name ?? "AI 自動化"}</span>
        <span className="grow" />
        <span className={"caption " + (left < 900_000 ? css.orange : css.fg2)}>{left < 0 ? "已逾期" : `${relative(r.expiresAt)}到期`}</span>
      </div>
      <div className={css.bold}>{r.title}</div>
      <div className={"caption muted" + clamp}>{r.rationale}</div>
      <div className={"mono " + css.actionCode + clamp}>{r.action}</div>
      {expanded && (
        <>
          {r.evidence.length > 0 && (
            <div className="col gap4">
              <div className={"caption " + css.bold}>依據</div>
              {r.evidence.map((e) => <div key={e} className={"caption muted row gap4 " + css.top}><SIcon name="checkmark.circle" size={12} />{e}</div>)}
            </div>
          )}
          <div className="col gap4">
            <div className={"caption " + css.bold}>回滾計畫</div>
            <div className="caption muted">{r.rollbackPlan}</div>
          </div>
        </>
      )}
      <div className="row gap8">
        <button className="btn link caption" onClick={() => setExpanded(!expanded)}>{expanded ? "收合" : "詳細內容"}</button>
        <span className="grow" />
        <button className="btn small" onClick={() => decide(false)}>拒絕</button>
        <button className="btn small purple" onClick={() => r.risk === "high" ? ask({
          title: "核准高風險動作?",
          message: `「${r.title}」被標為高風險。核准後 AI 會立即執行,必要時依回滾計畫復原:${r.rollbackPlan}`,
          buttons: [{ label: "核准並執行", primary: true, onClick: () => decide(true) }],
        }) : decide(true)}>核准</button>
      </div>
      {dlg}{toastEl}
    </div>
  );
}

// MARK: - 事件

function IncidentsCard({ d }: { d: Data }) {
  const o = useOmixor();
  const open = [...d.open].sort((a, b) => a.sev - b.sev || t(a.startedAt) - t(b.startedAt));
  const lastResolved = d.incidents.map((i) => t(i.resolvedAt)).filter((x) => !isNaN(x)).sort((a, b) => b - a)[0];
  return (
    <Card>
      <CardHeader title="進行中的事件" count={open.length} countColor={open.length ? "red" : "secondary"} onLink={canGo("incidents") ? () => o.go("incidents") : undefined} />
      {open.length === 0 ? (
        <InlineEmpty icon="flame" c="green" text={lastResolved ? `沒有進行中的事件 · 上次結案 ${relative(new Date(lastResolved).toISOString())}` : "沒有進行中的事件"} />
      ) : (
        <div className="col gap8">
          {open.slice(0, 3).map((inc) => {
            const c = color(sevColor(inc.sev));
            return (
              <button key={inc.id} className={tileClass + " col gap6 " + css.incTile} onClick={canGo("incidents") ? () => o.go("incidents", inc.id) : undefined}
                style={tint(c)}>
                <div className={"row gap6 " + css.wfull}>
                  <Pill text={`SEV${inc.sev}`} color={sevColor(inc.sev)} />
                  <span className="caption mono muted">{inc.id}</span>
                  <span className="grow" />
                  <Pill text={INCIDENT_STATUS[inc.status].label} color={INCIDENT_STATUS[inc.status].color} />
                </div>
                <div className={css.bold}>{inc.title}</div>
                {inc.impact && <div className={"caption muted " + css.clamp2}>{inc.impact}</div>}
                {inc.aiSummary && <div className={"caption muted row gap4 " + css.top}><SIcon name="sparkles" color="purple" size={12} /><span className={css.clamp3}>{inc.aiSummary}</span></div>}
                <div className={"row gap6 caption muted " + css.wfull}>
                  <ActorAvatar plainAI actor={{ kind: "person", id: inc.commander }} size={16} />
                  指揮官 {o.person(inc.commander)?.name ?? inc.commander}
                  <span className="grow" />
                  <SIcon name="clock" size={12} />已持續 {dur((now() - t(inc.startedAt)) / 1000)}
                </div>
              </button>
            );
          })}
        </div>
      )}
    </Card>
  );
}

// MARK: - 值班

function OnCallCard({ d }: { d: Data }) {
  const o = useOmixor();
  const n = now();
  const current = d.onCall.filter((s) => t(s.start) <= n && t(s.end) > n).sort((a, b) => a.role.localeCompare(b.role));
  const next = d.onCall.filter((s) => t(s.start) > n).sort((a, b) => t(a.start) - t(b.start))[0];
  const row = (s: OnCallShift, caption: string) => (
    <div key={s.id} className={"row gap8 " + css.gap10}>
      <ActorAvatar plainAI actor={{ kind: "person", id: s.person }} size={28} />
      <div className={"col grow " + css.gap1}>
        <span className={css.medium}>{o.person(s.person)?.name ?? s.person}</span>
        <span className="caption muted">{o.person(s.person)?.role ?? ""}</span>
      </div>
      <div className={"col " + css.end + " " + css.gap1}>
        <Pill text={s.role} color={s.role === "Primary" ? "var(--accent)" : "secondary"} />
        <span className={"muted " + css.xs}>{caption}</span>
      </div>
    </div>
  );
  return (
    <Card>
      <CardHeader title="值班" />
      {current.length === 0 && !next ? <InlineEmpty icon="person.badge.clock" text="尚未設定值班輪值" /> : (
        <>
          {current.map((s) => row(s, `到 ${monthDayTime(s.end)}`))}
          {next && <><div className="divider" /><div className="caption muted">下一班</div>{row(next, `${relative(next.start)}開始`)}</>}
        </>
      )}
    </Card>
  );
}

// MARK: - CI/CD

function DeliveryCard({ d }: { d: Data }) {
  const o = useOmixor();
  const inEnv = d.runs.filter((r) => !r.targetEnv || r.targetEnv === d.env);
  const isActive = (r: PipelineRun) => r.status === "running" || r.status === "queued" || r.status === "waitingApproval";
  const byStart = (a: { startedAt: string }, b: { startedAt: string }) => t(b.startedAt) - t(a.startedAt);
  const runs = [...inEnv.filter(isActive).sort(byStart), ...inEnv.filter((r) => !isActive(r)).sort(byStart)].slice(0, 4);
  const deps = d.deployments.filter((x) => x.env === d.env).sort(byStart).slice(0, 4);
  const runningCount = d.runs.filter((r) => r.status === "running" || r.status === "queued").length;
  const label = (text: string, icon: string) => <div className={"row gap4 caption muted " + css.sectionLabel}><SIcon name={icon} size={12} />{text}</div>;
  return (
    <Card>
      <CardHeader title="CI/CD" count={runningCount} countColor="blue" linkTitle="所有流程" onLink={canGo("pipelines") ? () => o.go("pipelines") : undefined} />
      {runs.length === 0 && deps.length === 0 ? <InlineEmpty icon="arrow.triangle.branch" text="最近沒有流程執行或部署" /> : (
        <div className={css.autoGrid + " " + css.grid300}>
          <div className="col gap4">
            {label("流程執行", "gearshape.2")}
            {runs.length === 0 && <div className={"caption muted " + css.py6}>沒有流程執行</div>}
            {runs.map((r, i) => <RunRow key={r.id} run={r} first={i === 0} />)}
          </div>
          <div className="col gap4">
            {label(`部署 · ${ENV[d.env].label}`, "shippingbox")}
            {deps.length === 0 && <div className={"caption muted " + css.py6}>{ENV[d.env].label} 最近沒有部署</div>}
            {deps.map((x, i) => <DeployRow key={x.id} d={x} first={i === 0} />)}
          </div>
        </div>
      )}
    </Card>
  );
}

function RunRow({ run, first }: { run: PipelineRun; first: boolean }) {
  const o = useOmixor();
  const { run: act, toastEl } = useAct();
  const done = run.stages.filter((s) => s.status === "succeeded").length;
  const progress = run.stages.length ? Math.min(1, (done + (run.stages.some((s) => s.status === "running") ? 0.5 : 0)) / run.stages.length) : run.status === "succeeded" ? 1 : 0;
  const upd = (a: string) => act(() => tool("pipeline_update", { run_id: run.id, action: a }));
  return (
    <div className={"col gap4 " + css.runRow + (first ? "" : " " + css.sepTop)} onClick={canGo("pipelines") ? () => o.go("pipelines", String(run.id)) : undefined}>
      <div className="row gap6">
        <SIcon name={RUN[run.status].icon} color={RUN[run.status].color} className={run.status === "running" ? css.pulse : undefined} />
        <span className={"ellipsis " + css.medium}>{run.pipeline}</span>
        <span className="caption muted num">#{run.id}</span>
        <span className="grow" />
        {isAI(run.triggeredBy) && <AIBadge />}
        {run.status === "failed" && <button className="btn small" onClick={(e) => { e.stopPropagation(); upd("retry"); }}>重試</button>}
        {(run.status === "running" || run.status === "queued") && <button className="btn small" onClick={(e) => { e.stopPropagation(); upd("cancel"); }}>取消</button>}
      </div>
      <div className="caption muted ellipsis">{run.commitMessage}</div>
      <div className={"row " + css.stages}>
        {run.stages.map((s) => <span key={s.id} title={`${s.name}:${RUN[s.status].label}`} className={css.stage} style={{ background: `color-mix(in srgb, ${color(RUN[s.status].color)} ${s.status === "queued" ? 25 : 85}%, transparent)` }} />)}
      </div>
      <div className={"row gap6 muted " + css.xs}>
        <SIcon name="arrow.triangle.branch" size={10} /><span className="ellipsis">{run.branch}</span><span className="mono">{run.commitSHA.slice(0, 7)}</span>
        <span className="grow" />
        <span className="num">{run.status === "running" ? `${Math.trunc(progress * 100)}%` : run.duration != null ? dur(run.duration) : relative(run.startedAt)}</span>
      </div>
      {run.status === "failed" && run.aiDiagnosis && <div className={"caption muted row gap4 " + css.top}><SIcon name="sparkles" color="purple" size={12} /><span className={css.clamp2}>{run.aiDiagnosis}</span></div>}
      {toastEl}
    </div>
  );
}

function DeployRow({ d, first }: { d: Deployment; first: boolean }) {
  const o = useOmixor();
  const { run, toastEl } = useAct();
  const menu = useContextMenu();
  return (
    <div className={"col " + css.runRow + (first ? "" : " " + css.sepTop)} onClick={canGo("pipelines") ? () => o.go("pipelines", d.id) : undefined}
      onContextMenu={menu.open([
        ...(canGo("pipelines") ? [{ label: "在 CI/CD 開啟", onClick: () => o.go("pipelines", d.id) }] : []),
        ...(d.status === "succeeded" || d.status === "failed" ? [{ label: `回滾到 ${d.previousVersion}`, onClick: () => run(() => action("deployment_rollback", { id: d.id })) }] : []),
      ])}>
      <div className="row gap6">
        <SIcon name={RUN[d.status].icon} color={RUN[d.status].color} />
        <span className={"ellipsis " + css.medium}>{o.service(d.service)?.name ?? d.service}</span>
        <span className="caption mono muted">{d.version}</span>
        <span className="grow" />
        {d.status === "waitingApproval" ? <button className="btn small primary" onClick={(e) => { e.stopPropagation(); run(() => action("deployment_approve", { id: d.id })); }}>核准部署</button> : <RunStatusBadge s={d.status} />}
      </div>
      {d.status === "running" && <ProgressBar value={d.progress} showLabel={false} />}
      <div className={"row gap6 muted " + css.xs}>
        <ActorAvatar plainAI actor={d.by} size={14} /><span className="ellipsis">{o.name(d.by)}</span>·<span>{d.strategy}</span>
        {d.canaryPercent != null && d.status === "running" && <span>canary {d.canaryPercent}%</span>}
        <span className="grow" />{relative(d.startedAt)}
      </div>
      {menu.el}{toastEl}
    </div>
  );
}

// MARK: - Sprint

function SprintCard({ d }: { d: Data }) {
  const o = useOmixor();
  const sp = d.sprint;
  const total = d.work.reduce((a, w) => a + w.points, 0);
  const done = d.work.filter((w) => w.status === "done").reduce((a, w) => a + w.points, 0);
  const daysLeft = sp ? Math.max(0, Math.floor((t(sp.end) - now()) / 86400000)) : 0;
  const span = sp ? t(sp.end) - t(sp.start) : 0;
  const timeProgress = span > 0 ? Math.min(1, Math.max(0, (now() - t(sp!.start)) / span)) : 0;
  const progress = total > 0 ? done / total : 0;
  const behind = total > 0 && progress + 0.1 < timeProgress;
  const waitingPRs = d.prs.filter((p) => p.status === "open").length;
  const aiItems = d.work.filter((w) => w.createdByAI).length;
  return (
    <Card>
      <CardHeader title={sp?.name || "Sprint"} linkTitle="開發進度" onLink={canGo("development") ? () => o.go("development") : undefined} />
      {d.work.length === 0 ? <InlineEmpty icon="list.bullet.rectangle" text="這個 Sprint 還沒有工作項目" /> : (
        <>
          {sp?.goal && <div className={"caption muted " + css.clamp2}>{sp.goal}</div>}
          <div className={"row gap6 " + css.baseline}>
            <span className={"num " + css.big}>{done}</span>
            <span className="muted">/ {total} 點</span>
            <span className="grow" />
            <span className="caption muted">{daysLeft > 0 ? `剩 ${daysLeft} 天` : "今天結束"}</span>
          </div>
          <Bar value={progress} c={behind ? "orange" : "green"} />
          {behind && <div className={"caption row gap4 " + css.orange}><SIcon name="tortoise" size={12} />進度落後時程 {Math.trunc((timeProgress - progress) * 100)}%</div>}
          {sp && sp.burndown.length > 1 && <div title="燃盡圖(剩餘點數)"><Sparkline values={sp.burndown.map((p) => p.value)} c="indigo" height={34} /></div>}
          <div className={"row " + css.gap10}>
            {(["todo", "inProgress", "review", "done"] as const).map((st) => (
              <div key={st} className={"col grow " + css.gap1}>
                <span className={"num " + css.bold}>{d.work.filter((w) => w.status === st).length}</span>
                <span className={"muted " + css.xs}>{WORK_STATUS[st].label}</span>
              </div>
            ))}
          </div>
          {(waitingPRs > 0 || aiItems > 0) && (
            <div className="row gap8 wrap">
              {waitingPRs > 0 && <Pill text={`${waitingPRs} 個 PR 待審`} color="blue" icon="arrow.triangle.pull" />}
              {aiItems > 0 && <Pill text={`${aiItems} 項由 AI 建立`} color="purple" icon="sparkles" />}
            </div>
          )}
        </>
      )}
    </Card>
  );
}

// MARK: - 錯誤預算

function ErrorBudgetCard({ d }: { d: Data }) {
  const o = useOmixor();
  const s = d.services;
  const c = (v: number) => (v < 0.2 ? "red" : v < 0.5 ? "orange" : "green");
  const tier1 = s.filter((x) => x.tier === 1);
  const basis = tier1.length ? tier1 : s;
  const overall = basis.length ? basis.reduce((a, x) => a + x.errorBudgetRemaining, 0) / basis.length : 0;
  const lowest = [...s].sort((a, b) => a.errorBudgetRemaining - b.errorBudgetRemaining).slice(0, 4);
  const R = 37.5, C = 2 * Math.PI * R;
  return (
    <Card>
      <CardHeader title="錯誤預算" linkTitle="SLO" onLink={canGo("monitoring") ? () => o.go("monitoring") : undefined} />
      {s.length === 0 ? <InlineEmpty icon="gauge.with.dots.needle.33percent" text="沒有設定 SLO 的服務" /> : (
        <>
          <div className={"row " + css.gap16}>
            <div className={css.donut}>
              <svg width={84} height={84} viewBox="0 0 84 84">
                <circle cx={42} cy={42} r={R} fill="none" stroke="color-mix(in srgb, var(--fg) 8%, transparent)" strokeWidth={9} />
                <circle cx={42} cy={42} r={R} fill="none" stroke={color(c(overall))} strokeWidth={9} strokeLinecap="round"
                  strokeDasharray={`${C * Math.min(1, Math.max(0, overall))} ${C}`} transform="rotate(-90 42 42)" />
              </svg>
              <div className={"col " + css.donutLabel}>
                <span className={"num " + css.bigXl}>{Math.round(overall * 100)}%</span>
                <span className={"muted " + css.xs}>剩餘</span>
              </div>
            </div>
            <div className="col gap4">
              <span className="caption muted">{tier1.length ? "第 1 級服務平均" : "所有服務平均"}</span>
              <span>{overall < 0.2 ? "預算即將用完,建議暫停高風險部署" : overall < 0.5 ? "預算消耗偏快,留意變更風險" : "預算充足,可正常發版"}</span>
            </div>
          </div>
          <div className="col gap6">
            {lowest.map((x) => (
              <button key={x.id} className={plainClass + " row gap8"} onClick={canGo("infrastructure") ? () => o.go("infrastructure", x.id) : undefined}>
                <span className={"caption ellipsis " + css.budgetName}>{x.name}</span>
                <Bar value={x.errorBudgetRemaining} c={c(x.errorBudgetRemaining)} />
                <span className={"caption muted num " + css.budgetPct}>{Math.round(x.errorBudgetRemaining * 100)}%</span>
              </button>
            ))}
          </div>
        </>
      )}
    </Card>
  );
}

// MARK: - 動態牆

function ActivityCard({ d }: { d: Data }) {
  const o = useOmixor();
  const [filter, setFilter] = useState<"all" | "people" | "ai">("all");
  const [showAll, setShowAll] = useState(false);
  const list = d.activity.filter((i) => filter === "all" || (filter === "people" ? i.actor.kind === "person" : i.actor.kind === "ai"));
  const shown = list.slice(0, showAll ? 40 : 8);
  const titles: Record<string, string> = { overview: "總覽", development: "開發進度", pipelines: "CI/CD", infrastructure: "伺服器與服務", monitoring: "監控與告警", incidents: "事件", automation: "AI 自動化", cabinet: "文件櫃", settings: "設定" };
  return (
    <Card>
      <div className="row">
        <span className={cardTitleClass}>最近動態</span>
        <span className="grow" />
        <div className={css.w180}><Segmented value={filter} onChange={setFilter} options={[{ value: "all", label: "全部" }, { value: "people", label: "人員" }, { value: "ai", label: "AI" }]} /></div>
      </div>
      {shown.length === 0 ? <InlineEmpty icon="clock.arrow.circlepath" text="還沒有任何動態" /> : (
        <>
          <div className="col">
            {shown.map((item, i) => {
              const ai = item.actor.kind === "ai";
              return (
                <button key={item.id} className={plainClass + " row gap8 " + css.actRow + (i ? " " + css.sepTop : "")}
                  title={`前往「${titles[item.section] ?? item.section}」`} onClick={canGo(item.section) ? () => o.go(item.section) : undefined}>
                  <ActorAvatar plainAI actor={item.actor} size={24} />
                  <div className={"col grow " + css.gap2}>
                    <div className="row gap6">
                      <span className={"ellipsis " + css.medium}>{o.name(item.actor)}</span>
                      {ai && <AIBadge />}
                      <span className="grow" />
                      <span className={"caption muted " + css.none}>{relative(item.date)}</span>
                    </div>
                    <div className={"caption muted row gap4 " + css.top}><SIcon name={item.icon} color={ai ? "purple" : "secondary"} size={12} /><span>{item.text}</span></div>
                  </div>
                </button>
              );
            })}
          </div>
          {list.length > 8 && <button className={"btn link caption " + css.selfStart} onClick={() => setShowAll(!showAll)}>{showAll ? "顯示較少" : `顯示更多(共 ${list.length} 筆)`}</button>}
        </>
      )}
    </Card>
  );
}

