// Swift 模型經 MCPJSON 編出來的 JSON 型別(監控、事件、AI 自動化、文件櫃、設定用)。欄位名稱看 mac/Sources/Omixor/Core/Models.swift 等。
import type { Actor, ActorView } from "../types";

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
export type IncidentEvent = { id: string; date: string; actor: Actor; actorRef?: ActorView; kind: IncidentEventKind; text: string };
/** 待決定(decision:要人選一個)與需要協助(help:要人做事)——同一張表,用 kind 分開(core/model/decision.go) */
export type DecisionKind = "decision" | "help";
export type DecisionStatus = "open" | "decided" | "withdrawn";
export type DecisionOption = { id: string; label: string; description?: string; recommended?: boolean };
export type Decision = {
  id: string;
  kind: DecisionKind;
  title: string;
  body: string;
  /** help 一律是空陣列 */
  options: DecisionOption[];
  status: DecisionStatus;
  /** decision:選項 id 或 "other";help:"done" 或 "declined";沒處理是空字串 */
  answer: string;
  answerNote: string;
  /** help 完成的證據 */
  evidence?: string;
  /** 來源 Issue 的顯示編號 key(沒有是空字串;來源 Issue 在看不到的受限專案時,整筆決定不會出現) */
  sourceIssue: string;
  blocks: string[];
  /** 建立者、處理人:相容字串(人 = email、角色 = agent:<handle>),旁邊是 actor 物件(ISS-36 C-3;沒處理沒有 decidedByActor) */
  createdBy: string;
  decidedBy: string;
  createdByActor?: ActorView;
  decidedByActor?: ActorView;
  createdAt: string;
  decidedAt?: string;
  updatedAt: string;
};
/** help 的 decided 畫面顯示「已處理」 */
export const DECISION_STATUS_LABEL: Record<DecisionStatus, (kind: DecisionKind) => string> = {
  open: () => "待處理",
  decided: (k) => (k === "help" ? "已處理" : "已決定"),
  withdrawn: () => "已撤回",
};

/** Issue 子項(issue_item,ISS-37:Issue 底下可領取、有依賴與驗收證據的小工作;core/model/issue_item.go)。畫面還沒做,先備型別與契約 */
export type ItemStatus = "todo" | "doing" | "blocked" | "done" | "canceled";
export type ItemWaitKind = "user_decision" | "item" | "external";
export const ITEM_STATUSES: ItemStatus[] = ["todo", "doing", "blocked", "done", "canceled"];
export const ITEM_STATUS_LABEL: Record<ItemStatus, string> = { todo: "待領取", doing: "進行中", blocked: "卡住", done: "完成", canceled: "取消" };
export const ITEM_WAIT_LABEL: Record<ItemWaitKind, string> = { user_decision: "使用者決定", item: "另一個子項", external: "外部條件" };
export type IssueItem = {
  id: string;
  issueId: string;
  seq: number;
  /** 顯示編號:<Issue key>.<序號>,例如 ISS-37.2 */
  key: string;
  title: string;
  status: ItemStatus;
  /** 負責人、領取者、建立者:相容字串(人 = email、角色 = agent:<handle>),旁邊是 actor 物件;沒指派 / 沒領取沒有欄位 */
  assignee?: string;
  assigneeActor?: ActorView;
  /** 前置子項的 key(同 Issue;未完成前置不能領取、完成) */
  dependsOn?: string;
  files?: string[];
  acceptance?: string;
  /** 完成證據:JSON 物件({commit, tests, note, summary, unverified[]…}) */
  evidence?: Record<string, unknown>;
  blockReason?: string;
  waitForKind?: ItemWaitKind;
  /** user_decision:問題或 DEC-n;item:同 Issue 另一子項的 key;external:說明 */
  waitFor?: string;
  claimedBy?: string;
  claimedByActor?: ActorView;
  /** 租約到期時間(領取中才有) */
  lockedUntil?: string;
  /** 負責人是角色時,領取綁定的 agent_task id */
  taskId?: string;
  /** 提升為獨立 Issue 後,新 Issue 的顯示編號(原子項同時標為 canceled) */
  promotedIssueId?: string;
  createdBy: string;
  createdByActor?: ActorView;
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
};

