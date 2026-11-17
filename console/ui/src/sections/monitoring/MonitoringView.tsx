// 監控與告警:告警、儀表板、日誌、規則、網站訪客五個分頁(Swift 的 MonitoringView)
import { useEffect, useState } from "react";
import { useView } from "../../api";
import { PageHeader } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { arr, type Alert, type AlertRule, type Dashboard } from "../../components/models";
import { Segmented } from "../../components/ui";
import type { Nav } from "../../types";
import type { SectionProps } from "../index";
import { AlertsTab } from "./AlertsTab";
import { DashboardsTab } from "./DashboardsTab";
import { LogsTab } from "./LogsTab";
import { RulesTab } from "./RulesTab";
import { VisitorsTab } from "./VisitorsTab";
import { useFeatureTabs } from "../../features";

type MonTab = "alerts" | "dashboards" | "logs" | "rules" | "visitors";
const TABS: { id: MonTab; title: string; icon: string }[] = [
  { id: "alerts", title: "告警", icon: "bell.badge" },
  { id: "dashboards", title: "儀表板", icon: "chart.xyaxis.line" },
  { id: "logs", title: "日誌", icon: "text.alignleft" },
  { id: "rules", title: "規則", icon: "list.bullet.rectangle" },
  { id: "visitors", title: "網站訪客", icon: "person.3" },
];

export const TIME_RANGE_LABEL: Record<string, string> = { "1h": "1 小時", "6h": "6 小時", "24h": "24 小時", "7d": "7 天", "30d": "30 天" };
export const TIME_RANGE_SECONDS: Record<string, number> = { "1h": 3600, "6h": 6 * 3600, "24h": 86400, "7d": 7 * 86400, "30d": 30 * 86400 };

export function useTimeRange(): { key: string; label: string; seconds: number } {
  const [nav] = useView<Nav>("nav");
  const key = nav?.timeRange ?? "24h";
  return { key, label: TIME_RANGE_LABEL[key] ?? key, seconds: TIME_RANGE_SECONDS[key] ?? 86400 };
}

export default function MonitoringView({ focusId, clearFocus }: SectionProps) {
  const [rawTab, setRawTab] = useState<MonTab>("alerts");
  const tabs = useFeatureTabs(TABS.map((x) => x.id), rawTab, setRawTab, (x) => `monitoring.${x}`);
  const tab = tabs.shown, setTab = tabs.select;
  const [alertId, setAlertId] = useState<string>();
  const [dashId, setDashId] = useState<string>();
  const [ruleId, setRuleId] = useState<string>();
  const [alertsRaw] = useView<Alert[]>("alerts");
  const [rulesRaw] = useView<AlertRule[]>("alert_rules");
  const [dashRaw] = useView<Dashboard[]>("dashboards");
  const tr = useTimeRange();
  const alerts = arr<Alert>(alertsRaw);

  // 從其他頁跳過來時,依 focusId 切到對應分頁並選中項目
  useEffect(() => {
    if (!focusId) return;
    if (focusId === "visitors") setTab("visitors");
    else if (alerts.some((a) => a.id === focusId)) { setTab("alerts"); setAlertId(focusId); }
    else if (arr<AlertRule>(rulesRaw).some((r) => r.id === focusId)) { setTab("rules"); setRuleId(focusId); }
    else if (arr<Dashboard>(dashRaw).some((d) => d.id === focusId)) { setTab("dashboards"); setDashId(focusId); }
    else if (!alertsRaw || !rulesRaw || !dashRaw) return; // 資料還沒讀到,等一下再判斷
    clearFocus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusId, alertsRaw, rulesRaw, dashRaw]);

  const firing = alerts.filter((a) => a.state === "firing");
  const crit = firing.filter((a) => a.severity === "critical").length;
  const subtitle = firing.length === 0
    ? `目前沒有觸發中的告警 · 時間範圍 ${tr.label}`
    : `${firing.length} 則告警觸發中` + (crit > 0 ? `(${crit} 則 Critical)` : "") + ` · 時間範圍 ${tr.label}`;

  return (
    <div className="col" style={{ flex: 1, minHeight: 0 }}>
      <PageHeader
        title="監控與告警"
        subtitle={subtitle}
        right={
          <Segmented
            value={tab}
            onChange={setTab}
            options={TABS.filter((t) => tabs.on(t.id)).map((t) => ({ value: t.id, label: <span className="row gap4"><Icon name={t.icon} size={12} />{t.title}</span> }))}
          />
        }
      />
      <div className="col" style={{ flex: 1, minHeight: 0 }}>
        {tab === "alerts" && <AlertsTab selectedId={alertId} setSelectedId={setAlertId} />}
        {tab === "dashboards" && <DashboardsTab selectedId={dashId} setSelectedId={setDashId} />}
        {tab === "logs" && <LogsTab />}
        {tab === "rules" && <RulesTab highlightedId={ruleId} setHighlightedId={setRuleId} />}
        {tab === "visitors" && <VisitorsTab />}
      </div>
    </div>
  );
}
