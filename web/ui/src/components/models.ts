// Swift 模型經 MCPJSON 編出來的 JSON 型別(監控、事件、AI 自動化、文件櫃、設定用)。欄位名稱看 app/Sources/Omixor/Core/Models.swift 等。
import type { Actor } from "../types";

export type Severity = "critical" | "warning" | "info";
export const SEVERITIES: Severity[] = ["critical", "warning", "info"];
export const SEV_LABEL: Record<Severity, string> = { critical: "Critical", warning: "Warning", info: "Info" };
export const SEV_COLOR: Record<Severity, string> = { critical: "red", warning: "orange", info: "blue" };
export const SEV_ICON: Record<Severity, string> = { critical: "exclamationmark.octagon.fill", warning: "exclamationmark.triangle.fill", info: "info.circle.fill" };
export const sevRank = (s: Severity) => SEVERITIES.indexOf(s);

export type AlertState = "firing" | "acknowledged" | "resolved" | "silenced";
export const ALERT_STATE_LABEL: Record<AlertState, string> = { firing: "觸發中", acknowledged: "已確認", resolved: "已解除", silenced: "已靜音" };
export const ALERT_STATE_COLOR: Record<AlertState, string> = { firing: "red", acknowledged: "orange", resolved: "green", silenced: "gray" };
export const ALERT_STATE_ICON: Record<AlertState, string> = { firing: "flame.fill", acknowledged: "eye.fill", resolved: "checkmark.circle.fill", silenced: "bell.slash.fill" };

export type Alert = {
  id: string; title: string; severity: Severity; service: string; state: AlertState; rule: string; value: string;
  startedAt: string; acknowledgedBy?: Actor | null; incidentId?: string | null; aiNote?: string | null;
};
export type AlertRule = { id: string; name: string; expression: string; severity: Severity; enabled: boolean; notify: string[]; autoRemediation?: string | null };
export type LogLevel = "debug" | "info" | "warn" | "error";
export const LOG_LEVELS: LogLevel[] = ["debug", "info", "warn", "error"];
export const LOG_COLOR: Record<LogLevel, string> = { debug: "gray", info: "blue", warn: "orange", error: "red" };
export type LogEntry = { id: string; date: string; level: LogLevel; service: string; host: string; message: string; traceId?: string | null };
export type MetricPoint = { date: string; value: number };
export type MetricSeries = { id: string; name: string; unit: string; points: MetricPoint[] };
export type Dashboard = { id: string; name: string; panels: MetricSeries[] };

export type Health = "healthy" | "degraded" | "down" | "maintenance" | "unknown";
export const HEALTH_COLOR: Record<Health, string> = { healthy: "green", degraded: "orange", down: "red", maintenance: "blue", unknown: "gray" };
export type Service = { id: string; name: string; health: Health; tier: number; dependencies: string[]; hostIds: string[] };
export type Server = { id: string; name: string; region: string };
export type Deployment = { id: string; service: string; version: string; previousVersion: string; startedAt: string };

export type IncidentStatus = "investigating" | "identified" | "monitoring" | "resolved";
export const INC_STATUSES: IncidentStatus[] = ["investigating", "identified", "monitoring", "resolved"];
export const INC_STATUS_LABEL: Record<IncidentStatus, string> = { investigating: "調查中", identified: "已找到原因", monitoring: "觀察中", resolved: "已解決" };
export const INC_STATUS_COLOR: Record<IncidentStatus, string> = { investigating: "red", identified: "orange", monitoring: "blue", resolved: "green" };
export const INC_STATUS_ICON: Record<IncidentStatus, string> = { investigating: "magnifyingglass", identified: "scope", monitoring: "eye", resolved: "checkmark.circle.fill" };
export type IncidentEventKind = "status" | "note" | "action" | "alert" | "deploy" | "aiFinding";
export type IncidentEvent = { id: string; date: string; actor: Actor; kind: IncidentEventKind; text: string };
export type Incident = {
  id: string; title: string; sev: number; status: IncidentStatus; services: string[]; commander: string; startedAt: string;
  resolvedAt?: string | null; impact: string; timeline: IncidentEvent[]; aiSummary?: string | null; followUps: string[];
};
export type OnCallShift = { id: string; person: string; role: string; start: string; end: string };
export type WorkStatus = "backlog" | "todo" | "inProgress" | "review" | "done";
export const WORK_STATUS_LABEL: Record<WorkStatus, string> = { backlog: "Backlog", todo: "待辦", inProgress: "進行中", review: "審查中", done: "完成" };
export type WorkItem = { id: string; title: string; type: string; status: WorkStatus; assignee?: Actor | null; points: number; service?: string | null; createdByAI: boolean };

