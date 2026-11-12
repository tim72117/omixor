// CI/CD › 部署:版本變化、策略、進度、健康檢查,可核准與回滾(Swift 的 PipeDeploymentsTab.swift)
import { useEffect, useRef, useState } from "react";
import { action } from "../../api";
import { Card, EmptyState, Pill } from "../../components/ui";
import {
  ActorLabel, CHECK, dur, ENV, ENV_ORDER, EnvBadge, Metric, now, ProgressBar, RunStatusBadge, Seg, SIcon, t, useAct, useConfirm,
  useList, useOps, Check, type Deployment, type EnvKey,
} from "../../components/opsKit";
import { relative } from "../../fmt";
import { CHECK_LABEL, depIsActive, depIsRollback, StatusIcon } from "./pipeShared";

export function PipeDeploymentsTab({ highlight, setHighlight }: { highlight?: string; setHighlight: (id?: string) => void }) {
  const o = useOps();
  const [deps, err] = useList<Deployment>("deployments");
  const [envFilter, setEnvFilter] = useState<EnvKey | "">((o.nav?.env as EnvKey) ?? "");
  const [follow, setFollow] = useState(true);
  const [activeOnly, setActiveOnly] = useState(false);
  const [serviceFilter, setServiceFilter] = useState("");
  const [dlg, ask] = useConfirm();
  const { run, toastEl } = useAct();
  const rowRefs = useRef<Record<string, HTMLDivElement | null>>({});

  useEffect(() => { if (follow && o.nav?.env) setEnvFilter(o.nav.env as EnvKey); }, [o.nav?.env, follow]);
  useEffect(() => {
    if (!highlight) return;
    const d = deps.find((x) => x.id === highlight);
    if (!d) return;
    if (envFilter && envFilter !== d.env) { setEnvFilter(""); setFollow(false); }
    if (activeOnly && !depIsActive(d)) setActiveOnly(false);
    setServiceFilter("");
    const a = window.setTimeout(() => rowRefs.current[highlight]?.scrollIntoView({ behavior: "smooth", block: "center" }), 150);
    const b = window.setTimeout(() => setHighlight(undefined), 3000);
    return () => { window.clearTimeout(a); window.clearTimeout(b); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [highlight, deps.length]);

  const rank = (d: Deployment) => (d.status === "waitingApproval" ? 0 : depIsActive(d) ? 1 : 2);
  const list = deps.filter((d) => (!envFilter || d.env === envFilter) && (!activeOnly || depIsActive(d)) && (!serviceFilter || d.service === serviceFilter))
    .sort((a, b) => rank(a) - rank(b) || t(b.startedAt) - t(a.startedAt));
  const svcName = (id: string) => o.service(id)?.name ?? id;

  const perform = (kind: "approve" | "rollback", d: Deployment) =>
    run(() => action(kind === "approve" ? "deployment_approve" : "deployment_rollback", { id: d.id }));
  const request = (kind: "approve" | "rollback", d: Deployment) => {
    // 正式環境一律先確認;其他環境直接執行
    if (d.env !== "production") return perform(kind, d);
    const name = svcName(d.service);
    ask(kind === "approve" ? {
      title: "核准部署到 Production?", message: `${name} ${d.previousVersion} → ${d.version},以 ${d.strategy} 方式部署,會影響所有正式使用者。`,
      buttons: [{ label: "核准部署", primary: true, onClick: () => perform(kind, d) }],
    } : {
      title: "回滾 Production?", message: `${name} 會從 ${d.version} 回到 ${d.previousVersion}。回滾期間部分請求可能由舊版處理。`,
      buttons: [{ label: `回滾到 ${d.previousVersion}`, danger: true, onClick: () => perform(kind, d) }],
    });
  };

  const weekAgo = now() - 7 * 86400000;
  const serviceIds = [...new Set(deps.map((d) => d.service))].sort();
  return (
    <div className="page-scroll">
      <div className="page-inner">
        <div className="ops-grid-auto" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))" }}>
          <Metric title="部署中" value={String(deps.filter((d) => d.status === "running" || d.status === "queued").length)} icon="arrow.triangle.2.circlepath" tint="blue" />
          <Metric title="等待核准" value={String(deps.filter((d) => d.status === "waitingApproval").length)} icon="hand.raised.fill" tint="purple" />
          <Metric title="7 天內成功" value={String(deps.filter((d) => d.status === "succeeded" && t(d.startedAt) > weekAgo).length)} icon="checkmark.circle.fill" tint="green" />
          <Metric title="7 天內失敗或回滾" value={String(deps.filter((d) => (d.status === "failed" || depIsRollback(d)) && t(d.startedAt) > weekAgo).length)} icon="arrow.uturn.backward.circle" tint="red" />
        </div>
        <div className="row gap12 wrap">
          <Seg value={envFilter} onChange={(v) => { setEnvFilter(v); setFollow(false); }} options={[{ value: "" as EnvKey | "", label: "全部" }, ...ENV_ORDER.map((e) => ({ value: e as EnvKey | "", label: ENV[e].label }))]} />
          <select className="select" value={serviceFilter} onChange={(e) => setServiceFilter(e.target.value)}>
            <option value="">所有服務</option><option disabled>──────</option>
            {serviceIds.map((s) => <option key={s} value={s}>{svcName(s)}</option>)}
          </select>
          <Check on={activeOnly} onChange={setActiveOnly}>只看進行中</Check>
          <span className="grow" />
          {!follow && <button className="btn link caption" onClick={() => { setFollow(true); setEnvFilter((o.nav?.env as EnvKey) ?? ""); }}>跟隨工具列環境</button>}
        </div>
        {list.length === 0 ? (
          <Card><div style={{ minHeight: 220, display: "flex" }}><EmptyState title={err && !deps.length ? "讀不到部署紀錄" : deps.length ? "沒有符合的部署" : "還沒有部署紀錄"} icon="shippingbox" message={err && !deps.length ? err : envFilter ? `目前只顯示 ${ENV[envFilter].label}` : undefined} /></div></Card>
        ) : (
          <Card style={{ padding: 0, gap: 0 }}>
            {list.map((d, i) => (
              <div key={d.id} ref={(el) => { rowRefs.current[d.id] = el; }} style={{ borderTop: i ? "1px solid var(--line)" : undefined }}>
                <DeploymentRow d={d} highlighted={d.id === highlight} onApprove={() => request("approve", d)} onRollback={() => request("rollback", d)} />
              </div>
            ))}
          </Card>
        )}
      </div>
      {dlg}{toastEl}
    </div>
  );
}

