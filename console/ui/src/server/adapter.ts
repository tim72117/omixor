// omixor server 的資料層:把畫面用的 view / action / tool 名稱(console 後端的 API)轉成核心 REST API(core/api)。
// 只有 api.ts 用。不依賴 React 與瀏覽器(http、組織選擇由呼叫端給;測試在 contract/serverAdapter.test.ts)。
// 權限、組織隔離、受限專案一律由伺服器判斷,這裡不過濾。畫面沒有後端的名稱:清單回空的,其他丟 404。
// 要用新的核心路由時,server/webauth.go 的 webRoutes 也要開放(瀏覽器登入預設拒絕)。
import { ApiError } from "../lib/http.ts";
import { decisionTool } from "./decisionTools.ts";

export type ServerHttp = {
  /** GET,回傳整個 JSON 回應 */
  get: (url: string) => Promise<any>;
  /** POST,回傳回應的 result */
  post: (url: string, body: unknown) => Promise<any>;
};

export type ServerDeps = {
  http: ServerHttp;
  /** 這個分頁選的組織(lib/orgSelection.ts) */
  org: { selected: () => string; select: (id: string) => void; reload: () => void };
  user: () => { id: string; name: string; email?: string } | undefined;
  origin: string;
};

type Org = { id: string; name: string; symbol?: string; color?: string; note?: string; myRole?: string };
type Args = Record<string, any>;

/** 沒有後端的清單型 view(共用元件照常運作) */
const EMPTY_LISTS = new Set(["automations", "services", "servers", "repos", "pull_requests", "work_items"]);

/** 會改資料的 action / tool(api.ts 做完後重讀畫面;讀取的不能列在這裡,否則會一直重讀) */
const WRITES = new Set([
  "issue_create", "issue_update", "issue_accept", "issue_delete", "issue_restore", "issue_purge",
  "issue_item_claim", "issue_item_report", "issue_item_update",
  "issue_decision_add", "issue_decision_answer", "issue_decision_to_issue", "issue_decision_withdraw", "issue_decision_reopen",
]);

const unavailable = (name: string) => new ApiError(`這台伺服器沒有提供這個功能(${name})`, 404);

const seg = (s: unknown) => encodeURIComponent(String(s ?? ""));

function query(args: Args, keys: string[]): string {
  const q = new URLSearchParams();
  for (const k of keys) if (typeof args[k] === "string" && args[k]) q.set(k, args[k]);
  const s = q.toString();
  return s ? "?" + s : "";
}

/** 只送有給的欄位 */
export function pick(args: Args, keys: string[]): Args {
  const out: Args = {};
  for (const k of keys) if (args[k] !== undefined) out[k] = args[k];
  return out;
}

const issue = (i: any) => ({ ...i, labels: i?.labels ?? [], comments: i?.comments ?? [] });
const issues = (l: any) => (Array.isArray(l) ? l.map(issue) : []);

/** 頭像底色:由帳號算出固定的顏色 */
const colorOf = (s: string) => [0x3b82f6, 0x10b981, 0xf59e0b, 0x8b5cf6, 0xef4444, 0x14b8a6][[...s].reduce((n, c) => n + c.charCodeAt(0), 0) % 6];

export type Call = {
  /** /api/orgs/<目前組織><path> */
  org: (path: string) => Promise<string>;
  get: (path: string) => Promise<any>;
  post: (path: string, body?: unknown) => Promise<any>;
};

