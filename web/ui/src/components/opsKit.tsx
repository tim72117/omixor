// 總覽、開發進度、CI/CD、伺服器與服務共用的型別與小元件。
// 對應 Swift 版 Core/Models.swift 的列舉(標籤、顏色、圖示)與 Components/Components.swift 的
// HealthBadge、RunStatusBadge、EnvBadge、AIBadge、ActorAvatar、ActorLabel、UsageBar、Sparkline、SeriesChart。
import { createContext, useContext, useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import * as L from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Area, AreaChart, CartesianGrid, Legend, Line, LineChart, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { useView } from "../api";
import { guardedNavigate, useFeatures } from "../features";
import { Icon } from "./Icon";
import { color, Modal, Pill } from "./ui";
import type { Actor, Nav, SectionId } from "../types";
export type { Actor };
import "./opsKit.css";

// MARK: - 模型(JSON 欄位 = Swift 的 Codable 欄位)

export type MetricPoint = { date: string; value: number };
export type Person = { id: string; name: string; role: string; colorHex: number };
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
export type Alert = {
  id: string; title: string; severity: SeverityKey; service: string; state: AlertStateKey; rule: string; value: string;
  startedAt: string; acknowledgedBy?: Actor | null; incidentId?: string | null; aiNote?: string | null;
};
export type Incident = {
  id: string; title: string; sev: number; status: IncidentStatusKey; services: string[]; commander: string;
  startedAt: string; resolvedAt?: string | null; impact: string; aiSummary?: string | null; followUps: string[];
};
export type OnCallShift = { id: string; person: string; role: string; start: string; end: string };
export type Automation = { id: string; name: string; summary: string; icon: string; trigger: string; runs30d: number; timeSavedHours: number };
export type AutomationRun = { id: string; automation: string; status: RunStatusKey; startedAt: string };
export type ApprovalRequest = {
  id: string; title: string; automation: string; risk: RiskKey; action: string; rationale: string; evidence: string[];
  rollbackPlan: string; createdAt: string; expiresAt: string; state: "pending" | "approved" | "rejected" | "expired";
};
export type ActivityItem = { id: string; date: string; actor: Actor; icon: string; text: string; section: SectionId };
export type Agent = { id: string; name: string; symbol?: string; color?: string; active?: boolean };

// MARK: - 列舉

type Meta = { label: string; color: string; icon?: string };
export type HealthKey = "healthy" | "degraded" | "down" | "maintenance" | "unknown";
export const HEALTH: Record<HealthKey, Meta> = {
  healthy: { label: "正常", color: "green", icon: "checkmark.circle.fill" },
  degraded: { label: "效能下降", color: "orange", icon: "exclamationmark.triangle.fill" },
  down: { label: "中斷", color: "red", icon: "xmark.octagon.fill" },
  maintenance: { label: "維護中", color: "blue", icon: "wrench.and.screwdriver.fill" },
  unknown: { label: "未知", color: "gray", icon: "questionmark.circle.fill" },
};
export const HEALTH_ORDER: HealthKey[] = ["healthy", "degraded", "down", "maintenance", "unknown"];
export const healthRank = (h: HealthKey) => ({ down: 0, degraded: 1, maintenance: 2, unknown: 3, healthy: 4 })[h] ?? 3;

export type EnvKey = "production" | "staging" | "development";
export const ENV: Record<EnvKey, Meta & { short: string }> = {
  production: { label: "Production", short: "prod", color: "red" },
  staging: { label: "Staging", short: "stg", color: "orange" },
  development: { label: "Development", short: "dev", color: "teal" },
};
export const ENV_ORDER: EnvKey[] = ["production", "staging", "development"];

export type RunStatusKey = "queued" | "running" | "succeeded" | "failed" | "canceled" | "waitingApproval";
export const RUN: Record<RunStatusKey, Meta> = {
  queued: { label: "排隊中", color: "gray", icon: "clock" },
  running: { label: "執行中", color: "blue", icon: "arrow.triangle.2.circlepath" },
  succeeded: { label: "成功", color: "green", icon: "checkmark.circle.fill" },
  failed: { label: "失敗", color: "red", icon: "xmark.circle.fill" },
  canceled: { label: "已取消", color: "secondary", icon: "slash.circle" },
  waitingApproval: { label: "等待核准", color: "purple", icon: "hand.raised.fill" },
};
export const RUN_ORDER: RunStatusKey[] = ["queued", "running", "succeeded", "failed", "canceled", "waitingApproval"];

export type PRStatusKey = "draft" | "open" | "approved" | "changesRequested" | "merged" | "closed";
export const PR: Record<PRStatusKey, Meta> = {
  draft: { label: "草稿", color: "gray" }, open: { label: "待審", color: "blue" }, approved: { label: "已核准", color: "green" },
  changesRequested: { label: "需修改", color: "orange" }, merged: { label: "已合併", color: "purple" }, closed: { label: "已關閉", color: "secondary" },
};
export const PR_ORDER: PRStatusKey[] = ["draft", "open", "approved", "changesRequested", "merged", "closed"];

export type CheckKey = "pending" | "running" | "passed" | "failed";
export const CHECK: Record<CheckKey, Meta> = {
  pending: { label: "等待中", color: "gray", icon: "circle.dotted" },
  running: { label: "執行中", color: "blue", icon: "arrow.triangle.2.circlepath" },
  passed: { label: "通過", color: "green", icon: "checkmark.circle.fill" },
  failed: { label: "失敗", color: "red", icon: "xmark.circle.fill" },
};

export type WorkStatusKey = "backlog" | "todo" | "inProgress" | "review" | "done";
export const WORK_STATUS: Record<WorkStatusKey, Meta> = {
  backlog: { label: "Backlog", color: "gray", icon: "tray" },
  todo: { label: "待辦", color: "secondary", icon: "circle" },
  inProgress: { label: "進行中", color: "blue", icon: "circle.lefthalf.filled" },
  review: { label: "審查中", color: "purple", icon: "eye" },
  done: { label: "完成", color: "green", icon: "checkmark.circle.fill" },
};
export const WORK_STATUS_ORDER: WorkStatusKey[] = ["backlog", "todo", "inProgress", "review", "done"];

export type WorkTypeKey = "feature" | "bug" | "chore" | "incident";
export const WORK_TYPE: Record<WorkTypeKey, Meta> = {
  feature: { label: "功能", color: "blue" }, bug: { label: "Bug", color: "red" },
  chore: { label: "雜務", color: "gray" }, incident: { label: "事件後續", color: "orange" },
};
export const WORK_TYPE_ORDER: WorkTypeKey[] = ["feature", "bug", "chore", "incident"];

export type RiskKey = "low" | "medium" | "high";
export const RISK: Record<RiskKey, Meta> = {
  low: { label: "低風險", color: "green" }, medium: { label: "中風險", color: "orange" }, high: { label: "高風險", color: "red" },
};

export type SeverityKey = "critical" | "warning" | "info";
export const SEVERITY: Record<SeverityKey, Meta & { rank: number }> = {
  critical: { label: "Critical", color: "red", rank: 0 }, warning: { label: "Warning", color: "orange", rank: 1 }, info: { label: "Info", color: "blue", rank: 2 },
};
export type AlertStateKey = "firing" | "acknowledged" | "resolved" | "silenced";
export const ALERT_STATE: Record<AlertStateKey, string> = { firing: "觸發中", acknowledged: "已確認", resolved: "已解除", silenced: "已靜音" };

export type IncidentStatusKey = "investigating" | "identified" | "monitoring" | "resolved";
export const INCIDENT_STATUS: Record<IncidentStatusKey, Meta> = {
  investigating: { label: "調查中", color: "red" }, identified: { label: "已找到原因", color: "orange" },
  monitoring: { label: "觀察中", color: "blue" }, resolved: { label: "已解決", color: "green" },
};

export const TIME_RANGE: Record<string, { label: string; seconds: number }> = {
  "1h": { label: "1 小時", seconds: 3600 }, "6h": { label: "6 小時", seconds: 6 * 3600 }, "24h": { label: "24 小時", seconds: 86400 },
  "7d": { label: "7 天", seconds: 7 * 86400 }, "30d": { label: "30 天", seconds: 30 * 86400 },
};

export const isAI = (a?: Actor | null) => a?.kind === "ai";
export const sameActor = (a?: Actor | null, b?: Actor | null) => !!a && !!b && a.kind === b.kind && (a.id ?? "") === (b.id ?? "");

// MARK: - 格式化(Swift 的 Fmt)

export const t = (iso?: string | null) => (iso ? new Date(iso).getTime() : NaN);
export const now = () => Date.now();

/** Fmt.duration:45s、3m 5s、1h 2m */
export function dur(sec?: number | null): string {
  if (sec == null || isNaN(sec)) return "—";
  const s = Math.floor(sec);
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}
/** Fmt.percent */
export const pct = (v: number, digits = 1) => `${v.toFixed(digits)}%`;
/** Fmt.number */
export function num(v: number): string {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
  return v.toFixed(0);
}
export function monthDay(iso: string | number): string {
  const d = new Date(iso);
  return `${d.getMonth() + 1}月${d.getDate()}日`;
}
export function monthDayTime(iso: string | number): string {
  const d = new Date(iso);
  return `${monthDay(iso)} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}
export function hhmm(iso: string | number): string {
  const d = new Date(iso);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}
export function startOfDay(ms: number): number {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}
export const isToday = (iso: string) => startOfDay(t(iso)) === startOfDay(now());
/** Calendar 的天數差(以日曆日計算) */
export const dayDiff = (from: number, to: number) => Math.round((startOfDay(to) - startOfDay(from)) / 86400000);

export function copyText(s: string) {
  navigator.clipboard?.writeText(s).catch(() => undefined);
}

// MARK: - 共用資料(人、自動化、角色、服務、導覽)

type Shared = {
  people: Person[]; automations: Automation[]; agents: Agent[]; services: Service[]; repos: Repository[];
  me: string; nav?: Nav;
  person: (id: string) => Person | undefined;
  service: (id: string) => Service | undefined;
  name: (a?: Actor | null) => string;
  go: (section: SectionId, focusId?: string | null) => void;
  /** 能不能跳到這個頁面(系統設定關掉的頁面,連結與按鈕不顯示) */
  canGo: (section: SectionId) => boolean;
};
const SharedCtx = createContext<Shared | null>(null);

/** 頁面最外層包一次,子元件用 useOps() 取人名、服務、導覽 */
export function OpsProvider({ children }: { children: ReactNode }) {
  const [people] = useView<Person[]>("people");
  const [automations] = useView<Automation[]>("automations");
  const [agentsView] = useView<{ agents: Agent[] }>("agents");
  const [services] = useView<Service[]>("services");
  const [repos] = useView<Repository[]>("repos");
  const [me] = useView<{ id: string; name: string }>("me");
  const [nav] = useView<Nav>("nav");
  const flags = useFeatures();
  const ppl = Array.isArray(people) ? people : [];
  const autos = Array.isArray(automations) ? automations : [];
  const agents = Array.isArray(agentsView?.agents) ? agentsView!.agents : [];
  const svcs = Array.isArray(services) ? services : [];
  const value: Shared = {
    people: ppl, automations: autos, agents, services: svcs, repos: Array.isArray(repos) ? repos : [],
    me: me?.id ?? "tim", nav,
    person: (id) => ppl.find((p) => p.id === id),
    service: (id) => svcs.find((s) => s.id === id),
    name: (a) => {
      if (!a) return "未指派";
      if (a.kind === "person") return ppl.find((p) => p.id === a.id)?.name ?? a.id ?? "?";
      if (a.kind === "ai") return autos.find((x) => x.id === a.id)?.name ?? agents.find((x) => x.id === a.id)?.name ?? (a.id === "mcp" ? "MCP 用戶端" : "AI");
      return "系統";
    },
    go: (section, focusId) => { guardedNavigate(section, focusId).catch(() => undefined); },
    canGo: flags.visible,
  };
  return <SharedCtx.Provider value={value}>{children}</SharedCtx.Provider>;
}

export function useOps(): Shared {
  const v = useContext(SharedCtx);
  if (!v) throw new Error("useOps 要放在 OpsProvider 裡");
  return v;
}

/** 讀 view,後端還沒有這個 view 或格式不對時回傳空陣列(畫面顯示空狀態) */
export function useList<T>(name: string, topics: string[] = []): [T[], string | undefined, boolean] {
  const [d, err] = useView<T[]>(name, topics);
  return [Array.isArray(d) ? d : [], err, d !== undefined || err !== undefined];
}

/** 每秒更新一次的現在時間(經過時間、倒數) */
export function useNow(ms = 1000): number {
  const [n, setN] = useState(Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setN(Date.now()), ms);
    return () => window.clearInterval(id);
  }, [ms]);
  return n;
}

// MARK: - 圖示(補 Icon.tsx 沒有的 SF Symbols)

const EXTRA: Record<string, LucideIcon> = {
  "checkmark.shield": L.ShieldCheck, speedometer: L.Gauge, "checkmark.seal": L.BadgeCheck, "checkmark.seal.fill": L.BadgeCheck,
  "xmark.seal": L.BadgeX, "xmark.octagon": L.OctagonX, "xmark.octagon.fill": L.OctagonX, "wrench.and.screwdriver": L.Wrench,
  "questionmark.circle": L.CircleHelp, "questionmark.circle.fill": L.CircleHelp, "arrow.triangle.2.circlepath": L.RefreshCw,
  "hand.raised.fill": L.Hand, "circle.dotted": L.CircleDotDashed, tortoise: L.Turtle, "person.badge.clock": L.UserRoundSearch,
  "gauge.with.dots.needle.33percent": L.Gauge, "list.bullet.rectangle": L.ListTodo, "flag.checkered": L.Flag, timer: L.Timer,
  hourglass: L.Hourglass, "flag.2.crossed.fill": L.Flag, "chart.line.downtrend.xyaxis": L.TrendingDown, "chart.bar": L.ChartColumn,
  tray: L.Inbox, circle: L.Circle, "circle.lefthalf.filled": L.Contrast, "arrow.up.forward": L.ArrowUpRight, "arrow.right": L.ArrowRight,
  "smallcircle.filled.circle": L.Dot, "ellipsis.circle": L.Ellipsis, ellipsis: L.Ellipsis, xmark: L.X, "arrow.triangle.merge": L.GitMerge,
  "exclamationmark.shield.fill": L.ShieldAlert, "shippingbox.fill": L.Package, "bubble.left": L.MessageSquare, scope: L.Crosshair,
  "stop.circle": L.CircleStop, "wand.and.stars": L.WandSparkles, terminal: L.SquareTerminal, "doc.text.magnifyingglass": L.FileSearch,
  number: L.Hash, bird: L.Bird, "square.split.2x1": L.Columns2, "arrow.uturn.backward.circle": L.RotateCcw,
  "arrow.down.circle.fill": L.CircleArrowDown, "arrow.up.forward.circle.fill": L.CircleArrowOutUpRight, "cross.case": L.BriefcaseMedical,
  "paperplane.fill": L.Send, "play.circle.fill": L.CirclePlay, "stop.circle.fill": L.CircleStop, "person.3": L.Users, "book.closed": L.BookOpen,
  "arrow.up.left.and.arrow.down.right": L.Maximize2, "drop.triangle": L.Droplet, restart: L.RotateCw, "arrow.uturn.left": L.Undo2,
  "arrow.right.circle": L.CircleArrowRight, "line.3.horizontal.decrease.circle": L.ListFilter, "line.3.horizontal.decrease.circle.fill": L.ListFilter,
  "point.3.connected.trianglepath.dotted": L.Workflow, "square.stack.3d.up.slash": L.Layers, "arrow.up.circle": L.CircleArrowUp,
  "sidebar.left": L.PanelLeft, "lock.shield": L.ShieldCheck, "gearshape.fill": L.Settings, power: L.Power, "rectangle.stack": L.Layers,
  "arrow.down": L.ArrowDown, "arrow.uturn.backward.circle.fill": L.RotateCcw, stethoscope: L.Stethoscope, "person.crop.circle.badge.checkmark": L.UserCheck,
  "exclamationmark.bubble": L.MessageSquareWarning, "arrow.triangle.pull": L.GitPullRequest, "chevron.left.forwardslash.chevron.right": L.Code,
};

export function SIcon({ name, size = 14, color: c, className, spin, style }: { name?: string; size?: number; color?: string; className?: string; spin?: boolean; style?: CSSProperties }) {
  const C = name ? EXTRA[name] : undefined;
  const cls = (className ?? "") + (spin ? " ops-spin" : "");
  if (C) return <C size={size} color={c ? color(c) : undefined} className={cls} strokeWidth={1.8} style={{ flex: "none", ...style }} />;
  return <span className={cls} style={{ display: "inline-flex", ...style }}><Icon name={name} size={size} color={c ? color(c) : undefined} /></span>;
}

// MARK: - 小元件

export const HealthBadge = ({ h, compact }: { h: HealthKey; compact?: boolean }) =>
  compact ? <span title={HEALTH[h]?.label}><SIcon name={HEALTH[h]?.icon} color={HEALTH[h]?.color} /></span>
    : <Pill text={HEALTH[h]?.label ?? h} color={HEALTH[h]?.color} icon={HEALTH[h]?.icon} />;

export const RunStatusBadge = ({ s }: { s: RunStatusKey }) => <Pill text={RUN[s]?.label ?? s} color={RUN[s]?.color} icon={RUN[s]?.icon} />;
export const EnvBadge = ({ env }: { env: EnvKey }) => <Pill text={ENV[env]?.short ?? env} color={ENV[env]?.color} />;
export const AIBadge = ({ text = "AI" }: { text?: string }) => <Pill text={text} color="purple" icon="sparkles" />;

/** 狀態圖示,執行中會旋轉 */
export function RunIcon({ s, size = 14 }: { s: RunStatusKey; size?: number }) {
  return <span title={RUN[s]?.label} style={{ display: "inline-flex" }}><SIcon name={RUN[s]?.icon} color={RUN[s]?.color} size={size} spin={s === "running"} className={s === "waitingApproval" ? "ops-pulse" : undefined} /></span>;
}

export function StatusDot({ c, pulsing }: { c: string; pulsing?: boolean }) {
  return <span className={"ops-dot" + (pulsing ? " pulsing" : "")} style={{ ["--c" as any]: color(c) }} />;
}

export function personColor(p?: Person) {
  if (!p) return "var(--gray)";
  return `#${p.colorHex.toString(16).padStart(6, "0")}`;
}

export function ActorAvatar({ actor, size = 22 }: { actor?: Actor | null; size?: number }) {
  const o = useOps();
  if (!actor) return <span className="ops-avatar ops-avatar-none" style={{ width: size, height: size }} title="未指派" />;
  const st: CSSProperties = { width: size, height: size, fontSize: size * 0.45 };
  if (actor.kind === "person") {
    const p = o.person(actor.id ?? "");
    return <span className="ops-avatar" style={{ ...st, background: personColor(p) }} title={p?.name ?? actor.id}>{p?.name.slice(0, 1) ?? "?"}</span>;
  }
  if (actor.kind === "ai") {
    return <span className="ops-avatar ai" style={st} title={o.name(actor)}><SIcon name="sparkles" size={size * 0.5} color="#fff" /></span>;
  }
  return <span className="ops-avatar sys" style={st} title="系統"><SIcon name="gearshape.fill" size={size * 0.5} color="#fff" /></span>;
}

export function ActorLabel({ actor, className }: { actor?: Actor | null; className?: string }) {
  const o = useOps();
  return <span className={"row gap6 " + (className ?? "")} style={{ minWidth: 0 }}><ActorAvatar actor={actor} size={18} /><span className="ellipsis">{o.name(actor)}</span></span>;
}

/** 0...100 的使用率條 */
export function UsageBar({ value, label }: { value: number; label?: string }) {
  const c = value > 85 ? "red" : value > 70 ? "orange" : "green";
  return (
    <div className="row gap8" style={{ width: "100%" }}>
      {label && <span className="caption muted" style={{ width: 34, flex: "none" }}>{label}</span>}
      <Bar value={value / 100} c={c} />
      <span className="caption muted num" style={{ width: 36, textAlign: "right", flex: "none" }}>{Math.trunc(value)}%</span>
    </div>
  );
}

/** 細長進度條;value 0...1 */
export function Bar({ value, c = "blue", height = 6, track, style }: { value: number; c?: string; height?: number; track?: string; style?: CSSProperties }) {
  return (
    <div className="ops-bar" style={{ height, background: track, ...style }}>
      <div style={{ width: `${Math.min(1, Math.max(0, value)) * 100}%`, background: color(c) }} />
    </div>
  );
}

/** PipeProgressBar:進度條 + 百分比 */
export function ProgressBar({ value, c = "blue", showLabel = true, style }: { value: number; c?: string; showLabel?: boolean; style?: CSSProperties }) {
  return (
    <div className="row gap6" style={{ width: "100%", ...style }}>
      <div className="ops-bar" style={{ height: 6 }}><div style={{ width: `${Math.max(2, Math.min(1, Math.max(0, value)) * 100)}%`, background: color(c), transition: "width .6s" }} /></div>
      {showLabel && <span className="num faint" style={{ fontSize: 10.5, width: 32, textAlign: "right", flex: "none" }}>{Math.round(value * 100)}%</span>}
    </div>
  );
}

let sparkId = 0;
export function Sparkline({ values, c = "blue", height = 28 }: { values: number[]; c?: string; height?: number }) {
  const [id] = useState(() => `sp${++sparkId}`);
  if (values.length < 2) return <div style={{ height }} />;
  const data = values.map((v, i) => ({ i, v }));
  const col = color(c);
  return (
    <div style={{ height, width: "100%" }}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 2, bottom: 2, left: 0, right: 0 }}>
          <defs>
            <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={col} stopOpacity={0.25} />
              <stop offset="100%" stopColor={col} stopOpacity={0.02} />
            </linearGradient>
          </defs>
          <YAxis hide domain={["dataMin", "dataMax"]} />
          <Area type="monotone" dataKey="v" stroke={col} strokeWidth={1.5} fill={`url(#${id})`} isAnimationActive={false} />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

export const SERIES_COLORS = ["var(--blue)", "var(--green)", "var(--orange)", "var(--purple)", "var(--pink)", "var(--teal)", "var(--indigo)", "var(--red)", "var(--yellow)", "var(--gray)"];
export const axisStyle = { fontSize: 10.5, fill: "var(--fg2)" };

/** 時間序列折線圖(SeriesChart) */
export function SeriesChart({ series, height = 180, threshold, yDomain, yFormat, colors }: {
  series: { name: string; points: MetricPoint[] }[]; height?: number; threshold?: number; yDomain?: [number, number]; yFormat?: (v: number) => string; colors?: string[];
}) {
  const rows = new Map<number, any>();
  series.forEach((s) => s.points.forEach((p) => {
    const k = t(p.date);
    const r = rows.get(k) ?? { x: k };
    r[s.name] = p.value;
    rows.set(k, r);
  }));
  const data = [...rows.values()].sort((a, b) => a.x - b.x);
  const span = data.length > 1 ? data[data.length - 1].x - data[0].x : 0;
  const fmtX = (v: number) => (span > 2 * 86400000 ? `${new Date(v).getMonth() + 1}/${new Date(v).getDate()}` : hhmm(v));
  return (
    <div style={{ height, width: "100%" }}>
      <ResponsiveContainer>
        <LineChart data={data} margin={{ top: 14, right: 8, bottom: 0, left: -12 }}>
          <CartesianGrid stroke="var(--line)" vertical={false} />
          <XAxis dataKey="x" type="number" scale="time" domain={["dataMin", "dataMax"]} tickFormatter={fmtX} tick={axisStyle} stroke="var(--line)" minTickGap={30} />
          <YAxis tick={axisStyle} stroke="var(--line)" domain={yDomain ?? ["auto", "auto"]} tickFormatter={yFormat} width={48} />
          <Tooltip labelFormatter={(v) => monthDayTime(Number(v))} formatter={(v: any) => (typeof v === "number" ? (yFormat ? yFormat(v) : v.toFixed(2)) : v)} contentStyle={{ fontSize: 12, background: "var(--bg2)", border: "1px solid var(--line)" }} />
          {series.length > 1 && <Legend wrapperStyle={{ fontSize: 11 }} iconSize={8} />}
          {series.map((s, i) => <Line key={s.name} dataKey={s.name} type="monotone" dot={false} strokeWidth={1.6} stroke={colors?.[i] ?? SERIES_COLORS[i % SERIES_COLORS.length]} isAnimationActive={false} />)}
          {threshold != null && <ReferenceLine y={threshold} stroke="var(--red)" strokeOpacity={0.6} strokeDasharray="4 3" label={{ value: "門檻", position: "insideTopRight", fontSize: 10, fill: "var(--red)" }} />}
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}

/** 數字指標卡(Swift 的 MetricTile:delta 依 deltaGood 顯示綠/紅,可附 sparkline) */
export function Metric({ title, value, delta, deltaGood = true, icon, tint = "blue", series }: {
  title: string; value: ReactNode; delta?: string | null; deltaGood?: boolean; icon?: string; tint?: string; series?: number[];
}) {
  return (
    <div className="card" style={{ gap: 6 }}>
      <div className="row gap6 muted" style={{ fontSize: 12.5 }}>{icon && <SIcon name={icon} color={tint} />}<span className="ellipsis">{title}</span></div>
      <div className="row gap8 wrap" style={{ alignItems: "baseline", rowGap: 0 }}>
        <span className="num" style={{ fontSize: 26, fontWeight: 600, whiteSpace: "nowrap" }}>{value}</span>
        {delta && <span className="caption" style={{ fontWeight: 500, color: deltaGood ? "var(--green)" : "var(--red)" }}>{delta}</span>}
      </div>
      {series && series.length > 0 && <Sparkline values={series} c={tint} height={28} />}
    </div>
  );
}

/** 有色邊框的提示區塊(PipeCallout) */
export function Callout({ tint, icon, children, style }: { tint: string; icon: string; children: ReactNode; style?: CSSProperties }) {
  const c = color(tint);
  return (
    <div className="ops-callout" style={{ background: `color-mix(in srgb, ${c} 8%, transparent)`, borderColor: `color-mix(in srgb, ${c} 28%, transparent)`, ...style }}>
      <SIcon name={icon} size={22} color={tint} />
      <div className="col gap8 grow">{children}</div>
    </div>
  );
}

/** 標題列:標題、數量徽章、右側「全部 >」連結(OverviewCardHeader) */
export function CardHeader({ title, count, countColor = "secondary", linkTitle = "全部", onLink, icon }: {
  title: string; count?: number; countColor?: string; linkTitle?: string; onLink?: () => void; icon?: ReactNode;
}) {
  const plain = countColor === "secondary";
  return (
    <div className="row gap8">
      {icon}
      <span className="card-title">{title}</span>
      {count != null && count > 0 && (
        <span className="ops-count" style={plain ? undefined : { background: color(countColor), color: "white" }}>{count}</span>
      )}
      <span className="grow" />
      {onLink && <button className="btn link caption" onClick={onLink}>{linkTitle} <SIcon name="chevron.right" size={10} /></button>}
    </div>
  );
}

/** 卡片內的小空狀態 */
export function InlineEmpty({ icon, text, c = "secondary" }: { icon: string; text: string; c?: string }) {
  return <div className="row gap8 muted" style={{ justifyContent: "center", padding: "14px 0", fontSize: 12.5 }}><SIcon name={icon} color={c} />{text}</div>;
}

/** 錯誤或後端尚未提供時的提示列 */
export function Banner({ children, tint = "orange" }: { children: ReactNode; tint?: string }) {
  if (!children) return null;
  return <div className="ops-banner" style={{ color: color(tint), background: `color-mix(in srgb, ${color(tint)} 8%, transparent)` }}><SIcon name="exclamationmark.triangle" size={12} />{children}</div>;
}

// MARK: - 選單、確認框、提示

export type MenuItem = { label: ReactNode; onClick?: () => void; disabled?: boolean; danger?: boolean; checked?: boolean } | "sep" | { header: string };

function MenuList({ items, close }: { items: MenuItem[]; close: () => void }) {
  return (
    <>
      {items.map((it, i) => {
        if (it === "sep") return <div key={i} className="menu-sep" />;
        if ("header" in it) return <div key={i} className="ops-menu-header">{it.header}</div>;
        return (
          <button key={i} className={"menu-item" + (it.danger ? " danger" : "")} disabled={it.disabled}
            onClick={() => { close(); it.onClick?.(); }}>
            <span style={{ width: 12, flex: "none" }}>{it.checked ? "✓" : ""}</span>{it.label}
          </button>
        );
      })}
    </>
  );
}

/** 右鍵選單:const menu = useContextMenu(); <div onContextMenu={menu.open(items)}>…</div>{menu.el} */
export function useContextMenu() {
  const [st, setSt] = useState<{ x: number; y: number; items: MenuItem[] } | null>(null);
  useEffect(() => {
    if (!st) return;
    const close = () => setSt(null);
    window.addEventListener("click", close);
    window.addEventListener("blur", close);
    window.addEventListener("scroll", close, true);
    return () => { window.removeEventListener("click", close); window.removeEventListener("blur", close); window.removeEventListener("scroll", close, true); };
  }, [st]);
  return {
    open: (items: MenuItem[]) => (e: React.MouseEvent) => { e.preventDefault(); e.stopPropagation(); setSt({ x: e.clientX, y: e.clientY, items }); },
    el: st ? (
      <div className="ops-menu" style={{ left: Math.min(st.x, window.innerWidth - 240), top: Math.min(st.y, window.innerHeight - 40 - st.items.length * 28) }} onClick={(e) => e.stopPropagation()}>
        <MenuList items={st.items} close={() => setSt(null)} />
      </div>
    ) : null,
  };
}

/** 按鈕下拉選單(Menu { … } label: …) */
export function MenuButton({ label, items, className = "btn", title, align = "left" }: { label: ReactNode; items: MenuItem[]; className?: string; title?: string; align?: "left" | "right" }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const h = (e: MouseEvent) => { if (!ref.current?.contains(e.target as Node)) setOpen(false); };
    window.addEventListener("mousedown", h);
    return () => window.removeEventListener("mousedown", h);
  }, [open]);
  return (
    <div ref={ref} style={{ position: "relative", display: "inline-flex" }}>
      <button className={className} title={title} onClick={() => setOpen(!open)}>{label}</button>
      {open && <div className="ops-menu abs" style={align === "right" ? { right: 0 } : { left: 0 }}><MenuList items={items} close={() => setOpen(false)} /></div>}
    </div>
  );
}

