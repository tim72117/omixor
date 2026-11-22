// 總覽、開發進度、CI/CD、伺服器與服務用的型別與列舉(原本在 components/omixorKit.tsx)。
// 對應 Swift 版 Core/Models.swift 的列舉(標籤、顏色、圖示)。和 domain.ts 同名的型別已合併成一份(見 models/index.ts)。
import type { Actor } from "../types";
import type {
  AlertState, Approval, Health, IncidentStatus, MetricPoint, Risk, RunStatus, Severity, WorkStatus,
} from "./domain";

// MARK: - 列舉的鍵(與 domain.ts 的同義型別共用同一個聯集)

export type HealthKey = Health;
export type RunStatusKey = RunStatus;
export type RiskKey = Risk;
export type SeverityKey = Severity;
export type AlertStateKey = AlertState;
export type IncidentStatusKey = IncidentStatus;
export type WorkStatusKey = WorkStatus;
export type EnvKey = "production" | "staging" | "development";
export type PRStatusKey = "draft" | "open" | "approved" | "changesRequested" | "merged" | "closed";
export type CheckKey = "pending" | "running" | "passed" | "failed";
export type WorkTypeKey = "feature" | "bug" | "chore" | "incident";

// MARK: - 模型(JSON 欄位 = Swift 的 Codable 欄位)

export type Service = {
  id: string; name: string; summary: string; team: string; owner: string; language: string; repo: string;
  health: HealthKey; versions: Partial<Record<EnvKey, string>>; tier: number; uptime30d: number; sloTarget: number;
  latencyP95: number; errorRate: number; rps: number; dependencies: string[]; hostIds: string[]; errorBudgetRemaining: number;
};
export type Container = { id: string; name: string; image: string; status: HealthKey; restarts: number; cpu: number; memoryMB: number };
export type Server = {
  id: string; name: string; role: string; provider: string; region: string; env: EnvKey; os: string; ip: string;
  cores: number; memoryGB: number; health: HealthKey; cpu: number; memory: number; disk: number; networkMbps: number;
  cpuSeries: MetricPoint[]; memorySeries: MetricPoint[]; uptimeDays: number; containers: Container[]; tags: string[];
};
export type Repository = {
  id: string; name: string; language: string; defaultBranch: string; openPRs: number; coverage: number;
  lastCommit: string; weeklyCommits: number[]; vulnerabilities: number;
};
export type AIReview = { summary: string; risk: RiskKey; findings: string[] };
export type PullRequest = {
  id: number; title: string; repo: string; author: Actor; branch: string; status: PRStatusKey; checks: CheckKey;
  reviewers: string[]; aiReview?: AIReview | null; additions: number; deletions: number; createdAt: string; updatedAt: string;
  linkedItem?: string | null;
};
export type WorkItem = {
  id: string; title: string; type: WorkTypeKey; status: WorkStatusKey; assignee?: Actor | null; points: number;
  service?: string | null; createdByAI: boolean;
};
export type Sprint = { name: string; start: string; end: string; goal: string; burndown: MetricPoint[]; idealBurndown: MetricPoint[] };
export type PipelineJob = { id: string; name: string; status: RunStatusKey; duration?: number | null; log: string[] };
export type PipelineStage = { id: string; name: string; status: RunStatusKey; jobs: PipelineJob[] };
export type PipelineRun = {
  id: number; pipeline: string; repo: string; branch: string; commitSHA: string; commitMessage: string; trigger: string;
  triggeredBy: Actor; status: RunStatusKey; stages: PipelineStage[]; startedAt: string; duration?: number | null;
  aiDiagnosis?: string | null; targetEnv?: EnvKey | null;
};
export type Deployment = {
  id: string; service: string; version: string; previousVersion: string; env: EnvKey; status: RunStatusKey;
  strategy: "Rolling" | "Canary" | "Blue/Green"; progress: number; canaryPercent?: number | null; startedAt: string;
  finishedAt?: string | null; by: Actor; changes: string[]; healthGate: CheckKey;
};
/** 核准請求(與 domain.ts 的 Approval 同一份資料;Approval 多一個選填的 decidedBy) */
export type ApprovalRequest = Approval;

// MARK: - 列舉的標籤、顏色、圖示