export function createServerAdapter({ http, org, user, origin }: ServerDeps) {
  let known: Promise<Org[]> | undefined;

  const orgList = () => {
    if (known) return known;
    const asked: Promise<Org[]> = http.get("/api/orgs").then((b) => b?.result ?? []);
    known = asked;
    asked.catch(() => { if (known === asked) known = undefined; });
    return asked;
  };

  const current = async (): Promise<Org> => {
    const list = await orgList();
    const o = list.find((x) => x.id === org.selected()) ?? list[0];
    if (!o) throw new ApiError("還沒有加入任何組織", 403);
    if (!org.selected()) org.select(o.id);
    return o;
  };

  const call: Call = {
    org: async (path) => `/api/orgs/${seg((await current()).id)}${path}`,
    get: async (path) => (await http.get(await call.org(path)))?.result,
    post: async (path, body = {}) => http.post(await call.org(path), body),
  };

  const allIssues = async (q = "") => issues(await call.get("/issues" + q));

  async function view(name: string): Promise<unknown> {
    const u = user();
    const self = u?.email || u?.id || "";
    if (EMPTY_LISTS.has(name)) return [];
    switch (name) {
      case "orgs": {
        const list = await orgList();
        const selected = org.selected();
        // 選的組織不能用了(被移出、刪除):換一個並重新載入,不留它的快取
        if (selected && !list.some((o) => o.id === selected)) {
          org.select(list[0]?.id ?? "");
          org.reload();
        }
        const cur = list.find((o) => o.id === selected)?.id ?? list[0]?.id ?? "";
        // perUser:組織是每個分頁自己的選擇;system = current:沒有「系統工作組織」這件事
        return { current: cur, system: cur, perUser: true, orgs: list.map((o) => ({ ...o, symbol: o.symbol || "building.2", color: o.color || "blue" })) };
      }
      case "account": {
        const v = await http.get("/api/version");
        return { localStorage: "postgres", self, features: [...(v?.features ?? []), "issue_decision_withdraw", "issue_decision_reopen"] };
      }
      case "me": return { id: self, name: u?.name || self, signedIn: true };
      case "mcp": return { status: { state: "listening" }, url: origin + "/mcp" };
      case "nav": return { section: "development", env: "production", timeRange: "24h" };
      case "members": {
        const admin = (await current()).myRole === "admin";
        return { canEdit: admin, orgAdmin: admin };
      }
      case "system": {
        const o = await current();
        const v = await call.get("/system");
        return { ...v, org: o.id, canEdit: false, source: "core", error: "", notice: { seq: 0, text: "" }, alwaysOn: [], modules: [] };
      }
      case "people": {
        const l: any[] = (await call.get("/actors?kind=user&status=all")) ?? [];
        return l.map((a) => ({ id: a.handle, name: a.name || a.handle, role: "", colorHex: colorOf(a.handle) }));
      }
      case "agents": return { agents: (await call.get("/agents")) ?? [], tasks: [] };
      case "projects": return ((await call.get("/projects")) ?? []).map((p: any) => ({ ...p, repos: p.repos ?? [] }));
      case "issues": return allIssues();
      case "issue_repos": {
        const used = new Map<string, string>();
        for (const i of await allIssues()) if (i.repo && !used.has(i.repo.toLowerCase())) used.set(i.repo.toLowerCase(), i.repo);
        return { github: [], gitlab: [], used: [...used.values()] };
      }
      case "badges": {
        const n = (await allIssues()).filter((i) => i.status === "open" && (i.priority === "urgent" || i.priority === "high")).length;
        return n ? { development: n } : {};
      }
    }
    throw unavailable(name);
  }

  const one = (id: unknown, rest = "") => `/issues/${seg(id)}${rest}`;
  /** 子項 key 是「<Issue key>.<序號>」 */
  const item = (id: unknown, rest: string) => one(String(id).slice(0, String(id).lastIndexOf(".")), `/items/${seg(id)}/${rest}`);

  async function run(name: string, args: Args): Promise<unknown> {
    switch (name) {
      // 只在瀏覽器的狀態
      case "navigate":
      case "org_switch": return {};
      case "issue_query": {
        // counts、total 只看專案範圍,其他條件不影響(同 console 的 issue_query)
        const all = await allIssues(query(args, ["project"]));
        const list = await allIssues(query(args, ["q", "status", "priority", "label", "repo", "assignee", "project", "sort"]));
        const count = (s: string) => all.filter((i) => i.status === s).length;
        return { issues: list, total: all.length, counts: { all: all.length, open: count("open"), closed: count("closed") } };
      }
      case "issue_board": {
        const b = await call.get("/issues/board" + query(args, ["q", "priority", "label", "repo", "assignee", "project"]));
        return { ...b, labels: b?.labels ?? [], columns: (b?.columns ?? []).map((c: any) => ({ ...c, issues: issues(c.issues) })) };
      }
      case "issue_trash": return allIssues("?deleted=1");
      case "issue_resolve": {
        const i = await call.get(one(args.ref));
        return { id: i.id, key: i.key, deleted: !!i.deletedAt };
      }
      case "issue_delete": return call.post(one(args.id, "/delete"));
      case "issue_restore": return issue(await call.post(one(args.id, "/restore")));
      case "issue_purge": return call.post(one(args.id, "/purge"));
      case "issue_create":
        return issue(await call.post("/issues", pick(args, ["title", "body", "priority", "labels", "repo", "project"])));
      case "issue_update": {
        const patch = pick(args, ["title", "body", "status", "priority", "labels", "repo", "project", "assignee", "version"]);
        let out: any;
        if (Object.keys(patch).some((k) => k !== "version")) out = await call.post(one(args.id), patch);
        if (typeof args.comment === "string") out = await call.post(one(args.id, "/comments"), { text: args.comment });
        return out && issue(out);
      }
      case "issue_accept": return issue(await call.post(one(args.id, args.release ? "/release" : "/accept")));
      case "issue_items": {
        const items: any[] = (await call.get(one(args.issue, "/items" + (args.include_done ? "?includeDone=1" : "")))) ?? [];
        return { issue: args.issue, total: items.length, open: items.filter((i) => i.status !== "done" && i.status !== "canceled").length, items };
      }
      case "issue_item_claim": return call.post(item(args.id, "claim"));
      case "issue_item_report":
        return call.post(item(args.id, "report"), {
          ...pick(args, ["status", "evidence", "note"]),
          ...(args.block_reason ? { blockReason: args.block_reason } : {}),
          ...(args.wait_for_kind ? { waitForKind: args.wait_for_kind } : {}),
          ...(args.wait_for ? { waitFor: args.wait_for } : {}),
        });
      case "issue_item_update":
        if (args.cancel === true) return call.post(item(args.id, "cancel"));
        break;
    }
    if (name.startsWith("issue_decision")) {
      const r = await decisionTool(call, name, args);
      if (r !== undefined) return r;
    }
    throw unavailable(name);
  }

  return {
    /** 下次重新讀組織清單(定期重讀、寫入之後) */
    invalidate() {
      known = undefined;
    },
    /** GET /api/view/<名稱> */
    async get(url: string): Promise<unknown> {
      if (url.startsWith("/api/view/")) return view(url.slice("/api/view/".length));
      throw unavailable(url);
    },
    /** POST /api/action/<名稱>、/api/tool/<名稱>、/api/attachments;write = 做完要重讀畫面 */
    async post(url: string, args: unknown): Promise<{ data: unknown; write: boolean }> {
      const a = (args ?? {}) as Args;
      if (url === "/api/attachments") {
        const r = await call.post("/attachments", { data: a.data });
        return { data: { url: "/api/attachments/" + r.id }, write: false };
      }
      const m = /^\/api\/(?:action|tool)\/([a-z0-9_]+)$/.exec(url);
      if (!m) throw unavailable(url);
      return { data: await run(m[1], a), write: WRITES.has(m[1]) };
    },
  };
}