export type ConfirmSpec = { title: string; message?: ReactNode; buttons: { label: string; onClick: () => void; danger?: boolean; primary?: boolean }[] };

/** confirmationDialog:const [dlg, ask] = useConfirm(); ask({...}); 把 dlg 放進畫面 */
export function useConfirm(): [ReactNode, (s: ConfirmSpec) => void] {
  const [spec, setSpec] = useState<ConfirmSpec | null>(null);
  const el = (
    <Modal open={!!spec} onClose={() => setSpec(null)} title={spec?.title} width={440}>
      {spec?.message && <div className="muted" style={{ whiteSpace: "pre-wrap", lineHeight: 1.5 }}>{spec.message}</div>}
      <div className="row gap8" style={{ justifyContent: "flex-end" }}>
        <button className="btn" onClick={() => setSpec(null)}>取消</button>
        {spec?.buttons.map((b) => (
          <button key={b.label} className={"btn" + (b.primary ? " primary" : "") + (b.danger ? " danger" : "")} onClick={() => { setSpec(null); b.onClick(); }}>{b.label}</button>
        ))}
      </div>
    </Modal>
  );
  return [el, setSpec];
}

/** 動作完成的短暫提示(InfraToast);也拿來顯示動作失敗 */
export function useToast(): [ReactNode, (text: string, kind?: "ok" | "error") => void] {
  const [msg, setMsg] = useState<{ text: string; kind: "ok" | "error" } | null>(null);
  const timer = useRef<number>();
  const show = (text: string, kind: "ok" | "error" = "ok") => {
    setMsg({ text, kind });
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setMsg(null), kind === "error" ? 5000 : 2600);
  };
  const el = msg ? (
    <div className="ops-toast">
      <SIcon name={msg.kind === "ok" ? "checkmark.circle.fill" : "exclamationmark.triangle.fill"} color={msg.kind === "ok" ? "green" : "orange"} />
      <span>{msg.text}</span>
    </div>
  ) : null;
  return [el, show];
}