const STRATEGY_ICON: Record<string, string> = { Rolling: "arrow.triangle.2.circlepath", Canary: "bird", "Blue/Green": "square.split.2x1" };

function DeploymentRow({ d, highlighted, onApprove, onRollback }: { d: Deployment; highlighted: boolean; onApprove: () => void; onRollback: () => void }) {
  const o = useOps();
  const [expanded, setExpanded] = useState(false);
  const canRollback = (d.status === "succeeded" || d.status === "failed") && d.previousVersion !== d.version;
  return (
    <div className="row" style={{ gap: 12, alignItems: "flex-start", padding: "12px 14px", background: highlighted ? "color-mix(in srgb, var(--accent) 12%, transparent)" : undefined, transition: "background .4s" }}>
      <span style={{ width: 24, flex: "none", paddingTop: 2 }}><StatusIcon s={d.status} size={18} /></span>
      <div className="col grow" style={{ gap: 5, minWidth: 0 }}>
        <div className="row gap8 wrap">
          <span className="card-title">{o.service(d.service)?.name ?? d.service}</span>
          <EnvBadge env={d.env} />
          <Pill text={d.strategy} color="blue" icon={STRATEGY_ICON[d.strategy]} />
          {d.canaryPercent != null && (d.strategy === "Canary" || d.canaryPercent > 0) && <Pill text={`流量 ${d.canaryPercent}%`} color="orange" icon="bird" />}
          {depIsRollback(d) && <Pill text="回滾" color="red" icon="arrow.uturn.backward" />}
        </div>
        <div className="row gap6 mono" style={{ fontSize: 12.5, userSelect: "text" }}>
          <span className="muted">{d.previousVersion}</span><SIcon name="arrow.right" size={11} className="faint" /><span style={{ fontWeight: 600 }}>{d.version}</span>
        </div>
        {d.changes.length > 0 && (expanded
          ? <div className="col" style={{ gap: 2 }}>{d.changes.map((c) => <span key={c} className="caption muted">• {c}</span>)}</div>
          : <span className="caption muted ellipsis">{d.changes.join(" · ")}</span>)}
        {d.changes.length > 1 && <button className="btn link caption" style={{ alignSelf: "flex-start", padding: 0 }} onClick={() => setExpanded(!expanded)}>{expanded ? "收起" : `全部 ${d.changes.length} 項變更`}</button>}
      </div>
      <div className="col" style={{ width: 200, flex: "none", alignItems: "flex-end", gap: 6 }}>
        {d.status === "running" || d.status === "queued" ? (
          <div className="col" style={{ alignItems: "flex-end", gap: 3 }}>
            <span className="caption" style={{ color: "var(--blue)", fontWeight: 500 }}>{d.status === "queued" ? "排隊中" : "部署中"}</span>
            <ProgressBar value={d.progress} style={{ width: 180 }} />
          </div>
        ) : d.status === "waitingApproval" ? (
          <div className="col" style={{ alignItems: "flex-end", gap: 3 }}><RunStatusBadge s={d.status} /><ProgressBar value={d.progress} c="purple" style={{ width: 180 }} /></div>
        ) : (
          <div className="row gap6"><RunStatusBadge s={d.status} />{d.finishedAt && <span className="caption muted num">{dur((t(d.finishedAt) - t(d.startedAt)) / 1000)}</span>}</div>
        )}
        <span className="row gap4 caption"><SIcon name={CHECK[d.healthGate]?.icon} color={CHECK[d.healthGate]?.color} size={12} />健康檢查 {CHECK_LABEL[d.healthGate] ?? d.healthGate}</span>
        <span className="row gap4 caption"><ActorLabel actor={d.by} /><span className="muted" style={{ flex: "none" }}>· {relative(d.startedAt)}</span></span>
      </div>
      <div className="col" style={{ width: 84, flex: "none", alignItems: "flex-end", gap: 6 }}>
        {d.status === "waitingApproval" && <button className="btn small purple" onClick={onApprove}><SIcon name="checkmark.seal" size={11} />核准</button>}
        {canRollback && <button className="btn small" title={`回到 ${d.previousVersion}`} onClick={onRollback}><SIcon name="arrow.uturn.backward" size={11} />回滾</button>}
      </div>
    </div>
  );
}
