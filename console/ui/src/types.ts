// 共用型別。資料的 JSON 格式和 Swift 版 MCPJSON 一樣(欄位名稱看 mac/Sources/Omixor/Core/Models.swift 等)。
export type Actor = { kind: "person" | "ai" | "system"; id?: string };

/** core 輸出的「誰」物件(model.ActorView;ISS-36 C-3):和相容字串欄位並存(authorActor 對 author、createdByActor 對 createdBy…)。
 *  name、handle 是「現在」的值;查不到的 actor 整個欄位沒有。 */
export type ActorView = {
  id: string; kind: "user" | "agent" | "system"; handle: string; name: string; email?: string;
  status: "active" | "disabled" | "suspended" | "retired";
};

export type SectionId =
  | "overview" | "development" | "pipelines" | "infrastructure" | "monitoring"
  | "incidents" | "automation" | "cabinet" | "settings" | "channels" | "system";

export const SECTIONS: { id: SectionId; title: string; icon: string; group: "main" | "auto" | "end" }[] = [
  { id: "overview", title: "總覽", icon: "square.grid.2x2", group: "main" },
  { id: "development", title: "開發進度", icon: "hammer", group: "main" },
  { id: "pipelines", title: "CI/CD", icon: "arrow.triangle.branch", group: "main" },
  { id: "infrastructure", title: "伺服器與服務", icon: "server.rack", group: "main" },
  { id: "monitoring", title: "監控與告警", icon: "waveform.path.ecg", group: "main" },
  { id: "incidents", title: "事件", icon: "flame", group: "main" },
  { id: "automation", title: "AI 自動化", icon: "sparkles", group: "auto" },
  { id: "channels", title: "對話", icon: "bubble.left.and.bubble.right", group: "auto" },
  { id: "cabinet", title: "文件櫃", icon: "archivebox", group: "auto" },
  { id: "settings", title: "設定", icon: "gearshape", group: "end" },
  { id: "system", title: "系統", icon: "switch.2", group: "end" },
];

/** 系統工作組織以外的組織也能用的頁面(資料分組織);第一個是預設落點(Issue 在開發進度) */
export const ORG_SECTIONS: SectionId[] = ["development", "incidents", "automation", "channels", "cabinet", "settings", "system"];

export type Nav = { section: SectionId; focusId?: string; env: string; timeRange: string };