/** 執行動作;失敗時用 toast 顯示錯誤(後端還沒做好的動作會顯示「沒有這個 action」之類的訊息) */
export function useAct() {
  const [toastEl, toast] = useToast();
  const run = async <T,>(f: () => Promise<T>, ok?: string): Promise<T | undefined> => {
    try {
      const r = await f();
      if (ok) toast(ok);
      return r;
    } catch (e: any) {
      toast(String(e?.message ?? e), "error");
      return undefined;
    }
  };
  return { run, toast, toastEl };
}

/** 點擊列(List 的一列) */
export function Seg<T extends string>({ value, options, onChange }: { value: T; options: { value: T; label: ReactNode; title?: string }[]; onChange: (v: T) => void }) {
  return (
    <div className="segmented">
      {options.map((o) => <button key={o.value} title={o.title} className={o.value === value ? "on" : ""} onClick={() => onChange(o.value)}>{o.label}</button>)}
    </div>
  );
}

/** Toggle(.button 樣式) */
export function ToggleButton({ on, onChange, children, title }: { on: boolean; onChange: (v: boolean) => void; children: ReactNode; title?: string }) {
  return <button className={"btn" + (on ? " ops-on" : "")} title={title} onClick={() => onChange(!on)}>{children}</button>;
}

export function Check({ on, onChange, children, disabled }: { on: boolean; onChange: (v: boolean) => void; children: ReactNode; disabled?: boolean }) {
  return (
    <label className="row gap4 ops-check" style={{ opacity: disabled ? 0.5 : 1 }}>
      <input type="checkbox" checked={on} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      {children}
    </label>
  );
}

