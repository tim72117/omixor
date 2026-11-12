// 服務分頁:左邊清單、右邊詳細資料(Swift 的 InfraServicesTab.swift + InfraServiceDetail.swift)
import { useEffect, useState } from "react";
import { tool } from "../../api";
import { Card, EmptyState, Modal, Pill } from "../../components/ui";
import {
  ActorLabel, ALERT_STATE, ENV_ORDER, EnvBadge, HEALTH, HEALTH_ORDER, HealthBadge, healthRank, MenuButton, Metric, num, pct, ProgressBar,
  RunStatusBadge, SearchField, SeriesChart, SEVERITY, SIcon, StatusDot, t, TIME_RANGE, UsageBar, useAct, useConfirm, useList, useOps, useWidth,
  type Alert, type Deployment, type EnvKey, type HealthKey, type Server, type Service,
} from "../../components/opsKit";
import { relative } from "../../fmt";
import { budgetColor, BudgetBar, Chip, minutes, SectionHeader, synthSeries, TierPill } from "./infraShared";

type Sort = "attention" | "tier" | "name" | "traffic";
const SORTS: { v: Sort; label: string }[] = [{ v: "attention", label: "需要注意的優先" }, { v: "tier", label: "關鍵等級" }, { v: "name", label: "名稱" }, { v: "traffic", label: "流量" }];