export type Autonomy = "suggest" | "approve" | "auto";
export const AUTONOMIES: Autonomy[] = ["suggest", "approve", "auto"];
export const AUTONOMY_LABEL: Record<Autonomy, string> = { suggest: "只建議", approve: "需核准", auto: "自動執行" };
export const AUTONOMY_DETAIL: Record<Autonomy, string> = {
  suggest: "AI 只產生分析與建議,不做任何變更",
  approve: "AI 準備好動作,由人核准後才執行",
  auto: "AI 直接執行,事後通知並可回滾",
};
export const AUTONOMY_COLOR: Record<Autonomy, string> = { suggest: "gray", approve: "purple", auto: "green" };
export const AUTONOMY_ICON: Record<Autonomy, string> = { suggest: "lightbulb", approve: "hand.raised", auto: "bolt.fill" };
export type Risk = "low" | "medium" | "high";
export const RISK_LABEL: Record<Risk, string> = { low: "低風險", medium: "中風險", high: "高風險" };
export const RISK_COLOR: Record<Risk, string> = { low: "green", medium: "orange", high: "red" };
export type Automation = {
  id: string; name: string; summary: string; icon: string; trigger: string; autonomy: Autonomy; enabled: boolean; scope: string[];
  allowedTools: string[]; runs30d: number; successRate: number; timeSavedHours: number; lastRun?: string | null;
};
export type RunStatus = "queued" | "running" | "succeeded" | "failed" | "canceled" | "waitingApproval";
export const RUN_STATUSES: RunStatus[] = ["queued", "running", "succeeded", "failed", "canceled", "waitingApproval"];
export const RUN_LABEL: Record<RunStatus, string> = { queued: "排隊中", running: "執行中", succeeded: "成功", failed: "失敗", canceled: "已取消", waitingApproval: "等待核准" };
export const RUN_COLOR: Record<RunStatus, string> = { queued: "gray", running: "blue", succeeded: "green", failed: "red", canceled: "secondary", waitingApproval: "purple" };
export const RUN_ICON: Record<RunStatus, string> = { queued: "clock", running: "arrow.triangle.2.circlepath", succeeded: "checkmark.circle.fill", failed: "xmark.circle.fill", canceled: "slash.circle", waitingApproval: "hand.raised.fill" };
export type AIStep = { id: string; title: string; tool?: string | null; output?: string | null; status: "done" | "running" | "pending" | "failed" | "skipped" };
export type AutomationRun = {
  id: string; automation: string; target: string; status: RunStatus; startedAt: string; finishedAt?: string | null;
  steps: AIStep[]; summary: string; tokens: number; costUSD: number;
};
export type ApprovalState = "pending" | "approved" | "rejected" | "expired";
export type Approval = {
  id: string; title: string; automation: string; risk: Risk; action: string; rationale: string; evidence: string[]; rollbackPlan: string;
  createdAt: string; expiresAt: string; state: ApprovalState; decidedBy?: string | null;
};
export type ActivityItem = { id: string; date: string; actor: Actor; icon: string; text: string; section: string };
export type Integration = { id: string; name: string; category: string; icon: string; connected: boolean; detail: string };

// 虛擬角色(Agents.swift)
export type DocAccess = "none" | "read" | "write";
export const DOC_ACCESS_LABEL: Record<DocAccess, string> = { none: "不能存取", read: "可讀", write: "可讀寫" };
export type RoutineKind = "daily" | "weekly" | "hourly";
export const ROUTINE_KIND_LABEL: Record<RoutineKind, string> = { daily: "每天", weekly: "每週", hourly: "每隔幾小時" };
export type Priority = "urgent" | "high" | "normal" | "low";
export const PRIORITIES: Priority[] = ["urgent", "high", "normal", "low"];
export const PRIORITY_LABEL: Record<Priority, string> = { urgent: "緊急", high: "高", normal: "一般", low: "低" };
export const PRIORITY_COLOR: Record<Priority, string> = { urgent: "red", high: "orange", normal: "blue", low: "gray" };
export type AgentRoutine = {
  id: string; title: string; instructions: string; kind: RoutineKind; hour: number; minute: number; weekday: number; everyHours: number;
  priority: Priority; enabled: boolean; createdAt?: string; lastRunAt?: string | null;
};
export type Agent = {
  id: string; name: string; role: string; symbol: string; color: string; instructions: string; active: boolean;
  routines: AgentRoutine[]; createdAt?: string; docAccess: Record<string, DocAccess>;
};
export type AgentTaskStatus = "queued" | "inProgress" | "blocked" | "done" | "canceled";
export const TASK_LABEL: Record<AgentTaskStatus, string> = { queued: "待領取", inProgress: "進行中", blocked: "需要協助", done: "完成", canceled: "已取消" };
export const TASK_COLOR: Record<AgentTaskStatus, string> = { queued: "secondary", inProgress: "blue", blocked: "orange", done: "green", canceled: "gray" };
export const TASK_ICON: Record<AgentTaskStatus, string> = { queued: "tray", inProgress: "arrow.triangle.2.circlepath", blocked: "hand.raised", done: "checkmark.circle", canceled: "xmark.circle" };
export const taskOpen = (s: AgentTaskStatus) => s === "queued" || s === "inProgress" || s === "blocked";
export type AgentTask = {
  id: string; title: string; instructions: string; agentId: string; status: AgentTaskStatus; priority: Priority; createdBy: string;
  routineId?: string | null; createdAt: string; updatedAt: string; result: string; links: string[];
  /** 領取租約:進行中才有;鎖定期間別人不能領取,逾時會被排程自動退回待領取 */
  lockedUntil?: string | null; claimedBy?: string;
  notes: { id: string; author: string; text: string; at: string }[];
};
/** 進行中任務的鎖定提示(非進行中、沒有租約回 null) */
export function leaseLabel(t: AgentTask): string | null {
  if (t.status !== "inProgress" || !t.lockedUntil) return null;
  const u = new Date(t.lockedUntil);
  if (u.getTime() <= Date.now()) return "鎖定已逾時";
  const hm = `${String(u.getHours()).padStart(2, "0")}:${String(u.getMinutes()).padStart(2, "0")}`;
  return `處理中 · 鎖定到 ${hm}${t.claimedBy ? `(${t.claimedBy})` : ""}`;
}
export type AgentsView = { agents: Agent[]; tasks: AgentTask[] };

