// 待決定 / 需要協助的純邏輯(不碰 React、不碰網路,node 測試直接跑):篩選、排序、計數、選項整理、權限判斷、送出的參數、錯誤訊息。
// 呼叫在 decisionApi.ts。資料形狀是 core/model/decision.go(型別在 models/domain.ts,這裡只用型別匯入)。
import type { Decision, DecisionKind, DecisionOption, DecisionStatus } from "../../models/domain";

export type KindFilter = "all" | DecisionKind;
export type StatusFilter = "all" | DecisionStatus;
export type DecisionFilters = { kind: KindFilter; status: StatusFilter; mine: boolean; issue: string };
/** 預設:全部種類、待處理 */
export const DECISION_FILTERS_DEFAULT: DecisionFilters = { kind: "all", status: "open", mine: false, issue: "" };

export const KIND_LABEL: Record<DecisionKind, string> = { decision: "待決定", help: "需要協助" };
export const KIND_COLOR: Record<DecisionKind, string> = { decision: "indigo", help: "orange" };
/** 單次讀取的上限(issue_decisions 的 limit 最大 200);超過時畫面提示 */
export const DECISION_LIMIT = 200;
export const MAX_OPTIONS = 12;
export const MAX_TITLE = 200;
export const MAX_TEXT = 20000;

export const statusLabel = (d: Pick<Decision, "status" | "kind">): string =>
  d.status === "open" ? "待處理" : d.status === "withdrawn" ? "已撤回" : d.kind === "help" ? "已處理" : "已決定";
export const STATUS_COLOR: Record<DecisionStatus, string> = { open: "orange", decided: "green", withdrawn: "secondary" };

// MARK: - 身分與權限(後端仍會再檢查,這裡只決定按鈕顯示 / 是否可按)

/** 我是誰(handle 或 email,不分大小寫);管理員身分來自 members view 的 canEdit */
export type Viewer = { me?: string; isAdmin: boolean; isHuman: boolean };
const norm = (s?: string) => s?.trim().toLowerCase() ?? "";
type ActorLike = { handle?: string; email?: string; kind?: string } | undefined;
const sameActor = (me: string | undefined, actor: ActorLike, compat?: string): boolean => {
  const m = norm(me);
  if (!m) return false;
  if (actor?.kind === "agent") return false;
  return [actor?.handle, actor?.email, compat].some((v) => norm(v) === m);
};
export const isCreator = (d: Pick<Decision, "createdBy" | "createdByActor">, v: Viewer) => sameActor(v.me, d.createdByActor, d.createdBy);
export const isDecider = (d: Pick<Decision, "decidedBy" | "decidedByActor">, v: Viewer) => sameActor(v.me, d.decidedByActor, d.decidedBy);

/** 一個動作的許可:ok 時可按;否則 reason 是按鈕 tooltip 的說明 */
export type Perm = { ok: true } | { ok: false; reason: string };
const yes: Perm = { ok: true };
const no = (reason: string): Perm => ({ ok: false, reason });

export function canAnswer(d: Pick<Decision, "status">, v: Viewer): Perm {
  if (!v.isHuman) return no("只有人可以處理(角色憑證不行)");
  if (d.status !== "open") return no("已經處理或撤回,不能再回答;要改請先重新開啟");
  return yes;
}
/** 撤回:建立者或組織管理員,而且還在待處理 */
export function canWithdraw(d: Pick<Decision, "status" | "createdBy" | "createdByActor">, v: Viewer): Perm {
  if (d.status !== "open") return no("只有待處理的可以撤回");
  if (v.isAdmin || isCreator(d, v)) return yes;
  return no("只有提出者或組織管理員可以撤回");
}
/** 重新開啟:組織管理員或原處理人,而且已處理 */
export function canReopen(d: Pick<Decision, "status" | "decidedBy" | "decidedByActor">, v: Viewer): Perm {
  if (d.status !== "decided") return no("只有已處理的可以重新開啟");
  if (v.isAdmin || isDecider(d, v)) return yes;
  return no("只有組織管理員或原處理人可以重新開啟");
}
/** 由它開 Issue:只有人,而且還沒有來源 Issue */
export function canToIssue(d: Pick<Decision, "sourceIssue" | "status">, v: Viewer): Perm {
  if (!v.isHuman) return no("只有人可以由它開 Issue");
  if (d.sourceIssue) return no("已經有來源 Issue(" + d.sourceIssue + ")");
  if (d.status === "withdrawn") return no("已撤回的不能開 Issue");
  return yes;
}

/** console 是否提供撤回 / 重新開啟:console 登記了 issue_decision_withdraw / issue_decision_reopen 工具就在 account features 宣告,偵測不到(舊 console)就停用並說明 */
export const FEATURE_DECISION_WITHDRAW = "issue_decision_withdraw";
export const FEATURE_DECISION_REOPEN = "issue_decision_reopen";
export const supportsFeature = (features: readonly string[] | null | undefined, f: string) => !!features?.includes(f);
export const UNSUPPORTED_REASON = "這台伺服器的 console 還沒提供這個動作";

