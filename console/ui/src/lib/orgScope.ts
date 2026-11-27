// 啟用 LDAP 而且選的不是系統工作組織時,整台一份的 view 後端一律 403(notSystemOrg,分類見 internal/web/orgscope.go)。
// 這裡讓畫面根本不發那些請求。不依賴 React(測試在 contract/orgScope.test.ts)。

/**
 * 任何組織都讀得到的 view:要和 internal/web/orgscope.go 的 orgViews、hostViews 一致
 * (internal/web/orgscope_test.go 比對,不同步時測試失敗;測試抓下面兩個 Set 括號裡的字串)。
 */
export const ANY_ORG_VIEWS = new Set<string>([
  "orgs", "account", "badges", "system", "issues", "issue_repos", "cabinet", "projects", "members",
  "org_chain", "channels", "agents", "incidents", "nav",
  "mcp", "me",
]);

/** 清單型的系統 view:在其他組織當作沒有資料(目錄、指令面板等共用元件照常運作) */
const LIST_VIEWS = new Set<string>([
  "people", "services", "servers", "repos", "pull_requests", "work_items", "pipeline_runs", "deployments", "alerts",
  "alert_rules", "logs", "dashboards", "on_call", "automations", "automation_runs", "approvals", "activity", "integrations",
]);

export type OrgScopeIndex = { perUser?: boolean; current?: string; system?: string };

export const outsideSystemOrg = (idx: OrgScopeIndex | undefined) => !!idx?.perUser && idx.current !== idx.system;

export const SYSTEM_ONLY_MESSAGE = "這個功能只在系統工作組織提供";

/**
 * 回傳 load(view 名稱, 實際的讀取):系統 view 在其他組織不發請求(清單型回空陣列,其他丟出 SYSTEM_ONLY_MESSAGE)。
 * readOrgs 讀 view「orgs」;讀成功的結果記著,invalidate() 之後重讀(組織、系統工作組織、登入的人變了)。
 */
export function createOrgScope(readOrgs: () => Promise<OrgScopeIndex>) {
  let known: Promise<boolean> | undefined;
  const outside = () => {
    if (known) return known;
    const asked = readOrgs().then(outsideSystemOrg);
    known = asked;
    return asked.catch(() => {
      if (known === asked) known = undefined; // 讀不到(例如還沒登入):下次再問,這次照常發請求
      return false;
    });
  };
  return {
    invalidate() {
      known = undefined;
    },
    async load<T>(name: string, read: () => Promise<T>): Promise<T | { data: unknown[] }> {
      if (ANY_ORG_VIEWS.has(name) || !(await outside())) return read();
      if (LIST_VIEWS.has(name)) return { data: [] };
      throw new Error(SYSTEM_ONLY_MESSAGE);
    },
  };
}