export function InfraServicesTab({ selection, setSelection, servers, onOpenServer }: { selection?: string; setSelection: (id?: string) => void; servers: Server[]; onOpenServer: (id: string) => void }) {
  const o = useOps();
  const [search, setSearch] = useState("");
  const [health, setHealth] = useState<HealthKey>();
  const [sort, setSort] = useState<Sort>("attention");
  const [ref, w] = useWidth<HTMLDivElement>();
  const q = search.trim().toLowerCase();
  let list = o.services.filter((s) => (!health || s.health === health) && (!q || [s.name, s.team, s.summary, s.id].some((f) => f.toLowerCase().includes(q))));
  list = [...list].sort((a, b) => {
    switch (sort) {
      case "attention": return healthRank(a.health) - healthRank(b.health) || a.errorBudgetRemaining - b.errorBudgetRemaining || a.name.localeCompare(b.name);
      case "tier": return a.tier - b.tier || a.name.localeCompare(b.name);
      case "name": return a.name.localeCompare(b.name, undefined, { numeric: true });
      default: return b.rps - a.rps;
    }
  });
  useEffect(() => {
    if (selection && o.service(selection)) return;
    const first = list[0]?.id ?? o.services[0]?.id;
    if (first) setSelection(first);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [o.services.length]);
  if (o.services.length === 0) return <EmptyState title="還沒有服務" icon="square.stack.3d.up.slash" message="接上服務目錄後,這裡會列出所有服務的健康狀態與 SLO。" />;
  const sel = selection ? o.service(selection) : undefined;
  return (
    <div ref={ref} className="ops-fill">
      <div className="col" style={{ width: Math.min(400, Math.max(230, w * 0.32)), flex: "none", borderRight: "1px solid var(--line)", minHeight: 0 }}>
        <div className="row gap6" style={{ padding: 10 }}>
          <SearchField value={search} onChange={setSearch} placeholder="搜尋服務、團隊" style={{ flex: 1 }} />
          <MenuButton className="ops-icon-btn" title="篩選與排序" align="right" label={<SIcon name={health ? "line.3.horizontal.decrease.circle.fill" : "line.3.horizontal.decrease.circle"} size={16} color={health ? "var(--accent)" : undefined} />} items={[
            { header: "健康狀態" },
            { label: "全部狀態", checked: !health, onClick: () => setHealth(undefined) },
            ...HEALTH_ORDER.map((h) => ({ label: <><SIcon name={HEALTH[h].icon} color={HEALTH[h].color} size={12} />&nbsp;{HEALTH[h].label}</>, checked: health === h, onClick: () => setHealth(h) })),
            "sep", { header: "排序" },
            ...SORTS.map((s) => ({ label: s.label, checked: sort === s.v, onClick: () => setSort(s.v) })),
          ]} />
        </div>
        <div className="divider" />
        {list.length === 0 ? <EmptyState title="沒有符合的服務" icon="magnifyingglass" message="換個關鍵字或清除篩選。" /> : (
          <div className="ops-list">
            {list.map((s) => (
              <div key={s.id} className={"ops-list-row row" + (s.id === selection ? " on" : "")} style={{ gap: 10, padding: "8px 12px" }} onClick={() => setSelection(s.id)}>
                <span title={HEALTH[s.health].label}><SIcon name={HEALTH[s.health].icon} color={HEALTH[s.health].color} size={15} /></span>
                <div className="col grow" style={{ gap: 3, minWidth: 0 }}>
                  <span className="row gap6"><span className="ellipsis" style={{ fontWeight: 500 }}>{s.name}</span><TierPill tier={s.tier} /></span>
                  <span className="caption muted num ellipsis">p95 {Math.trunc(s.latencyP95)}ms&nbsp;&nbsp;<span style={{ color: s.errorRate >= 1 ? "var(--red)" : undefined }}>錯誤 {pct(s.errorRate, 2)}</span>&nbsp;&nbsp;{num(s.rps)} rps</span>
                </div>
                <div className="col" style={{ alignItems: "flex-end", gap: 3 }} title={`錯誤預算剩餘 ${Math.trunc(s.errorBudgetRemaining * 100)}%`}>
                  <span className="caption num" style={{ color: `var(--${budgetColor(s.errorBudgetRemaining)})` }}>{Math.trunc(s.errorBudgetRemaining * 100)}%</span>
                  <BudgetBar remaining={s.errorBudgetRemaining} height={4} width={40} />
                </div>
              </div>
            ))}
          </div>
        )}
        <div className="ops-list-foot">
          <span>{list.length} / {o.services.length} 個服務</span><span className="grow" />
          {health && <button className="btn link caption" onClick={() => setHealth(undefined)}>清除篩選:{HEALTH[health].label}</button>}
        </div>
      </div>
      <div className="ops-scroll" style={{ position: "relative" }}>
        {sel ? <ServiceDetail key={sel.id} s={sel} servers={servers} onSelectService={setSelection} onOpenServer={onOpenServer} />
          : <EmptyState title="選擇一個服務" icon="square.stack.3d.up" message="從左邊清單選一個服務,查看 SLO、延遲、相依關係與最近部署。" />}
      </div>
    </div>
  );
}

function ServiceDetail({ s, servers, onSelectService, onOpenServer }: { s: Service; servers: Server[]; onSelectService: (id: string) => void; onOpenServer: (id: string) => void }) {
  const o = useOps();
  const [deps] = useList<Deployment>("deployments");
  const [alerts] = useList<Alert>("alerts");
  const [showScale, setShowScale] = useState(false);
  const [replicas, setReplicas] = useState(Math.max(1, s.hostIds.length * 2));
  const [dlg, ask] = useConfirm();
  const { run, toastEl } = useAct();
  const env = (o.nav?.env ?? "production") as EnvKey;
  const tr = TIME_RANGE[o.nav?.timeRange ?? "24h"] ?? TIME_RANGE["24h"];
  const span = Math.min(tr.seconds, 7 * 86400);
  const lat = synthSeries(s.id + "-lat", s.latencyP95, span, 48, 0.18);
  const err = synthSeries(s.id + "-err", s.errorRate, span, 48, 0.5);
  const rps = synthSeries(s.id + "-rps", s.rps, span, 48, 0.25);
  const meets = s.uptime30d >= s.sloTarget;
  const post = (icon: string, text: string, ok: string) => run(() => tool("ops_activity_post", { icon, text, section: "infrastructure" }), ok);

  const win = 30 * 24 * 60;
  const allowed = ((100 - s.sloTarget) / 100) * win;
  const used = Math.max(0, ((100 - s.uptime30d) / 100) * win);
  const rem = s.errorBudgetRemaining;
  const [stText, stColor, stIcon] = rem < 0.2 ? ["錯誤預算快用完了,建議暫停非必要部署", "red", "exclamationmark.octagon.fill"]
    : rem < 0.5 ? ["預算消耗偏快,留意部署節奏", "orange", "exclamationmark.triangle.fill"] : ["預算充足,可以照常部署", "green", "checkmark.circle.fill"];
  const dependents = o.services.filter((x) => x.dependencies.includes(s.id));
  const hosts = s.hostIds.map((id) => servers.find((x) => x.id === id)).filter((x): x is Server => !!x);
  const recent = deps.filter((d) => d.service === s.id).sort((a, b) => t(b.startedAt) - t(a.startedAt)).slice(0, 6);
  const svcAlerts = alerts.filter((a) => a.service === s.id && (a.state === "firing" || a.state === "acknowledged"))
    .sort((a, b) => SEVERITY[a.severity].rank - SEVERITY[b.severity].rank || t(b.startedAt) - t(a.startedAt));
  const depChip = (id: string) => {
    const x = o.service(id);
    return x ? <Chip key={id} text={x.name} c={HEALTH[x.health].color} icon={HEALTH[x.health].icon} title={`${x.name}:${HEALTH[x.health].label}`} onClick={() => onSelectService(x.id)} />
      : <Chip key={id} text={id} c="gray" icon="questionmark.circle" title="服務目錄裡找不到這個服務" />;
  };
  const stat = (title: string, v: string) => <div className="col" style={{ gap: 2 }}><span className="caption muted">{title}</span><span className="num" style={{ fontSize: 15, fontWeight: 500 }}>{v}</span></div>;
  const prod = s.versions?.production, stg = s.versions?.staging;
  return (
    <div className="col" style={{ padding: 18, gap: 14 }}>
      <Card>
        <div className="row gap8 wrap" style={{ alignItems: "baseline" }}>
          <span className="h3" style={{ userSelect: "text" }}>{s.name}</span><HealthBadge h={s.health} /><TierPill tier={s.tier} />
        </div>
        {s.summary && <div className="muted">{s.summary}</div>}
        <div className="row wrap muted" style={{ gap: 14 }}>
          <span className="row gap4"><SIcon name="person.3" size={12} />{s.team}</span>
          <ActorLabel actor={{ kind: "person", id: s.owner }} />
          <span className="row gap4"><SIcon name="chevron.left.forwardslash.chevron.right" size={12} />{s.language}</span>
          <button className="btn link row gap4" title="到開發進度查看儲存庫" onClick={() => o.go("development", s.repo)}><SIcon name="book.closed" size={12} />{s.repo}</button>
        </div>
        <div className="row wrap" style={{ gap: 10 }}>
          {ENV_ORDER.map((e) => <span key={e} className="row" style={{ gap: 5 }}><EnvBadge env={e} /><span className="mono" style={{ color: e === env ? undefined : "var(--fg2)" }}>{s.versions?.[e] ?? "—"}</span></span>)}
          {prod && stg && stg !== prod && <Pill text="Staging 有未上線版本" color="orange" icon="arrow.up.circle" />}
        </div>
        <div className="divider" />
        <div className="row gap8 wrap">
          <button className="btn" onClick={() => o.go("monitoring", s.id)}><SIcon name="doc.text.magnifyingglass" size={12} />查看日誌與指標</button>
          <button className="btn" onClick={() => ask({
            title: `要重新啟動 ${s.name} 嗎?`, message: `會逐台重新啟動 ${s.hostIds.length} 台主機上的執行個體,過程中流量會先導到其他主機。`,
            buttons: [{ label: "滾動重新啟動", danger: true, onClick: () => post("arrow.clockwise", `滾動重新啟動 ${s.name}(${s.hostIds.length} 台主機)`, `已送出 ${s.name} 的滾動重新啟動`) }],
          })}><SIcon name="arrow.clockwise" size={12} />重新啟動</button>
          <button className="btn" onClick={() => setShowScale(true)}><SIcon name="arrow.up.left.and.arrow.down.right" size={12} />調整副本數</button>
          <button className="btn" onClick={() => o.go("pipelines", s.id)}><SIcon name="arrow.triangle.branch" size={12} />部署紀錄</button>
        </div>
      </Card>
      <div className="ops-grid-auto" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(150px, 1fr))" }}>
        <Metric title="可用率(30 天)" value={pct(s.uptime30d, 2)} delta={`目標 ${pct(s.sloTarget, 2)}`} deltaGood={meets} icon="checkmark.shield" tint={meets ? "green" : "red"} />
        <Metric title="p95 延遲" value={`${Math.trunc(s.latencyP95)} ms`} icon="timer" tint="blue" series={lat.map((p) => p.value)} />
        <Metric title="錯誤率" value={pct(s.errorRate, 2)} icon="exclamationmark.bubble" tint={s.errorRate >= 1 ? "red" : "orange"} series={err.map((p) => p.value)} />
        <Metric title="流量" value={`${num(s.rps)} rps`} icon="arrow.left.arrow.right" tint="teal" series={rps.map((p) => p.value)} />
      </div>
      <Card title="SLO 與錯誤預算" subtitle={`目標 ${pct(s.sloTarget, 2)} · 30 天滾動視窗`}>
        <div className="row" style={{ gap: 24 }}>{stat("實際可用率", pct(s.uptime30d, 3))}{stat("允許停機", minutes(allowed))}{stat("已用掉", minutes(used))}</div>
        <div className="col" style={{ gap: 5 }}>
          <div className="row"><span className="caption muted">錯誤預算剩餘</span><span className="grow" /><span className="num" style={{ fontWeight: 600, color: `var(--${budgetColor(rem)})` }}>{Math.trunc(rem * 100)}%</span></div>
          <BudgetBar remaining={rem} height={8} />
        </div>
        <div className="row gap6" style={{ color: `var(--${stColor})` }}><SIcon name={stIcon} color={stColor} />{stText}</div>
      </Card>
      <div className="ops-grid-auto" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(300px, 1fr))" }}>
        <Card title="p95 延遲" subtitle={`最近 ${tr.label} · ms`}><SeriesChart series={[{ name: "p95", points: lat }]} height={150} yFormat={(v) => v.toFixed(0)} /></Card>
        <Card title="錯誤率" subtitle={`最近 ${tr.label} · %`}><SeriesChart series={[{ name: "錯誤率", points: err }]} height={150} threshold={1} yFormat={(v) => v.toFixed(2)} /></Card>
      </div>
      <Card>
        <SectionHeader title="相依關係" trailing={`依賴 ${s.dependencies.length} · 被依賴 ${dependents.length}`} />
        <div className="col gap6"><span className="caption muted">依賴的服務</span>
          {s.dependencies.length === 0 ? <span className="faint">沒有依賴其他服務</span> : <div className="row gap6 wrap">{s.dependencies.map(depChip)}</div>}
        </div>
        <div className="col gap6"><span className="caption muted">呼叫這個服務的</span>
          {dependents.length === 0 ? <span className="faint">沒有其他服務依賴它</span> : <div className="row gap6 wrap">{dependents.map((d) => depChip(d.id))}</div>}
        </div>
      </Card>
      <Card>
        <SectionHeader title="執行中的主機" trailing={`${hosts.length} 台`} />
        {hosts.length === 0 ? <span className="faint">目前沒有登記的主機</span> : hosts.map((h, i) => (
          <button key={h.id} className="ops-plain row" style={{ gap: 10, padding: "4px 0", borderTop: i ? "1px solid var(--line)" : undefined }} onClick={() => onOpenServer(h.id)}>
            <StatusDot c={HEALTH[h.health].color} pulsing={h.health === "down"} />
            <div className="col" style={{ gap: 1, minWidth: 120 }}><span className="ellipsis" style={{ fontWeight: 500 }}>{h.name}</span><span className="caption mono muted ellipsis">{h.region} · {h.ip}</span></div>
            <EnvBadge env={h.env} />
            <span className="grow" />
            <div className="col" style={{ gap: 3, width: 180 }}><UsageBar value={h.cpu} label="CPU" /><UsageBar value={h.memory} label="RAM" /></div>
            <SIcon name="chevron.right" size={11} className="faint" />
          </button>
        ))}
      </Card>
      <Card>
        <SectionHeader title="最近部署" trailing={recent.length ? `最近 ${recent.length} 筆` : undefined} />
        {recent.length === 0 ? <span className="faint">這個服務最近沒有部署</span> : recent.map((d, i) => (
          <button key={d.id} className="ops-plain col" title="到 CI/CD 查看這次部署" style={{ gap: 5, padding: "4px 0", borderTop: i ? "1px solid var(--line)" : undefined }} onClick={() => o.go("pipelines", d.id)}>
            <div className="row gap8">
              <RunStatusBadge s={d.status} /><EnvBadge env={d.env} />
              <span className="mono ellipsis">{d.previousVersion} → {d.version}</span>
              <Pill text={d.canaryPercent != null ? `${d.strategy} ${d.canaryPercent}%` : d.strategy} />
              <span className="grow" />
              <ActorLabel actor={d.by} className="caption" /><span className="caption muted" style={{ flex: "none" }}>{relative(d.startedAt)}</span>
            </div>
            {d.status === "running" && <ProgressBar value={d.progress} showLabel={false} />}
            {d.changes[0] && <span className="caption muted ellipsis">{d.changes.length > 1 ? `${d.changes[0]} 等 ${d.changes.length} 項變更` : d.changes[0]}</span>}
          </button>
        ))}
      </Card>
      <Card>
        <SectionHeader title="相關告警" trailing={svcAlerts.length ? `${svcAlerts.length} 則進行中` : undefined} />
        {svcAlerts.length === 0 ? <span className="row gap4" style={{ color: "var(--green)" }}><SIcon name="checkmark.circle" color="green" />沒有進行中的告警</span> : svcAlerts.map((a, i) => (
          <button key={a.id} className="ops-plain row gap8" title="到監控與告警查看" style={{ alignItems: "flex-start", padding: "4px 0", borderTop: i ? "1px solid var(--line)" : undefined }} onClick={() => o.go("monitoring", a.id)}>
            <Pill text={SEVERITY[a.severity].label} color={SEVERITY[a.severity].color} />
            <div className="col grow" style={{ gap: 2, minWidth: 0 }}><span style={{ fontWeight: 500 }}>{a.title}</span><span className="caption mono muted ellipsis">{a.rule} · 目前 {a.value}</span></div>
            <div className="col" style={{ alignItems: "flex-end", gap: 2 }}>
              <span className="caption" style={{ fontWeight: 500, color: a.state === "firing" ? "var(--red)" : "var(--fg2)" }}>{ALERT_STATE[a.state]}</span>
              <span className="caption muted">{relative(a.startedAt)}</span>
            </div>
          </button>
        ))}
      </Card>
      <Modal open={showScale} onClose={() => setShowScale(false)} width={300}>
        <div className="card-title">調整 {s.name} 副本數</div>
        <div className="row gap8"><span>副本數</span><span className="grow" />
          <button className="btn small" disabled={replicas <= 1} onClick={() => setReplicas(replicas - 1)}>−</button>
          <span className="num" style={{ fontSize: 16, fontWeight: 600, minWidth: 28, textAlign: "center" }}>{replicas}</span>
          <button className="btn small" disabled={replicas >= 64} onClick={() => setReplicas(replicas + 1)}>+</button>
        </div>
        <div className="caption muted">目前分佈在 {s.hostIds.length} 台主機。調整後會依負載自動排程。</div>
        <div className="row gap8" style={{ justifyContent: "flex-end" }}>
          <button className="btn" onClick={() => setShowScale(false)}>取消</button>
          <button className="btn primary" onClick={() => { setShowScale(false); post("arrow.up.left.and.arrow.down.right", `把 ${s.name} 調整為 ${replicas} 個副本`, `${s.name} 已調整為 ${replicas} 個副本`); }}>套用</button>
        </div>
      </Modal>
      {dlg}{toastEl}
    </div>
  );
}
