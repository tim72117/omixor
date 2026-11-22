// 看板與 Issue 分頁共用的 Issue 篩選:狀態放在 DevelopmentView(DevModel.issueFilters),切換分頁時保留。
// 篩選規則都在核心(issue_query / issue_board 的參數),這裡只負責 FilterBar 的膠囊定義。
import type { FilterDef, FilterOption } from "../../components/FilterBar";
import type { Project } from "../../models";
import { NO_REPO, repoKey } from "./devShared";
import { projectName } from "./IssueForm";
import { PRIORITIES, PRIORITY, PROJECT_ALL, PROJECT_ORG, type Issue } from "./issueModel";

export type IssueStatusFilter = "open" | "closed" | "all";
/** query:搜尋;status:只有 Issue 分頁用(看板依狀態分欄);label / priority / repo:"" = 全部;
 *  repo 的 NO_REPO = 未標記;project:""(PROJECT_ALL)全部、"-"(PROJECT_ORG)只組織層級、PRJ-n */
export type IssueFilters = { query: string; status: IssueStatusFilter; label: string; priority: Issue["priority"] | ""; repo: string; project: string };
export const ISSUE_FILTERS_DEFAULT: IssueFilters = { query: "", status: "open", label: "", priority: "", repo: "", project: PROJECT_ALL };

/** Issue 用到的標籤(去重:不分大小寫,保留第一次出現的寫法),依字母排序 */
export function usedLabels(issues: { labels?: string[] }[]): string[] {
  const m = new Map<string, string>();
  for (const i of issues) for (const l of i.labels ?? []) if (l && !m.has(l.toLowerCase())) m.set(l.toLowerCase(), l);
  return [...m.values()].sort((a, b) => a.localeCompare(b));
}

/** 標籤、優先度、專案、倉庫四個篩選膠囊(倉庫:沒有 Issue 標記倉庫、也沒在篩選時不顯示) */
export function issueFilterDefs(f: IssueFilters, set: (p: Partial<IssueFilters>) => void, { labels, repos, projects }: { labels: string[]; repos: string[]; projects: Project[] }): FilterDef[] {
  const labelOpts: FilterOption[] = labels.map((l) => ({ value: l, label: l }));
  // 篩選中的標籤 / 專案 / 倉庫已經沒有了:仍列出來,膠囊與選單才對得上
  if (f.label && !labels.some((l) => l.toLowerCase() === f.label.toLowerCase())) labelOpts.push({ value: f.label, label: f.label });
  const projectOpts: (FilterOption | "sep")[] = [{ value: PROJECT_ORG, label: "組織(未分專案)" }];
  if (projects.length > 0) projectOpts.push("sep", ...projects.map((p) => ({ value: p.id, label: projectName(p) })));
  if (f.project !== PROJECT_ALL && f.project !== PROJECT_ORG && !projects.some((p) => p.id === f.project)) projectOpts.push({ value: f.project, label: f.project });
  const defs: FilterDef[] = [
    { key: "label", label: "標籤", title: "依標籤篩選", allLabel: "全部標籤", options: labelOpts, value: f.label, onChange: (v) => set({ label: v }) },
    {
      key: "priority", label: "優先度", title: "依優先度篩選", allLabel: "全部優先度",
      options: PRIORITIES.map((p) => ({ value: p, label: PRIORITY[p].label })), value: f.priority, onChange: (v) => set({ priority: v as IssueFilters["priority"] }),
    },
    { key: "project", label: "專案", title: "依專案篩選", allLabel: "全部專案", options: projectOpts, value: f.project, defaultValue: PROJECT_ALL, onChange: (v) => set({ project: v }) },
  ];
  if (repos.length > 0 || f.repo) {
    const repoOpts: (FilterOption | "sep")[] = [{ value: NO_REPO, label: "未標記倉庫" }];
    if (repos.length > 0) repoOpts.push("sep", ...repos.map((r) => ({ value: r, label: r })));
    if (f.repo && f.repo !== NO_REPO && !repos.some((r) => repoKey(r) === repoKey(f.repo))) repoOpts.push({ value: f.repo, label: f.repo });
    defs.push({ key: "repo", label: "倉庫", title: "依所屬倉庫篩選", allLabel: "全部倉庫", options: repoOpts, value: f.repo, onChange: (v) => set({ repo: v }) });
  }
  return defs;
}
