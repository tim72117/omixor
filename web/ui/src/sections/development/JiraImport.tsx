// Issue › 從 Jira 匯入:設定 Jira 連線(Cloud 用 email + API token,Server/DC 用個人存取權杖),
// 選專案後把 Issue(含留言)匯入目前組織。資料:view jira;動作:jira_setup / jira_projects / jira_import。
// 再匯入一次會更新同一筆(用 Jira 網址比對),不會重複。只讀 Jira,不會改 Jira 上的東西。
import { useEffect, useState } from "react";
import { action, useRun, useView } from "../../api";
import { Banner, Check, SIcon } from "../../components/opsKit";

type JiraView = { configured: boolean; baseURL?: string; auth?: string; cloud?: boolean; loggedIn?: boolean; account?: string | null; error?: string | null };
type Project = { key: string; name: string };
type Result = { created: number; updated: number; comments: number; images: number; projects: Record<string, number>; errors: Record<string, string> };

const isCloudURL = (u: string) => /\.(atlassian\.net|jira\.com)(\/|$|:)/i.test(u.replace(/^https?:\/\//, ""));

export function JiraImport({ onDone }: { onDone: () => void }) {
  const [jira] = useView<JiraView>("jira", ["jira", "connections"]);
  const [editing, setEditing] = useState(false);
  if (!jira) return <div className="muted">讀取中…</div>;
  const ready = jira.configured && jira.loggedIn && !jira.error;
  return (
    <div className="col gap12">
      <div className="row gap8" style={{ alignItems: "center" }}>
        <SIcon name="square.and.arrow.down" size={16} />
        <span className="h3">從 Jira 匯入 Issue</span>
      </div>
      {ready && !editing ? (
        <>
          <div className="row gap8 caption muted" style={{ alignItems: "center" }}>
            <span>已連線 {jira.baseURL}{jira.account ? `(${jira.account})` : ""}</span>
            <button className="btn link" onClick={() => setEditing(true)}>更換連線</button>
          </div>
          <ProjectPicker onDone={onDone} />
        </>
      ) : (
        <SetupForm jira={jira} onSaved={() => setEditing(false)} onCancel={ready ? () => setEditing(false) : undefined} />
      )}
    </div>
  );
}

function SetupForm({ jira, onSaved, onCancel }: { jira: JiraView; onSaved: () => void; onCancel?: () => void }) {
  const [baseURL, setBaseURL] = useState(jira.baseURL ?? "");
  const [email, setEmail] = useState("");
  const [token, setToken] = useState("");
  const { busy, error, run } = useRun();
  const cloud = isCloudURL(baseURL);
  return (
    <div className="col gap8">
      {jira.configured && jira.error && <Banner>{jira.error}</Banner>}
      <label className="bx-field"><span>Jira 網址</span>
        <input className="input" placeholder="https://your-team.atlassian.net" value={baseURL} onChange={(e) => setBaseURL(e.target.value)} />
      </label>
      <label className="bx-field"><span>{cloud ? "登入用的 email" : "帳號 email(Jira Cloud 必填;Server / Data Center 用個人存取權杖時留空)"}</span>
        <input className="input" autoComplete="off" placeholder="me@example.com" value={email} onChange={(e) => setEmail(e.target.value)} />
      </label>
      <label className="bx-field"><span>{email.trim() ? "API token" : "API token / 個人存取權杖"}</span>
        <input className="input" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)}
          placeholder={jira.loggedIn ? "已設定,輸入新的會取代" : "存在本機(加密),不會顯示"} />
      </label>
      <div className="caption muted">
        {cloud
          ? <>Jira Cloud:到 <a href="https://id.atlassian.com/manage-profile/security/api-tokens" target="_blank" rel="noopener">Atlassian 帳號 › 安全性 › API token</a> 建立。</>
          : <>Server / Data Center:在 Jira 的「個人資料 › 個人存取權杖」建立。</>}
        {" "}只需要讀取權限;omixor 只讀 Jira,不會修改。
      </div>
      {error && <Banner>{error}</Banner>}
      <div className="row gap8">
        <button className="btn primary" disabled={busy || !baseURL.trim() || (!token.trim() && !jira.loggedIn)}
          onClick={() => run(async () => { await action("jira_setup", { baseURL, email, token }); setToken(""); onSaved(); })}>
          {busy ? "連線中…" : "連線"}
        </button>
        {onCancel && <button className="btn" onClick={onCancel}>取消</button>}
      </div>
    </div>
  );
}

