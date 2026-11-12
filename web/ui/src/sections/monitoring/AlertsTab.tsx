// 告警分頁:依狀態分組(觸發中優先、依嚴重度排序),每列可確認/解除/靜音、建立事件、請 AI 分析(Swift 的 MonAlertsTab)
import { useEffect, useMemo, useRef, useState } from "react";
import { tool, useRun, useView } from "../../api";
import { AICallout, ActorLabel, IconButton, SearchField, Spinner, ToggleChip, copyText, go, hm, shortDuration, useContextMenu, useDirectory, useNow } from "../../components/bits";
import { Icon } from "../../components/Icon";
import {
  ALERT_STATE_COLOR, ALERT_STATE_ICON, ALERT_STATE_LABEL, SEVERITIES, SEV_COLOR, SEV_ICON, SEV_LABEL, arr, sevRank,
  type Alert, type AlertState, type Deployment, type LogEntry, type Service, type Severity,
} from "../../components/models";
import { EmptyState, ErrorText, MetricTile, Pill, color } from "../../components/ui";
import { relative } from "../../fmt";

const STATE_ORDER: AlertState[] = ["firing", "acknowledged", "silenced", "resolved"];

export function AlertsTab({ selectedId, setSelectedId }: { selectedId?: string; setSelectedId: (id?: string) => void }) {
  const [alertsRaw, err] = useView<Alert[]>("alerts");
  const [servicesRaw] = useView<Service[]>("services");
  const [logsRaw] = useView<LogEntry[]>("logs");
  const [deploysRaw] = useView<Deployment[]>("deployments");
  const alerts = arr<Alert>(alertsRaw);
  const services = arr<Service>(servicesRaw);
  const [sevFilter, setSevFilter] = useState<Set<Severity>>(new Set(SEVERITIES));
  const [query, setQuery] = useState("");
  const [collapsed, setCollapsed] = useState<Set<AlertState>>(new Set(["resolved"]));
  const [analyses, setAnalyses] = useState<Record<string, string>>({});
  const [analyzing, setAnalyzing] = useState<Set<string>>(new Set());
  const { error, run } = useRun();
  const dir = useDirectory();
  const svcName = (id: string) => services.find((s) => s.id === id)?.name ?? id;

  // 選中的告警:展開所在分組、加回嚴重度篩選、捲過去
  useEffect(() => {
    const a = alerts.find((x) => x.id === selectedId);
    if (!a) return;
    setCollapsed((c) => { const n = new Set(c); n.delete(a.state); return n; });
    setSevFilter((f) => new Set([...f, a.severity]));
    window.setTimeout(() => document.getElementById("alert-" + a.id)?.scrollIntoView({ behavior: "smooth", block: "center" }), 150);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedId, alertsRaw ? 1 : 0]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return alerts.filter((a) => sevFilter.has(a.severity) && (!q || [a.title, a.service, svcName(a.service), a.rule, a.id, a.value].some((s) => s.toLowerCase().includes(q))));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [alerts, sevFilter, query, services]);

  const firing = alerts.filter((a) => a.state === "firing");

  const analyze = (a: Alert) => {
    if (analyzing.has(a.id)) return;
    setAnalyzing((s) => new Set([...s, a.id]));
    setSelectedId(a.id);
    const text = analyzeAlert(a, services, arr(logsRaw), arr(deploysRaw));
    window.setTimeout(() => {
      setAnalyses((m) => ({ ...m, [a.id]: text }));
      setAnalyzing((s) => { const n = new Set(s); n.delete(a.id); return n; });
    }, 1200);
  };

  const update = (a: Alert, act: "acknowledge" | "resolve" | "silence") => run(() => tool("ops_alert_update", { alert_id: a.id, action: act }));

  const createIncident = (a: Alert) =>
    run(async () => {
      if (a.incidentId) return;
      const sev = a.severity === "critical" ? 1 : a.severity === "warning" ? 2 : 3;
      await tool("ops_incident_declare", {
        title: a.title, sev, impact: "由告警建立,影響範圍評估中。", services: [a.service], alert_ids: [a.id],
        ...(dir.me ? { commander: dir.me } : {}),
      });
      if (a.state === "firing") await tool("ops_alert_update", { alert_id: a.id, action: "acknowledge" });
    });

  if (err) return <EmptyState title="讀不到告警" icon="exclamationmark.triangle" message={err} />;

  return (
    <div className="page-scroll">
      <div className="page-inner">
        <div className="grid g4">
          {SEVERITIES.map((s) => (
            <MetricTile key={s} title={`${SEV_LABEL[s]} 觸發中`} value={firing.filter((a) => a.severity === s).length} icon={SEV_ICON[s]} tint={SEV_COLOR[s]} />
          ))}
          <MetricTile title="已確認 / 已靜音" value={`${alerts.filter((a) => a.state === "acknowledged").length} / ${alerts.filter((a) => a.state === "silenced").length}`} icon="eye" tint="orange" />
        </div>
        <div className="row gap8">
          {SEVERITIES.map((s) => (
            <ToggleChip key={s} title={SEV_LABEL[s]} c={SEV_COLOR[s]} on={sevFilter.has(s)}
              count={alerts.filter((a) => a.severity === s && a.state !== "resolved").length}
              onClick={() => setSevFilter((f) => { const n = new Set(f); if (n.has(s)) n.delete(s); else n.add(s); return n; })} />
          ))}
          <div className="grow" />
          <SearchField value={query} onChange={setQuery} placeholder="搜尋告警、服務、規則" width={260} />
        </div>
        <ErrorText>{error}</ErrorText>
        {alertsRaw === undefined ? <div className="muted">載入中…</div>
          : alerts.length === 0 ? <EmptyState title="沒有告警" icon="bell.slash" message="所有規則都在正常範圍內。" />
          : filtered.length === 0 ? <EmptyState title="沒有符合條件的告警" icon="line.3.horizontal.decrease.circle" message="調整嚴重度或搜尋條件試試看。" />
          : STATE_ORDER.map((state) => {
              const items = filtered.filter((a) => a.state === state)
                .sort((x, y) => sevRank(x.severity) - sevRank(y.severity) || y.startedAt.localeCompare(x.startedAt));
              if (!items.length) return null;
              const open = !collapsed.has(state);
              return (
                <div key={state} className="col gap8">
                  <button className="bx-row-btn" style={{ padding: "2px 0" }}
                    onClick={() => setCollapsed((c) => { const n = new Set(c); if (n.has(state)) n.delete(state); else n.add(state); return n; })}>
                    <span style={{ transform: open ? "rotate(90deg)" : undefined, transition: "transform .15s", display: "inline-flex" }}>
                      <Icon name="chevron.right" size={12} className="muted" />
                    </span>
                    <Icon name={ALERT_STATE_ICON[state]} size={14} color={color(ALERT_STATE_COLOR[state])} />
                    <span className="h3">{ALERT_STATE_LABEL[state]}</span>
                    <span className="muted num">{items.length}</span>
                  </button>
                  {open && items.map((a) => (
                    <AlertRow key={a.id} alert={a} serviceName={svcName(a.service)} selected={selectedId === a.id}
                      analysis={analyses[a.id]} analyzing={analyzing.has(a.id)}
                      onSelect={() => setSelectedId(selectedId === a.id ? undefined : a.id)}
                      onAnalyze={() => analyze(a)} onCreateIncident={() => createIncident(a)} onUpdate={(act) => update(a, act)} />
                  ))}
                </div>
              );
            })}
      </div>
    </div>
  );
}