/** 組織鏈的一條邊:from 可以派工作給 to(label 空白 = 任務),to 做完回報給 from。節點是 "agent:<角色 id>" 或 "user:<email 小寫>" */
export type ChainEdge = { from: string; to: string; label: string };
export type OrgChain = { edges: ChainEdge[]; updatedAt?: string };
/** 組織鏈視圖(org_chain):鏈、組織成員(user 節點的候選)、能不能修改 */
export type OrgChainView = {
  org: string; remote: boolean; canEdit: boolean; chain: OrgChain;
  members: { email: string; name: string; role: string }[]; error?: string;
};

/** 代理人可存取某位置的權限(nil = 不能) */
export function agentAccess(a: Agent, section: string, tab?: string | null): DocAccess | undefined {
  const da = a.docAccess ?? {};
  if (tab && da[tab]) return da[tab] === "none" ? undefined : da[tab];
  const s = da[section];
  return !s || s === "none" ? undefined : s;
}

// 文件櫃(Cabinet.swift)
export type CabinetTab = { path: string; label: string; code?: string | null; group?: string | null };
export type CabinetSection = { id: string; label: string; path?: string; tabs: CabinetTab[] };
export type CabinetDoc = { id: string; title: string; body: string; section: string; tab?: string | null; tags: string[]; author: string; createdAt: string; updatedAt: string };
export type CabinetView = { layout: CabinetSection[]; docs: CabinetDoc[] };
export const tabTitle = (t: CabinetTab) => (t.code ? `${t.code} ${t.label}` : t.label);

// 連線(ConnectionModels.swift)
export type AuthKind = "oauth" | "approval" | "bearer" | "basic" | "none";
export const AUTH_KINDS: AuthKind[] = ["oauth", "approval", "bearer", "basic", "none"];
export const AUTH_LABEL: Record<AuthKind, string> = { oauth: "OAuth 2.0 / OIDC", approval: "瀏覽器核准", bearer: "固定 Token", basic: "帳號 + Token(Basic)", none: "不用認證" };
export const AUTH_DETAIL: Record<AuthKind, string> = {
  oauth: "標準 OAuth 授權碼流程 + PKCE,從網址自動探索端點;伺服器支援時自動註冊用戶端。Google、Auth0、Keycloak、Okta、MCP 伺服器等都適用",
  approval: "omixor 在本機開回呼網址,瀏覽器裡由管理員按核准後換 token(evoleap CLI 的登入方式)",
  bearer: "貼上 API key 或個人存取權杖,每次請求帶 Authorization: Bearer",
  basic: "存「帳號:token」(例如 Jira Cloud 的 email:API token),每次請求帶 Authorization: Basic",
  none: "公開的 API,不帶任何認證",
};
export type Connection = {
  id: string; name: string; baseURL: string; isProduction: boolean; testPath: string; auth: AuthKind; note: string;
  oauth: { issuer: string; authorizationEndpoint: string; tokenEndpoint: string; registrationEndpoint: string; clientId: string; hasClientSecret: boolean; scopes: string; resource: string; extraParams: string; sendIdToken: boolean; registeredPort?: number | null };
  approval: { consoleURL: string; startPath: string; approvePath: string; exchangePath: string; logoutPath: string };
};
export type ConnectionState = { account?: string | null; busy?: boolean; error?: string | null; lastTest?: string | null };
export type ConnectionsView = { connections: Connection[]; states: Record<string, ConnectionState> };

export type Organization = { id: string; name: string; symbol: string; color: string; note: string; createdAt?: string };
export type OrgIndex = { current: string; orgs: Organization[] };

/** 陣列型 view 的安全取值(後端回了別的格式也不會壞) */
export const arr = <T,>(v: unknown): T[] => (Array.isArray(v) ? (v as T[]) : []);