export type Incident = {
  id: string; title: string; sev: number; status: IncidentStatus; services: string[]; commander: string; startedAt: string;
  resolvedAt?: string | null; impact: string; timeline: IncidentEvent[]; aiSummary?: string | null; followUps: string[];
  /** 指揮官、宣告者的 actor 物件(ISS-36 C-3) */
  commanderActor?: ActorView; createdByActor?: ActorView;
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
  /** 建立者、領取者的 actor 物件(ISS-36 C-3) */
  createdByActor?: ActorView; claimedByActor?: ActorView;
  notes: { id: string; author: string; authorActor?: ActorView; text: string; at: string }[];
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

/** 角色自動執行(這台 Mac 上的 Claude Code;設定存在組織資料夾的 agent-runner.json,不進核心) */
export type AgentRunConfig = { autoRun: boolean; model: string; effort: string; workdir: string; allowBash: boolean; timeoutMinutes: number };
export type RunnerCurrent = { taskId: string; agentId: string; agentName: string; title: string; org: string; startedAt: string; lastOutput: string };
export type RunnerOutcome = "done" | "blocked" | "error";
export type RunnerRecord = {
  taskId: string; agentId: string; agentName: string; title: string; outcome: RunnerOutcome | string; error?: string;
  startedAt: string; finishedAt: string; durationSeconds: number; costUsd: number;
};
export type AgentRunnerView = {
  org: string; loop: boolean; claudeFound: boolean; claudeBin: string; efforts: string[];
  agents: { id: string; name: string; active: boolean; config: AgentRunConfig }[];
  current: RunnerCurrent | null; history: RunnerRecord[]; lastError?: string; error?: string;
  /** 系統設定關掉了「自動執行」:不領新任務(進行中的做完) */
  disabled?: boolean; disabledReason?: string;
};

/** 組織鏈的一條邊:from 可以派工作給 to(label 空白 = 任務),to 做完回報給 from。節點是 "agent:<角色 id>" 或 "user:<email 小寫>" */
export type ChainEdge = { from: string; to: string; label: string; fromActor?: ActorView; toActor?: ActorView };
export type OrgChain = { edges: ChainEdge[]; updatedAt?: string };
/** 組織鏈視圖(org_chain):鏈、組織成員(user 節點的候選)、能不能修改 */
export type OrgChainView = {
  org: string; remote: boolean; canEdit: boolean; chain: OrgChain;
  members: { email: string; name: string; role: string }[]; error?: string;
};

/** 訊息頻道(核心 model/channel.go):成員是節點 "agent:<角色 id>" 或 "user:<email 小寫>"。unread 是我的未讀數 */
export type Channel = {
  id: string; name: string; topic: string; issueId: string; members: string[]; createdBy: string;
  createdAt: string; updatedAt: string; unread: number; lastMessageAt: string | null;
  /** 成員(順序同 members)、建立者的 actor 物件(ISS-36 C-3) */
  memberActors?: ActorView[]; createdByActor?: ActorView;
};
/** 頻道訊息:id 是 M-<n>(每個頻道各自編號),mentions 是被 @ 的成員節點 */
export type ChannelMessage = {
  id: string; channelId: string; author: string; text: string; mentions: string[]; at: string;
  /** 作者、被提及者(順序同 mentions)的 actor 物件(ISS-36 C-3) */
  authorActor?: ActorView; mentionActors?: ActorView[];
};
export type ChannelPage = { messages: ChannelMessage[]; hasMore: boolean };
/** 對話視圖(channels):我的頻道、我的節點、組織成員與角色 */
export type ChannelsView = {
  org: string; remote: boolean; me: string; channels: Channel[];
  members: { email: string; name: string; role: string }[]; agents: Agent[]; error?: string;
};

/** docAccess 的 key(同 core 的 model.DocAccessKey):未分專案(空字串)不加前綴,專案是 "<專案 id>:<區塊 id 或頁籤網址>" */
export const docAccessKey = (project: string | null | undefined, key: string) => (project ? `${project}:${key}` : key);

/** 代理人可存取某專案(空字串或省略 = 未分專案)某位置的權限(undefined = 不能)。
 *  同 core 的 Agent.AccessIn:頁籤的設定優先(有設定就以它為準,"none" = 不能),再看區塊;沒有前綴的 key 不適用於專案 */
export function agentAccess(a: Agent, section: string, tab?: string | null, project?: string | null): DocAccess | undefined {
  const da = a.docAccess ?? {};
  const has = (k: string) => Object.prototype.hasOwnProperty.call(da, k);
  if (tab) {
    const k = docAccessKey(project, tab);
    if (has(k)) return !da[k] || da[k] === "none" ? undefined : da[k];
  }
  const k = docAccessKey(project, section);
  const s = has(k) ? da[k] : undefined;
  return !s || s === "none" ? undefined : s;
}

// 文件櫃(Cabinet.swift)
export type CabinetTab = { path: string; label: string; code?: string | null; group?: string | null };
export type CabinetSection = { id: string; label: string; path?: string; tabs: CabinetTab[] };
/** project:所屬專案 id(省略 = 未分專案,就是原本的組織文件櫃) */
export type CabinetDoc = { id: string; title: string; body: string; section: string; tab?: string | null; tags: string[]; author: string; authorActor?: ActorView; createdAt: string; updatedAt: string; project?: string };
/** layoutVersion:結構的版本號(沒存過是 0,每存一次 +1);存結構時帶 expectedVersion 做樂觀鎖 */
export type CabinetView = { layout: CabinetSection[]; docs: CabinetDoc[]; layoutVersion: number };
/** 組織底下的專案(core 的 model.Project;PRJ-<n>)。
 *  restricted = 受限專案:只有專案成員、組織管理員看得到(Issue、文件櫃、專案本身),看不到的不會出現在清單、單筆回 404;false = 組織成員都看得到。
 *  myRole = 呼叫者在專案的角色("admin" 組織管理員或專案管理員、"member" 專案成員;不是成員沒有這個欄位)。
 *  改旗標用 action("project_update", { id, restricted }) 或建立時 project_create 帶 restricted(組織管理員) */
export type Project = { id: string; name: string; key?: string; description: string; repos: string[]; restricted: boolean; myRole?: string; createdAt: string; updatedAt: string };
/** 專案成員(core 的 model.ProjectMember;GET …/projects/{id}/members)。node 是相容的節點寫法(user:<email>、agent:<角色 id>),role 是 "admin" | "member"。
 *  管理用 action("project_members" | "project_member_set" | "project_member_remove", { id: 專案 id, actor, role }),console 限 web 管理員 */
export type ProjectMember = { project: string; actor?: ActorView; node: string; role: string; createdAt: string };
/** 專案與程式碼倉庫的連結(core 的 model.ProjectRepo;GET …/projects/repos)。只用於預設值與篩選,不是權限來源:
 *  建立 Issue 沒指定專案、repo 只連到一個專案時預設放進那個專案;依 repo 篩選時連到該 repo 的專案裡沒標 repo 的 Issue 也算。沒連任何專案 = 組織共用。
 *  連結、取消連結用 action("project_repo_link" | "project_repo_unlink", { id: 專案 id, repo }),回傳更新後的 Project(組織管理員) */
export type ProjectRepo = { project: string; repo: string; createdAt: string };
/** cabinet_query 的回傳:某個專案(空字串 = 未分專案)的結構與文件,加上組織的專案清單 */
export type CabinetQuery = CabinetView & { project: string; layoutSaved: boolean; projects: Project[]; projectsError?: string; error?: string };
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

/** 看板拖放:把 status 這張 Issue 拖進 dropStatus 的欄,放下後要改成的狀態;null = 不能放(原欄、待處理 ↔ 進行中、欄沒有 dropStatus) */
export const boardDropStatus = (dropStatus: "open" | "closed" | null | undefined, status: "open" | "closed"): "open" | "closed" | null =>
  dropStatus && dropStatus !== status ? dropStatus : null;