function AlertRow({ alert: a, serviceName, selected, analysis, analyzing, onSelect, onAnalyze, onCreateIncident, onUpdate }: {
  alert: Alert; serviceName: string; selected: boolean; analysis?: string; analyzing: boolean;
  onSelect: () => void; onAnalyze: () => void; onCreateIncident: () => void; onUpdate: (a: "acknowledge" | "resolve" | "silence") => void;
}) {
  const now = useNow(30000);
  const [hover, setHover] = useState(false);
  const menu = useContextMenu();
  const active = a.state === "firing" || a.state === "acknowledged";
  const sevCol = color(SEV_COLOR[a.severity]);
  const ref = useRef<HTMLDivElement>(null);
  return (
    <div
      id={"alert-" + a.id}
      ref={ref}
      onClick={onSelect}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      onContextMenu={menu.open([
        { label: "確認", onClick: () => onUpdate("acknowledge"), disabled: a.state !== "firing" },
        { label: "解除", onClick: () => onUpdate("resolve"), disabled: a.state === "resolved" },
        { label: "靜音", onClick: () => onUpdate("silence"), disabled: a.state === "silenced" || a.state === "resolved" },
        "-",
        a.incidentId ? { label: `查看事件 ${a.incidentId}`, onClick: () => go("incidents", a.incidentId!) } : { label: "建立事件", onClick: onCreateIncident, disabled: a.state === "resolved" },
        { label: "讓 AI 分析", onClick: onAnalyze },
        "-",
        { label: "前往服務", onClick: () => go("infrastructure", a.service) },
        { label: "複製告警 ID", onClick: () => copyText(a.id) },
      ])}
      style={{
        display: "flex", background: "var(--bg2)", borderRadius: 10, overflow: "hidden",
        border: selected ? "2px solid var(--accent)" : `1px solid color-mix(in srgb, var(--fg) ${hover ? 15 : 7}%, transparent)`,
      }}
    >
      <div style={{ width: 4, flex: "none", background: sevCol, opacity: a.state === "resolved" || a.state === "silenced" ? 0.4 : 1 }} />
      <div className="col gap8 grow" style={{ padding: 12, gap: 10 }}>
        <div className="row gap8">
          <Icon name={SEV_ICON[a.severity]} size={14} color={sevCol} />
          <span style={{ fontWeight: 600, fontSize: 13.5 }}>{a.title}</span>
          <Pill text={SEV_LABEL[a.severity]} color={SEV_COLOR[a.severity]} />
          {a.state !== "firing" && <Pill text={ALERT_STATE_LABEL[a.state]} color={ALERT_STATE_COLOR[a.state]} icon={ALERT_STATE_ICON[a.state]} />}
          <div className="grow" />
          <span className="mono" style={{ fontWeight: 600, fontSize: 13, color: active ? sevCol : "var(--fg2)" }}>{a.value}</span>
          {!selected && (
            <span className="row" style={{ opacity: hover ? 1 : 0.55 }}>
              {a.state === "firing" && <IconButton icon="eye" title="確認" onClick={() => onUpdate("acknowledge")} />}
              {a.state !== "resolved" && <IconButton icon="checkmark.circle" title="解除" onClick={() => onUpdate("resolve")} />}
              <IconButton icon="sparkles" title="讓 AI 分析" tint="purple" onClick={onAnalyze} />
            </span>
          )}
        </div>
        <div className="row caption muted" style={{ gap: 14 }}>
          <span className="row gap4 ellipsis"><Icon name="shippingbox" size={12} />{serviceName}</span>
          <span className="row gap4 ellipsis" style={{ minWidth: 0 }}><Icon name="function" size={12} /><span className="mono ellipsis" style={{ fontSize: 11 }}>{a.rule}</span></span>
          <span className="row gap4" style={{ flex: "none" }}><Icon name="clock" size={12} />
            {active ? `持續 ${shortDuration((now - new Date(a.startedAt).getTime()) / 1000)}` : `開始於 ${relative(a.startedAt)}`}
          </span>
          {a.acknowledgedBy && a.state === "acknowledged" && <span className="row gap4" style={{ flex: "none" }}>確認者 <ActorLabel actor={a.acknowledgedBy} size={14} /></span>}
          <div className="grow" />
          {a.incidentId && (
            <button className="btn link caption" title={`前往事件 ${a.incidentId}`} onClick={(e) => { e.stopPropagation(); go("incidents", a.incidentId!); }}>
              <Icon name="flame.fill" size={11} />{a.incidentId}
            </button>
          )}
        </div>
        {a.aiNote && <AICallout title="AI 初步判讀">{a.aiNote}</AICallout>}
        {analyzing ? (
          <div className="row gap8" style={{ padding: 10, borderRadius: 8, background: "color-mix(in srgb, var(--purple) 6%, transparent)", color: "var(--purple)" }}>
            <Spinner size={12} /> AI 正在比對指標、日誌與最近部署…
          </div>
        ) : analysis ? <AICallout title="AI 分析結果">{analysis}</AICallout> : null}
        {selected && (
          <div className="row gap8" onClick={(e) => e.stopPropagation()}>
            <button className="btn" disabled={a.state !== "firing"} onClick={() => onUpdate("acknowledge")}><Icon name="eye" size={12} />確認</button>
            <button className="btn" disabled={a.state === "resolved"} onClick={() => onUpdate("resolve")}><Icon name="checkmark.circle" size={12} />解除</button>
            <button className="btn" disabled={a.state === "silenced" || a.state === "resolved"} onClick={() => onUpdate("silence")}><Icon name="bell.slash" size={12} />靜音</button>
            <span style={{ width: 1, height: 16, background: "var(--line)" }} />
            {a.incidentId
              ? <button className="btn" onClick={() => go("incidents", a.incidentId!)}><Icon name="flame" size={12} />查看事件 {a.incidentId}</button>
              : <button className="btn" disabled={a.state === "resolved"} onClick={onCreateIncident}><Icon name="flame" size={12} />建立事件</button>}
            <div className="grow" />
            <button className="btn purple" disabled={analyzing} onClick={onAnalyze}><Icon name="sparkles" size={12} />{analysis ? "重新分析" : "讓 AI 分析"}</button>
          </div>
        )}
      </div>
      {menu.node}
    </div>
  );
}