function ProjectPicker({ onDone }: { onDone: () => void }) {
  const [projects, setProjects] = useState<Project[]>();
  const [picked, setPicked] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [includeClosed, setIncludeClosed] = useState(false);
  const [result, setResult] = useState<Result>();
  const load = useRun();
  const imp = useRun();
  useEffect(() => { void load.run(async () => setProjects(await action<Project[]>("jira_projects"))); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  if (result) {
    const errs = Object.entries(result.errors ?? {});
    return (
      <div className="col gap8">
        <div>匯入完成:新增 <b>{result.created}</b> 筆、更新 <b>{result.updated}</b> 筆,共 {result.comments} 則留言、{result.images ?? 0} 張圖片。</div>
        <div className="caption muted">{Object.entries(result.projects).map(([k, n]) => `${k} ${n} 筆`).join("、")}</div>
        {errs.map(([k, e]) => <Banner key={k}>{k}:{e}</Banner>)}
        <div className="row gap8">
          <button className="btn primary" onClick={onDone}>完成</button>
          <button className="btn" onClick={() => setResult(undefined)}>再匯入</button>
        </div>
      </div>
    );
  }
  const q = query.trim().toLowerCase();
  const list = (projects ?? []).filter((p) => !q || p.key.toLowerCase().includes(q) || p.name.toLowerCase().includes(q));
  const toggle = (k: string, on: boolean) => setPicked((s) => (on ? [...s, k] : s.filter((x) => x !== k)));
  return (
    <div className="col gap8">
      {load.error && <Banner>{load.error}</Banner>}
      {!projects ? <div className="muted">{load.busy ? "讀取專案中…" : ""}</div> : projects.length === 0 ? (
        <div className="muted">這個帳號看不到任何專案。</div>
      ) : (
        <>
          <input className="input" placeholder={`搜尋專案(共 ${projects.length} 個)`} value={query} onChange={(e) => setQuery(e.target.value)} />
          <div className="col gap4" style={{ maxHeight: 260, overflowY: "auto", border: "1px solid var(--line)", borderRadius: 6, padding: 8 }}>
            {list.map((p) => (
              <Check key={p.key} on={picked.includes(p.key)} onChange={(v) => toggle(p.key, v)}>
                <span className="mono">{p.key}</span> <span>{p.name}</span>
              </Check>
            ))}
            {list.length === 0 && <div className="caption muted">沒有符合的專案</div>}
          </div>
          <Check on={includeClosed} onChange={setIncludeClosed}>包含已完成的 Issue(匯入後是已關閉)</Check>
          <div className="caption muted">會匯入標題、說明、狀態、優先度、標籤、留言與圖片附件;再匯入一次會更新同一筆,不會重複。</div>
        </>
      )}
      {imp.error && <Banner>{imp.error}</Banner>}
      <div className="row gap8">
        <button className="btn primary" disabled={imp.busy || picked.length === 0}
          onClick={() => imp.run(async () => setResult(await action<Result>("jira_import", { projects: picked, includeClosed })))}>
          {imp.busy ? "匯入中…" : picked.length ? `匯入 ${picked.length} 個專案` : "選擇要匯入的專案"}
        </button>
        <button className="btn" onClick={onDone}>取消</button>
      </div>
    </div>
  );
}