/** 搜尋框 */
export function SearchField({ value, onChange, placeholder, style }: { value: string; onChange: (v: string) => void; placeholder: string; style?: CSSProperties }) {
  return (
    <div className="ops-search" style={style}>
      <SIcon name="magnifyingglass" className="muted" />
      <input value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
      {value && <button className="ops-icon-btn" onClick={() => onChange("")} title="清除搜尋"><SIcon name="xmark.circle.fill" className="faint" /></button>}
    </div>
  );
}

/** 頁首:標題、副標、右側內容 */
export function PageTitle({ title, subtitle, children }: { title: string; subtitle?: ReactNode; children?: ReactNode }) {
  return (
    <div className="row gap12" style={{ alignItems: "center" }}>
      <div className="col gap4" style={{ minWidth: 0 }}>
        <div className="page-title">{title}</div>
        {subtitle && <div className="muted">{subtitle}</div>}
      </div>
      <span className="grow" />
      {children}
    </div>
  );
}

/** 元素寬度(做兩欄/一欄切換);回傳 callback ref,元素晚出現也能量到 */
export function useWidth<T extends HTMLElement>(): [(el: T | null) => void, number] {
  const [el, setEl] = useState<T | null>(null);
  const [w, setW] = useState(1000);
  useEffect(() => {
    if (!el) return;
    setW(el.getBoundingClientRect().width);
    const ro = new ResizeObserver((es) => setW(es[0].contentRect.width));
    ro.observe(el);
    return () => ro.disconnect();
  }, [el]);
  return [setEl, w];
}