// MARK: - 篩選、排序、計數

/** 「待我處理」:待處理的(人類帳號全部都能處理);和後端 mine=true 一致 */
export const isMine = (d: Pick<Decision, "status">, v: Viewer): boolean => d.status === "open" && v.isHuman;

const sameIssue = (a: string, b: string) => norm(a) === norm(b);

export function filterDecisions(list: Decision[], f: DecisionFilters, v: Viewer): Decision[] {
  const issue = f.issue.trim();
  return list.filter((d) =>
    (f.kind === "all" || d.kind === f.kind) &&
    (f.status === "all" || d.status === f.status) &&
    (!f.mine || isMine(d, v)) &&
    (!issue || sameIssue(d.sourceIssue, issue)));
}

/** 待處理在前;同狀態依建立時間新到舊;再依編號 */
export function sortDecisions(list: Decision[]): Decision[] {
  const rank = (d: Decision) => (d.status === "open" ? 0 : d.status === "decided" ? 1 : 2);
  const num = (id: string) => Number(id.replace(/^\D+/, "")) || 0;
  return [...list].sort((a, b) => rank(a) - rank(b) || Date.parse(b.createdAt) - Date.parse(a.createdAt) || num(b.id) - num(a.id));
}

export type StatusCounts = Record<DecisionStatus | "all", number>;
export function countByStatus(list: Pick<Decision, "status">[]): StatusCounts {
  const c: StatusCounts = { open: 0, decided: 0, withdrawn: 0, all: list.length };
  for (const d of list) c[d.status]++;
  return c;
}

/** 某張 Issue 的決定摘要(Issue 詳情用) */
export function issueSummary(list: Pick<Decision, "status" | "sourceIssue">[], issueKey: string): StatusCounts {
  return countByStatus(list.filter((d) => sameIssue(d.sourceIssue, issueKey)));
}
export function summaryText(c: StatusCounts): string {
  if (c.all === 0) return "";
  const parts = [`待處理 ${c.open}`, `已處理 ${c.decided}`];
  if (c.withdrawn > 0) parts.push(`已撤回 ${c.withdrawn}`);
  return parts.join(" · ");
}

/** 有來源 Issue 的分組(依 key;沒有來源的放最後,key 是 "") */
export function groupBySource<T extends Pick<Decision, "sourceIssue">>(list: T[]): { issue: string; items: T[] }[] {
  const m = new Map<string, T[]>();
  for (const d of list) { const k = d.sourceIssue || ""; (m.get(k) ?? m.set(k, []).get(k)!).push(d); }
  return [...m.entries()].map(([issue, items]) => ({ issue, items })).sort((a, b) => (a.issue === "" ? 1 : b.issue === "" ? -1 : a.issue.localeCompare(b.issue)));
}

// MARK: - 時間與顯示文字

/** 相對時間(繁中):剛剛 / N 分鐘前 / N 小時前 / N 天前 / N 個月前;未來的時間視為剛剛;無效回 "—" */
export function relativeTimeZh(iso: string | undefined | null, now: number = Date.now()): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "—";
  const s = Math.max(0, (now - t) / 1000);
  if (s < 60) return "剛剛";
  if (s < 3600) return Math.floor(s / 60) + " 分鐘前";
  if (s < 86400) return Math.floor(s / 3600) + " 小時前";
  if (s < 86400 * 30) return Math.floor(s / 86400) + " 天前";
  if (s < 86400 * 365) return Math.floor(s / (86400 * 30)) + " 個月前";
  return Math.floor(s / (86400 * 365)) + " 年前";
}

/** 選項摘要:「A、B(建議)、C」;超過 max 個收成「…等 N 項」;help 沒有選項回 "" */
export function optionSummary(options: DecisionOption[] | undefined, max = 3): string {
  const list = options ?? [];
  const shown = list.slice(0, max).map((o) => o.label + (o.recommended ? "(建議)" : ""));
  return shown.join("、") + (list.length > max ? `…等 ${list.length} 項` : "");
}

/** 回答的顯示文字:decision 的選項 id → 選項標籤(other → 其他);help 的 done / declined → 已完成 / 婉拒 */
export function answerLabel(d: Pick<Decision, "kind" | "answer" | "options">): string {
  if (!d.answer) return "";
  if (d.kind === "help") return d.answer === "done" ? "已完成" : d.answer === "declined" ? "婉拒" : d.answer;
  if (d.answer === "other") return "其他";
  return (d.options ?? []).find((o) => o.id === d.answer)?.label ?? d.answer;
}

/** 提出者、處理人的顯示名稱:actor 物件優先,其次相容字串 */
export const whoLabel = (actor?: { name?: string; handle?: string }, compat?: string): string => actor?.name || actor?.handle || compat || "—";

// MARK: - 提出新的待決定

