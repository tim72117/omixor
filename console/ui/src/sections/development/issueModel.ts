// Issue 的型別與純邏輯(不碰 React、不碰網路,node 測試直接跑):顯示編號、專案篩選、版本衝突、專案代號驗證。
// 規則的來源是 core(DOC-10):顯示一律用 key;id 是內部 UUIDv7,不顯示;沒有 key(舊伺服器)時退回顯示 id。

import type { ActorView } from "../../types";

export type IssueComment = { id: string; author: string; authorActor?: ActorView; text: string; at: string };
export type Priority = "urgent" | "high" | "normal" | "low";
export type Issue = {
  id: string; title: string; body: string; status: "open" | "closed"; priority: Priority; labels: string[];
  source: string; link: string; assignee?: string; repo?: string; createdBy: string; createdAt: string; updatedAt: string; closedAt?: string | null; comments: IssueComment[];
  /** 顯示編號:組織層級 ISS-<n>、專案內 <代號>-<n>;舊伺服器沒有 */
  key?: string;
  /** 所屬專案 id(PRJ-<n>);空或沒有 = 組織層級 */
  project?: string;
  number?: number;
  /** 樂觀鎖版本:每次修改本體加一;修改時帶回去,別人已經改過就回 409 */
  version?: number;
  deletedAt?: string | null;
  deletedBy?: string;
  /** 同名字串欄位旁邊的 actor 物件(ISS-36 C-3);沒有或查不到就沒有 */
  createdByActor?: ActorView; assigneeActor?: ActorView; deletedByActor?: ActorView;
  /** 子項(issue_item)計數:未完成(todo / doing / blocked)與全部(不含 canceled);沒有子項沒有這兩個欄位(core 出口補,不存庫) */
  itemsOpen?: number; itemsTotal?: number;
};

/** 優先度的顯示文字與顏色(排序規則在核心,畫面不排) */
export const PRIORITY: Record<Priority, { label: string; color: string }> = {
  urgent: { label: "緊急", color: "red" }, high: { label: "高", color: "orange" },
  normal: { label: "一般", color: "blue" }, low: { label: "低", color: "gray" },
};
export const PRIORITIES: Priority[] = ["urgent", "high", "normal", "low"];
/** 緊急、高在列表與卡片上顯示標記;一般、低只用顏色 */
export const showPriorityPill = (p: Priority) => p === "urgent" || p === "high";

/** 畫面上顯示的編號:key 優先,沒有就退回 id */
export const issueKey = (i: Pick<Issue, "id" | "key">): string => i.key || i.id;

const same = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();

/** 依 id、目前的 key 找 Issue(不分大小寫);找不到回 undefined */
export function findIssue<T extends Pick<Issue, "id" | "key">>(issues: T[], ref: string | undefined): T | undefined {
  if (!ref) return undefined;
  return issues.find((i) => same(i.id, ref)) ?? issues.find((i) => !!i.key && same(i.key, ref));
}

/** 頻道、任務連結等只存一個 Issue 參照(id 或 key)的地方:找得到 Issue 就顯示它的 key,找不到原樣顯示 */
export const issueRefLabel = (ref: string, issues: Pick<Issue, "id" | "key">[]): string => {
  const i = findIssue(issues, ref);
  return i ? issueKey(i) : ref;
};

/** 任務的連結是不是一個 Issue 參照:UUID、ISS-n、已知專案代號的 <代號>-n,或清單裡找得到的。
 *  只長得像 <英文>-n 的(例如 Jira 的 JIRA-12、外部連結)不算:代號要是組織層級的 ISS、已知專案的代號(projectKeys),或清單裡 Issue 的 key 前綴 */
export const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export const KEY_RE = /^([A-Za-z][A-Za-z0-9]{0,9})-(\d+)$/;
export function isIssueRef(link: string, issues: Pick<Issue, "id" | "key">[] = [], projectKeys: string[] = []): boolean {
  if (findIssue(issues, link)) return true;
  if (/^(PRJ|T|DOC|CH|INC)-\d+$/i.test(link)) return false; // 專案、任務、文件、頻道、事故的編號
  if (UUID_RE.test(link)) return true;
  const m = KEY_RE.exec(link);
  if (!m) return false;
  const prefix = m[1].toUpperCase();
  return prefix === "ISS" || projectKeys.some((k) => k.toUpperCase() === prefix) || issues.some((i) => !!i.key && i.key.split("-")[0].toUpperCase() === prefix);
}

// ---- 專案篩選(對應後端 project 查詢參數)

/** "" = 全部、"-" = 只看組織層級、PRJ-n = 只看那個專案 */
export const PROJECT_ALL = "";
export const PROJECT_ORG = "-";

// 篩選與各狀態的數量一律以後端 issue_query / issue_board 回傳的為準(counts、total 已依 project 計算),畫面不再自己篩、自己算
export type Counts = Record<"open" | "closed" | "all", number>;

// ---- 版本衝突

type ErrLike = { status?: number; data?: Record<string, any> } | null | undefined;

/** 修改被擋下是不是版本衝突:看 ApiError.data 有沒有 current / currentVersion(不比對訊息文字);有明確的 HTTP 409 也算 */
export function isVersionConflict(e: unknown): boolean {
  const x = e as ErrLike;
  if (!x || typeof x !== "object") return false;
  const d = x.data;
  return x.status === 409 || typeof d?.currentVersion === "number" || (!!d && typeof d.current === "object" && d.current !== null);
}