/** Markdown 簡易顯示(Swift 的 MarkdownText):標題、清單、程式碼區塊、行內粗體/程式碼/連結/圖片 */
export function MarkdownText({ source }: { source: string }) {
  type B = { k: "h"; l: number; t: string } | { k: "li"; m: string; t: string } | { k: "code"; t: string } | { k: "p"; t: string };
  const out: B[] = [];
  let code: string[] | null = null;
  let para: string[] = [];
  const flush = () => { if (para.length) { out.push({ k: "p", t: para.join("\n") }); para = []; } };
  for (const raw of source.split("\n")) {
    const line = raw.trim();
    if (line.startsWith("```")) {
      if (code) { out.push({ k: "code", t: code.join("\n") }); code = null; } else { flush(); code = []; }
      continue;
    }
    if (code) { code.push(raw); continue; }
    if (!line) { flush(); continue; }
    const h = /^(#{1,4}) (.*)$/.exec(line);
    if (h) { flush(); out.push({ k: "h", l: h[1].length, t: h[2].trim() }); continue; }
    const b = /^[-*+] (.*)$/.exec(line);
    if (b) { flush(); out.push({ k: "li", m: "•", t: b[1] }); continue; }
    const n = /^(\d+)\. (.*)$/.exec(line);
    if (n) { flush(); out.push({ k: "li", m: n[1] + ".", t: n[2] }); continue; }
    para.push(raw);
  }
  if (code) out.push({ k: "code", t: (code as string[]).join("\n") });
  flush();
  return (
    <div className="col gap6" style={{ userSelect: "text" }}>
      {out.map((b, i) => {
        if (b.k === "h") return <div key={i} style={{ fontWeight: 600, fontSize: b.l <= 1 ? 16 : b.l === 2 ? 14 : 13, marginTop: 4 }}><Inline s={b.t} /></div>;
        if (b.k === "li") return <div key={i} className="row gap6" style={{ alignItems: "baseline" }}><span className="muted">{b.m}</span><span><Inline s={b.t} /></span></div>;
        if (b.k === "code") return <pre key={i} className="ops-code">{b.t}</pre>;
        return <div key={i} style={{ whiteSpace: "pre-wrap", lineHeight: 1.5 }}><Inline s={b.t} /></div>;
      })}
    </div>
  );
}

/** 能顯示的圖片網址:本機附件(/api/attachments/…)或 https 圖片 */
const imageSrcOK = (u: string) => /^\/api\/attachments\/[0-9a-f]{32}\.(png|jpg|gif|webp)$/.test(u) || /^https:\/\//.test(u);

/** Markdown 圖片:點一下開新分頁看原圖 */
function MdImage({ alt, src }: { alt: string; src: string }) {
  if (!imageSrcOK(src)) return <>{alt || src}</>;
  return (
    <a href={src} target="_blank" rel="noreferrer" title={alt ? `${alt}(點一下看原圖)` : "點一下看原圖"} style={{ display: "inline-block", maxWidth: "100%" }}>
      <img src={src} alt={alt} loading="lazy" referrerPolicy="no-referrer"
        style={{ maxWidth: "100%", maxHeight: 420, borderRadius: 6, border: "1px solid var(--line)", display: "block", cursor: "zoom-in" }} />
    </a>
  );
}

function Inline({ s }: { s: string }) {
  const parts: ReactNode[] = [];
  const re = /(!\[[^\]]*\]\([^)\s]+\)|\*\*[^*]+\*\*|`[^`]+`|\[[^\]]+\]\([^)]+\))/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let k = 0;
  while ((m = re.exec(s))) {
    if (m.index > last) parts.push(s.slice(last, m.index));
    const tok = m[0];
    if (tok.startsWith("![")) {
      const mm = /^!\[([^\]]*)\]\(([^)\s]+)\)$/.exec(tok)!;
      parts.push(<MdImage key={k++} alt={mm[1]} src={mm[2]} />);
    } else if (tok.startsWith("**")) parts.push(<b key={k++}>{tok.slice(2, -2)}</b>);
    else if (tok.startsWith("`")) parts.push(<code key={k++} className="ops-inline-code">{tok.slice(1, -1)}</code>);
    else {
      const mm = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(tok)!;
      parts.push(/^https?:/.test(mm[2]) ? <a key={k++} href={mm[2]} target="_blank" rel="noreferrer">{mm[1]}</a> : mm[1]);
    }
    last = m.index + tok.length;
  }
  if (last < s.length) parts.push(s.slice(last));
  return <>{parts}</>;
}
