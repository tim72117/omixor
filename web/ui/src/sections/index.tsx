// 各頁面。每個頁面一個資料夾(sections/<頁面>/),在這裡登記。
// 頁面元件收到 focusId(其他頁跳過來時要選中的項目,用完呼叫 clearFocus)。
import { lazy, type ComponentType } from "react";
import type { SectionId } from "../types";

export type SectionProps = { focusId?: string; clearFocus: () => void };

export const placeholder = (title: string) => () => import("./Placeholder").then((m) => ({ default: () => m.Placeholder({ title }) }));

// 頁面完成後把 placeholder 換成 lazy(() => import("./<頁面>/<頁面>View"))
export const SECTION_VIEWS: Record<SectionId, ComponentType<SectionProps>> = {
  overview: lazy(() => import("./overview/OverviewView")),
  development: lazy(() => import("./development/DevelopmentView")),
  pipelines: lazy(() => import("./pipelines/PipelinesView")),
  infrastructure: lazy(() => import("./infrastructure/InfrastructureView")),
  monitoring: lazy(() => import("./monitoring/MonitoringView")),
  incidents: lazy(() => import("./incidents/IncidentsView")),
  automation: lazy(() => import("./automation/AutomationView")),
  cabinet: lazy(() => import("./cabinet/CabinetView")),
  settings: lazy(() => import("./settings/SettingsView")),
  channels: lazy(() => import("./channels/ChannelsView")),
};