/** 模擬的 AI 分析(Swift 的 MonAlertAnalyzer) */
export function analyzeAlert(a: Alert, services: Service[], logs: LogEntry[], deployments: Deployment[]): string {
  const svc = services.find((s) => s.id === a.service);
  const name = svc?.name ?? a.service;
  const started = new Date(a.startedAt).getTime();
  const related = logs.filter((l) => l.service === a.service && new Date(l.date).getTime() >= started - 30 * 60000);
  const errors = related.filter((l) => l.level === "error");
  const warns = related.filter((l) => l.level === "warn");
  const parts: string[] = [`${name} 在 ${hm(a.startedAt)} 開始違反「${a.rule}」,目前值 ${a.value}。`];
  if (errors.length || warns.length) {
    let s = `告警前後 30 分鐘內有 ${errors.length} 筆 error、${warns.length} 筆 warn 日誌`;
    const counts = new Map<string, number>();
    errors.forEach((e) => counts.set(e.message, (counts.get(e.message) ?? 0) + 1));
    const top = [...counts.entries()].sort((x, y) => y[1] - x[1])[0]?.[0];
    if (top) s += `,最常見的是「${top.length > 60 ? top.slice(0, 60) + "…" : top}」`;
    parts.push(s + "。");
  } else parts.push("同時段沒有明顯的錯誤日誌,較可能是流量或資源面的問題。");
  const d = deployments
    .filter((x) => x.service === a.service && new Date(x.startedAt).getTime() <= started && started - new Date(x.startedAt).getTime() < 6 * 3600000)
    .sort((x, y) => y.startedAt.localeCompare(x.startedAt))[0];
  if (d) parts.push(`告警前 ${shortDuration((started - new Date(d.startedAt).getTime()) / 1000)} 有一次部署(${d.previousVersion} → ${d.version}),建議優先檢查這次變更,必要時回滾。`);
  else parts.push("6 小時內沒有這個服務的部署紀錄。");
  if (svc?.dependencies?.length) {
    const bad = svc.dependencies.map((id) => services.find((s) => s.id === id)).filter((s): s is Service => !!s && s.health !== "healthy");
    if (bad.length) parts.push(`相依服務 ${bad.map((s) => s.name).join("、")} 也處於異常狀態,可能是上游問題。`);
  }
  parts.push(a.severity === "critical" ? "建議:立即建立事件並通知值班人員。" : a.severity === "warning" ? "建議:先確認並持續觀察 15 分鐘,若沒有改善再升級。" : "建議:不需立即處理,可列入下次例行檢查。");
  return parts.join("");
}