export type Meta = { label: string; color: string; icon?: string };
export const HEALTH: Record<HealthKey, Meta> = {
  healthy: { label: "正常", color: "green", icon: "checkmark.circle.fill" },
  degraded: { label: "效能下降", color: "orange", icon: "exclamationmark.triangle.fill" },
  down: { label: "中斷", color: "red", icon: "xmark.octagon.fill" },
  maintenance: { label: "維護中", color: "blue", icon: "wrench.and.screwdriver.fill" },
  unknown: { label: "未知", color: "gray", icon: "questionmark.circle.fill" },
};
export const HEALTH_ORDER: HealthKey[] = ["healthy", "degraded", "down", "maintenance", "unknown"];
export const healthRank = (h: HealthKey) => ({ down: 0, degraded: 1, maintenance: 2, unknown: 3, healthy: 4 })[h] ?? 3;

export const ENV: Record<EnvKey, Meta & { short: string }> = {
  production: { label: "Production", short: "prod", color: "red" },
  staging: { label: "Staging", short: "stg", color: "orange" },
  development: { label: "Development", short: "dev", color: "teal" },
};
export const ENV_ORDER: EnvKey[] = ["production", "staging", "development"];

export const RUN: Record<RunStatusKey, Meta> = {
  queued: { label: "排隊中", color: "gray", icon: "clock" },
  running: { label: "執行中", color: "blue", icon: "arrow.triangle.2.circlepath" },
  succeeded: { label: "成功", color: "green", icon: "checkmark.circle.fill" },
  failed: { label: "失敗", color: "red", icon: "xmark.circle.fill" },
  canceled: { label: "已取消", color: "secondary", icon: "slash.circle" },
  waitingApproval: { label: "等待核准", color: "purple", icon: "hand.raised.fill" },
};
export const RUN_ORDER: RunStatusKey[] = ["queued", "running", "succeeded", "failed", "canceled", "waitingApproval"];

export const PR: Record<PRStatusKey, Meta> = {
  draft: { label: "草稿", color: "gray" }, open: { label: "待審", color: "blue" }, approved: { label: "已核准", color: "green" },
  changesRequested: { label: "需修改", color: "orange" }, merged: { label: "已合併", color: "purple" }, closed: { label: "已關閉", color: "secondary" },
};
export const PR_ORDER: PRStatusKey[] = ["draft", "open", "approved", "changesRequested", "merged", "closed"];

export const CHECK: Record<CheckKey, Meta> = {
  pending: { label: "等待中", color: "gray", icon: "circle.dotted" },
  running: { label: "執行中", color: "blue", icon: "arrow.triangle.2.circlepath" },
  passed: { label: "通過", color: "green", icon: "checkmark.circle.fill" },
  failed: { label: "失敗", color: "red", icon: "xmark.circle.fill" },
};

export const WORK_STATUS: Record<WorkStatusKey, Meta> = {
  backlog: { label: "Backlog", color: "gray", icon: "tray" },
  todo: { label: "待辦", color: "secondary", icon: "circle" },
  inProgress: { label: "進行中", color: "blue", icon: "circle.lefthalf.filled" },
  review: { label: "審查中", color: "purple", icon: "eye" },
  done: { label: "完成", color: "green", icon: "checkmark.circle.fill" },
};

export const WORK_TYPE: Record<WorkTypeKey, Meta> = {
  feature: { label: "功能", color: "blue" }, bug: { label: "Bug", color: "red" },
  chore: { label: "雜務", color: "gray" }, incident: { label: "事件後續", color: "orange" },
};
export const WORK_TYPE_ORDER: WorkTypeKey[] = ["feature", "bug", "chore", "incident"];

export const RISK: Record<RiskKey, Meta> = {
  low: { label: "低風險", color: "green" }, medium: { label: "中風險", color: "orange" }, high: { label: "高風險", color: "red" },
};

export const SEVERITY: Record<SeverityKey, Meta & { rank: number }> = {
  critical: { label: "Critical", color: "red", rank: 0 }, warning: { label: "Warning", color: "orange", rank: 1 }, info: { label: "Info", color: "blue", rank: 2 },
};
export const ALERT_STATE: Record<AlertStateKey, string> = { firing: "觸發中", acknowledged: "已確認", resolved: "已解除", silenced: "已靜音" };

export const INCIDENT_STATUS: Record<IncidentStatusKey, Meta> = {
  investigating: { label: "調查中", color: "red" }, identified: { label: "已找到原因", color: "orange" },
  monitoring: { label: "觀察中", color: "blue" }, resolved: { label: "已解決", color: "green" },
};

export const TIME_RANGE: Record<string, { label: string; seconds: number }> = {
  "1h": { label: "1 小時", seconds: 3600 }, "6h": { label: "6 小時", seconds: 6 * 3600 }, "24h": { label: "24 小時", seconds: 86400 },
  "7d": { label: "7 天", seconds: 7 * 86400 }, "30d": { label: "30 天", seconds: 30 * 86400 },
};
