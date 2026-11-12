// 共用型別。資料的 JSON 格式和 Swift 版 MCPJSON 一樣(欄位名稱看 app/Sources/Omixor/Core/Models.swift 等)。
export type Actor = { kind: "person" | "ai" | "system"; id?: string };

export type SectionId =
  | "overview" | "development" | "pipelines" | "infrastructure" | "monitoring"
  | "incidents" | "automation" | "cabinet" | "settings" | "channels";

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
];

export type Nav = { section: SectionId; focusId?: string; env: string; timeRange: string };