export type OptionDraft = { label: string; description: string; recommended: boolean };
export const emptyOption = (): OptionDraft => ({ label: "", description: "", recommended: false });

/** 整理選項草稿:去掉沒填標籤的、標籤去頭尾空白、重複標籤只留第一個;recommended 最多一個(留第一個) */
export function normalizeOptions(drafts: OptionDraft[]): { label: string; description?: string; recommended?: boolean }[] {
  const seen = new Set<string>();
  let rec = false;
  const out: { label: string; description?: string; recommended?: boolean }[] = [];
  for (const d of drafts) {
    const label = d.label.trim();
    if (!label || seen.has(label.toLowerCase())) continue;
    seen.add(label.toLowerCase());
    const o: { label: string; description?: string; recommended?: boolean } = { label };
    const desc = d.description.trim();
    if (desc) o.description = desc;
    if (d.recommended && !rec) { o.recommended = true; rec = true; }
    out.push(o);
  }
  return out;
}

export type NewDraft = { kind: DecisionKind; title: string; body: string; options: OptionDraft[]; sourceIssue: string };
export const emptyDraft = (sourceIssue = ""): NewDraft => ({ kind: "decision", title: "", body: "", options: [emptyOption(), emptyOption()], sourceIssue });

/** 檢查草稿:回錯誤說明(空字串 = 可送出) */
export function draftProblem(d: NewDraft): string {
  const title = d.title.trim();
  if (!title) return "請填標題";
  if (title.length > MAX_TITLE) return `標題最多 ${MAX_TITLE} 字`;
  if (d.body.length > MAX_TEXT) return `內文最多 ${MAX_TEXT} 字`;
  if (d.kind === "decision") {
    const n = normalizeOptions(d.options).length;
    if (n < 2) return "待決定至少要有 2 個不同的選項";
    if (n > MAX_OPTIONS) return `選項最多 ${MAX_OPTIONS} 個`;
  }
  const src = d.sourceIssue.trim();
  if (src && !/^[A-Za-z][A-Za-z0-9]{0,9}-\d+$/.test(src)) return "來源 Issue 要是編號(例如 ISS-37)";
  return "";
}

/** issue_decision_add 的參數(help 不帶 options;來源 Issue 統一大寫) */
export function buildAddArgs(d: NewDraft): Record<string, unknown> {
  const args: Record<string, unknown> = { kind: d.kind, title: d.title.trim() };
  if (d.body.trim()) args.body = d.body.trim();
  if (d.kind === "decision") args.options = normalizeOptions(d.options);
  if (d.sourceIssue.trim()) args.source_issue = d.sourceIssue.trim().toUpperCase();
  return args;
}

// MARK: - 回答

/** 回答的選擇:decision 是選項 id 或 "other";help 是 "done" 或 "declined" */
export type AnswerInput = { choice: string; note: string; evidence: string };

/** 檢查回答:回錯誤說明(空字串 = 可送出)。other 與 declined 一定要附說明 */
export function answerProblem(d: Pick<Decision, "kind" | "options">, a: AnswerInput): string {
  if (!a.choice) return d.kind === "help" ? "請選擇完成或婉拒" : "請選一個選項,或選「其他」";
  if (d.kind === "decision" && a.choice !== "other" && !d.options.some((o) => o.id === a.choice)) return "沒有這個選項";
  if (d.kind === "help" && a.choice !== "done" && a.choice !== "declined") return "回答只能是完成或婉拒";
  if ((a.choice === "other" || a.choice === "declined") && !a.note.trim()) return a.choice === "other" ? "選「其他」要寫說明" : "婉拒要寫原因";
  if (a.note.length > MAX_TEXT || a.evidence.length > MAX_TEXT) return `文字最多 ${MAX_TEXT} 字`;
  return "";
}

/** issue_decision_answer 的參數(evidence 只有 help 完成時帶) */
export function buildAnswerArgs(d: Pick<Decision, "id" | "kind">, a: AnswerInput): Record<string, unknown> {
  const args: Record<string, unknown> = { id: d.id, answer: a.choice };
  if (a.note.trim()) args.note = a.note.trim();
  if (d.kind === "help" && a.choice === "done" && a.evidence.trim()) args.evidence = a.evidence.trim();
  return args;
}

// MARK: - 錯誤訊息

/** 後端的錯誤整理成一句清楚的話:403 沒權限、404 找不到(也可能在受限專案看不到)、409 狀態已變;其他原樣顯示後端訊息 */
export function errorMessage(e: unknown): string {
  const status = (e as { status?: number })?.status ?? 0;
  const msg = String((e as { message?: string })?.message ?? e ?? "").trim();
  const tail = msg ? "(" + msg + ")" : "";
  if (status === 403) return "沒有權限做這個動作" + tail;
  if (status === 404) return "找不到這筆待決定,可能已被移除或你看不到它" + tail;
  if (status === 409) return "狀態已經變了(別人可能剛處理過),已重新讀取" + tail;
  return msg || "操作失敗";
}
