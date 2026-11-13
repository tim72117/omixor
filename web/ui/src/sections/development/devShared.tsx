// 開發進度頁內部共用:分頁、跨分頁狀態、計算指標、小元件(Swift 的 DevShared.swift)。
import { createContext, useContext } from "react";
import { action } from "../../api";
import { color, EmptyState } from "../../components/ui";
import { ActorAvatar, CHECK, SIcon, WORK_TYPE, now, t, type CheckKey, type PullRequest, type Repository, type WorkItem, type WorkTypeKey } from "../../components/opsKit";

export type DevTab = "overview" | "board" | "issues" | "pulls" | "repos" | "github" | "gitlab";
export const DEV_TABS: { id: DevTab; label: string; icon: string }[] = [
  { id: "overview", label: "概覽", icon: "chart.bar.xaxis" },
  { id: "board", label: "Sprint 看板", icon: "rectangle.split.3x1" },
  { id: "issues", label: "Issue", icon: "exclamationmark.bubble" },
  { id: "pulls", label: "Pull Requests", icon: "arrow.triangle.pull" },
  { id: "repos", label: "Repositories", icon: "shippingbox" },
  { id: "github", label: "GitHub", icon: "chevron.left.forwardslash.chevron.right" },
  { id: "gitlab", label: "GitLab", icon: "chevron.left.forwardslash.chevron.right" },
];

export type DevModel = {
  tab: DevTab; setTab: (t: DevTab) => void;
  selectedPR?: number; setSelectedPR: (n?: number) => void;
  prRepoFilter?: string; setPrRepoFilter: (r?: string) => void;
  highlightedItem?: string; setHighlightedItem: (id?: string) => void;
  selectedIssue?: string; setSelectedIssue: (id?: string) => void;
  showIssue: (id: string) => void; showPR: (id: number) => void; showRepoPRs: (repo: string) => void; showItem: (id: string) => void;
};
export const DevCtx = createContext<DevModel | null>(null);
export const useDev = () => useContext(DevCtx)!;

// MARK: - 程式碼倉庫設定的入口(GitHub、GitLab 分頁用)

/** 跳到 設定 › 程式碼倉庫(SettingsView 以 focusId 決定打開的分頁) */
export const openRepoSettings = () => action("navigate", { section: "settings", focusId: "repos" }).catch(() => undefined);

/** 分頁頂端的小按鈕:管理連線與追蹤的 repo / 專案 */
export function ManageReposButton() {
  return <button className="btn" title="在 設定 › 程式碼倉庫 管理連線與追蹤的 repo / 專案" onClick={openRepoSettings}><SIcon name="gearshape" size={12} />管理倉庫</button>;
}

/** 還沒設定、沒登入或沒追蹤時的空狀態,附「前往設定」按鈕 */
export function RepoSetupEmpty({ title, message, icon = "chevron.left.forwardslash.chevron.right" }: { title: string; message: string; icon?: string }) {
  return (
    <EmptyState title={title} icon={icon} message={
      <span className="col" style={{ alignItems: "center", gap: 10 }}>
        <span>{message}</span>
        <button className="btn primary" onClick={openRepoSettings}>前往 設定 › 程式碼倉庫</button>
      </span>
    } />
  );
}

// MARK: - 指標

