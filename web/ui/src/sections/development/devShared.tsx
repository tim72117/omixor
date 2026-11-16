// 開發進度頁內部共用:分頁、跨分頁狀態、計算指標、小元件(Swift 的 DevShared.swift)。
import { createContext, useContext } from "react";
import { action } from "../../api";
import { color, EmptyState } from "../../components/ui";
import { ActorAvatar, CHECK, SIcon, WORK_TYPE, now, t, type CheckKey, type PullRequest, type Repository, type WorkItem, type WorkTypeKey } from "../../components/opsKit";

// Repositories(功能 development.repos,選用模組 integrations):GitHub 與 GitLab 追蹤中的 repo 一起列;
// 原本的 GitHub、GitLab 分頁已併進來(舊連結的 focusId "github" / "gitlab" 改開 Repositories)
export type DevTab = "overview" | "board" | "issues" | "pulls" | "repos";
export const DEV_TABS: { id: DevTab; label: string; icon: string }[] = [
  { id: "overview", label: "概覽", icon: "chart.bar.xaxis" },
  { id: "board", label: "看板", icon: "rectangle.split.3x1" },
  { id: "issues", label: "Issue", icon: "exclamationmark.bubble" },
  { id: "pulls", label: "Pull Requests", icon: "arrow.triangle.pull" },
  { id: "repos", label: "Repositories", icon: "shippingbox" },
];

export type DevModel = {
  tab: DevTab; setTab: (t: DevTab) => void;
  selectedPR?: number; setSelectedPR: (n?: number) => void;
  prRepoFilter?: string; setPrRepoFilter: (r?: string) => void;
  highlightedItem?: string; setHighlightedItem: (id?: string) => void;
  selectedIssue?: string; setSelectedIssue: (id?: string) => void;
  /** Repositories 選的 repo(「平台:key」,例如 github:owner/name、gitlab:42) */
  selectedRepo?: string; setSelectedRepo: (id?: string) => void;
  showIssue: (id: string) => void; showPR: (id: number) => void; showRepoPRs: (repo: string) => void; showItem: (id: string) => void;
  /** 打開 Repositories 並選這個 repo(「平台:key」;undefined = 只切到分頁) */
  showRepo: (id?: string) => void;
};
export const DevCtx = createContext<DevModel | null>(null);
export const useDev = () => useContext(DevCtx)!;

// MARK: - Issue 的倉庫篩選(Issue 分頁與看板共用;Swift 的 IssueRepoOptions / IssueRepoFilterPicker)

/** 選單去重用的鍵:不分大小寫(平台前綴保留,github:x 與 gitlab:x 是不同倉庫) */
export const repoKey = (r: string) => r.trim().toLowerCase();
/** 篩選「未標記倉庫」用的值,同核心的 model.IssueFilterNone(含括號,CleanRepo 不接受,不會和真的 repo 名稱撞到) */
export const NO_REPO = "(none)";

/** Issue 用到的倉庫(去重:不分大小寫,保留第一次出現的寫法),依字母排序 */
export function usedRepos(issues: { repo?: string }[]): string[] {
  const m = new Map<string, string>();
  for (const i of issues) if (i.repo && !m.has(repoKey(i.repo))) m.set(repoKey(i.repo), i.repo);
  return [...m.values()].sort((a, b) => a.localeCompare(b));
}

/** 依所屬倉庫篩選的下拉:"" = 全部倉庫;NO_REPO = 未標記;其他 = 只列這個倉庫(比對規則在核心的 model.RepoMatch)。
 *  沒有 Issue 標記倉庫、也沒在篩選時不顯示 */
export function RepoFilterSelect({ value, onChange, repos }: { value: string; onChange: (v: string) => void; repos: string[] }) {
  if (repos.length === 0 && !value) return null;
  return (
    <select className="select" title="依所屬倉庫篩選" value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">全部倉庫</option>
      <option value={NO_REPO}>未標記倉庫</option>
      {repos.length > 0 && <option disabled>──────</option>}
      {repos.map((r) => <option key={r} value={r}>{r}</option>)}
      {/* 篩選中的倉庫已經沒有 Issue 用了:仍列出來,選單才不會是空白 */}
      {value && value !== NO_REPO && !repos.some((r) => repoKey(r) === repoKey(value)) && <option value={value}>{value}</option>}
    </select>
  );
}

// MARK: - 程式碼倉庫設定的入口(Repositories 分頁用)

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