/** 衝突時伺服器附的目前內容;沒附(或不是物件)回 undefined */
export function conflictCurrent(e: unknown): Issue | undefined {
  const c = (e as ErrLike)?.data?.current;
  return c && typeof c === "object" ? (c as Issue) : undefined;
}

export const CONFLICT_MESSAGE = "這張 Issue 已被別人更新";

/** 編輯中的欄位(使用者還沒送出的內容) */
export type Draft = { title: string; body: string; labels: string; repo: string; project: string };
export const splitLabels = (s: string) => s.split(",").map((x) => x.trim()).filter(Boolean);
export const draftOf = (i: Issue): Draft => ({ title: i.title, body: i.body ?? "", labels: (i.labels ?? []).join(", "), repo: i.repo ?? "", project: i.project ?? "" });

export const DRAFT_LABEL: Record<keyof Draft, string> = { title: "標題", body: "內文", labels: "標籤", repo: "倉庫", project: "專案" };

/** 欄位比較用的正規化(標題、倉庫、專案去頭尾空白;標籤去空白後比內容) */
const normField = (f: keyof Draft, d: Draft) => (f === "labels" ? splitLabels(d.labels).join(",") : f === "title" || f === "repo" || f === "project" ? d[f].trim() : d[f]);

export type MergeConflict = { field: keyof Draft; mine: string; theirs: string };

/**
 * 三方合併:origin = 草稿開啟(或上次合併)時的基準版本,draft = 使用者目前的草稿,latest = 別人更新後的最新內容。
 * 每個欄位:使用者沒改過(draft = origin)→ 自動採用最新值;只有使用者改、別人沒改 → 保留我的;兩邊改成一樣 → 不算差異;
 * 兩邊都改而且不同 → 保留我的,列在 conflicts 讓使用者決定。adopted 是自動換成最新值的欄位。
 */
export function mergeLatest(origin: Issue, draft: Draft, latest: Issue): { draft: Draft; conflicts: MergeConflict[]; adopted: (keyof Draft)[] } {
  const o = draftOf(origin), t = draftOf(latest);
  const merged = { ...draft };
  const conflicts: MergeConflict[] = [];
  const adopted: (keyof Draft)[] = [];
  for (const f of Object.keys(DRAFT_LABEL) as (keyof Draft)[]) {
    const mine = normField(f, draft), base = normField(f, o), theirs = normField(f, t);
    if (mine === base) {
      if (theirs !== base) { merged[f] = t[f]; adopted.push(f); }
    } else if (theirs !== base && theirs !== mine) {
      conflicts.push({ field: f, mine: draft[f], theirs: t[f] });
    }
  }
  return { draft: merged, conflicts, adopted };
}

/** 草稿和另一份 Issue(別人更新後的最新內容)不一樣的欄位(兩邊直接比,不看基準;合併請用 mergeLatest) */
export function draftDiff(draft: Draft, latest: Issue): { field: keyof Draft; mine: string; theirs: string }[] {
  const theirs = draftOf(latest);
  return (Object.keys(DRAFT_LABEL) as (keyof Draft)[])
    .filter((f) => normField(f, draft) !== normField(f, theirs))
    .map((f) => ({ field: f, mine: draft[f], theirs: theirs[f] }));
}

/** 草稿相對於基準版本有改的欄位,組成 issue_update 的參數(沒改的不送,免得蓋掉別人的修改) */
export function draftChanges(draft: Draft, base: Issue): Record<string, unknown> {
  const b = draftOf(base);
  const out: Record<string, unknown> = {};
  if (draft.title.trim() !== b.title.trim()) out.title = draft.title.trim();
  if (draft.body !== b.body) out.body = draft.body;
  if (splitLabels(draft.labels).join(",") !== splitLabels(b.labels).join(",")) out.labels = splitLabels(draft.labels);
  if (draft.repo.trim() !== b.repo) out.repo = draft.repo.trim();
  if (draft.project !== b.project) out.project = draft.project;
  return out;
}

// ---- 專案代號(同 core 的 model.CleanProjectKey:2 到 10 個大寫英數字、首字英文、不可用保留字;重複與「已有 Issue」由伺服器檢查)

export const RESERVED_PROJECT_KEYS = ["ISS", "PRJ", "T", "DOC", "CH", "INC"];

/** 輸入的代號整理成大寫、去掉前後空白 */
export const cleanProjectKey = (s: string) => s.trim().toUpperCase();

/** 格式有問題回說明,沒問題回 "";空字串合法(專案可以還沒有代號,只是不能放 Issue) */
export function projectKeyProblem(raw: string): string {
  const k = cleanProjectKey(raw);
  if (k === "") return "";
  if (!/^[A-Z]/.test(k)) return "代號的第一個字要是英文字母";
  if (!/^[A-Z0-9]+$/.test(k)) return "代號只能用英文字母與數字";
  if (k.length < 2 || k.length > 10) return "代號要 2 到 10 個字";
  if (RESERVED_PROJECT_KEYS.includes(k)) return `${k} 是系統保留字,不能當代號`;
  return "";
}

/** 專案底下有沒有 Issue(含垃圾桶裡的,伺服器會一起算;畫面只看得到清單裡的,所以只是提早唯讀的提示,最後以伺服器為準) */
export const projectHasIssues = (projectId: string, issues: Pick<Issue, "project">[]) => issues.some((i) => i.project === projectId);