export const isActivePR = (pr: PullRequest) => pr.status !== "merged" && pr.status !== "closed";
export function sprintPoints(items: WorkItem[]) {
  const active = items.filter((i) => i.status !== "backlog");
  return { done: active.filter((i) => i.status === "done").reduce((a, i) => a + i.points, 0), total: active.reduce((a, i) => a + i.points, 0) };
}
export function cycleTimeHours(prs: PullRequest[]) {
  const m = prs.filter((p) => p.status === "merged");
  return m.length ? m.reduce((a, p) => a + (t(p.updatedAt) - t(p.createdAt)), 0) / m.length / 3600000 : undefined;
}
export function reviewWaitHours(prs: PullRequest[]) {
  const w = prs.filter((p) => p.status === "open");
  return w.length ? w.reduce((a, p) => a + (now() - t(p.createdAt)), 0) / w.length / 3600000 : undefined;
}
export const aiShare = (prs: PullRequest[]) => (prs.length ? prs.filter((p) => p.author.kind === "ai").length / prs.length : undefined);
export const avgCoverage = (repos: Repository[]) => (repos.length ? repos.reduce((a, r) => a + r.coverage, 0) / repos.length : undefined);
export function hours(h?: number) {
  if (h == null) return "—";
  if (h < 1) return `${Math.trunc(h * 60)} 分`;
  if (h < 48) return `${h.toFixed(1)} 小時`;
  return `${(h / 24).toFixed(1)} 天`;
}
export function age(iso: string) {
  const s = (now() - t(iso)) / 1000;
  if (s < 3600) return `${Math.max(1, Math.trunc(s / 60))} 分`;
  if (s < 86400) return `${Math.trunc(s / 3600)} 小時`;
  return `${Math.trunc(s / 86400)} 天`;
}

// MARK: - 小元件

export function ChecksIcon({ s, showLabel }: { s: CheckKey; showLabel?: boolean }) {
  const m = CHECK[s] ?? CHECK.pending;
  return <span className="row gap4" title={`檢查:${m.label}`} style={{ color: color(m.color) }}><SIcon name={m.icon} color={m.color} />{showLabel && m.label}</span>;
}

export function DiffStat({ a, d }: { a: number; d: number }) {
  return <span className="row gap6 num" style={{ fontSize: 12.5 }}><span style={{ color: "var(--green)" }}>+{a}</span><span style={{ color: "var(--red)" }}>−{d}</span></span>;
}

export function AvatarStack({ ids, size = 20 }: { ids: string[]; size?: number }) {
  return (
    <span className="row">
      <span className="ops-avatar-stack">{ids.slice(0, 4).map((id) => <ActorAvatar key={id} actor={{ kind: "person", id }} size={size} />)}</span>
      {ids.length > 4 && <span className="muted" style={{ fontSize: 10.5, paddingLeft: 8 }}>+{ids.length - 4}</span>}
    </span>
  );
}

/** 覆蓋率條:越高越好 */
export function CoverageBar({ value }: { value: number }) {
  const c = value >= 80 ? "green" : value >= 60 ? "orange" : "red";
  return (
    <div className="row gap8" style={{ width: "100%" }}>
      <div className="ops-bar" style={{ height: 6 }}><div style={{ width: `${Math.min(100, Math.max(0, value))}%`, background: color(c) }} /></div>
      <span className="caption muted num" style={{ width: 36, textAlign: "right", flex: "none" }}>{value.toFixed(0)}%</span>
    </div>
  );
}

export function TypeTag({ type }: { type: WorkTypeKey }) {
  const m = WORK_TYPE[type];
  return <span className="row gap4 caption muted"><span style={{ width: 8, height: 8, borderRadius: 2, background: color(m?.color) }} />{m?.label ?? type}</span>;
}

export function VulnLabel({ count, big }: { count: number; big?: boolean }) {
  const st = big ? { fontSize: 15, fontWeight: 600 } : undefined;
  if (count === 0) return <span className="row gap4 num" style={{ color: "var(--green)", ...st }}><SIcon name="checkmark.shield" color="green" />0</span>;
  const c = count >= 3 ? "red" : "orange";
  return <span className="row gap4 num" style={{ color: color(c), ...st }}><SIcon name="exclamationmark.shield.fill" color={c} />{count}</span>;
}

export function DetailSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="card" style={{ padding: 12, gap: 8, borderRadius: 10 }}>
      <div className="muted" style={{ fontWeight: 600 }}>{title}</div>
      {children}
    </div>
  );
}
