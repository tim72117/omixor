// 設定 › 程式碼倉庫:GitHub 與 GitLab 的連線、登入與追蹤的 repo / 專案(原本在開發進度的 GitHub、GitLab 分頁裡)。
// 開發進度那兩個分頁只看資料(執行紀錄、PR/MR、Pipeline),沒設定好時用「前往設定」跳到這裡(focusId "repos")。
// GitHub 的設定元件在這個檔;GitLab 的在 gitlab/gitlabSettings.tsx(GitLab 只有 web 版)。
// 資料:view github、view gitlab;動作:github_ensure_connection、github_add_repo、github_remove_repo、gitlab_*、connection_*(見 docs/PORTING.md)。
import { useState } from "react";
import { action, useRun, useView } from "../../api";
import { Card, ErrorText } from "../../components/ui";
import { Banner, copyText, SIcon, useAct } from "../../components/opsKit";
import { GitLabProjects, GitLabSetup, InsecureBanner, LogoutRow, ServerCard } from "../../gitlab/gitlabSettings";
import type { GitLabView } from "../../gitlab/GitLabTab";
import type { Connection, GitHubView } from "../development/DevGitHubTab";
import { Form, Row, Section } from "./form";

const HOMEPAGE = "http://127.0.0.1";
const CALLBACK = "http://127.0.0.1/callback";
const REGISTER_URL = `https://github.com/settings/applications/new?oauth_application[name]=omixor&oauth_application[url]=${encodeURIComponent(HOMEPAGE)}&oauth_application[callback_url]=${encodeURIComponent(CALLBACK)}`;
const openURL = (u: string) => u && window.open(u, "_blank", "noopener");

export function SettingsRepos() {
  return (
    <Form>
      <GitHubPane />
      <GitLabPane />
    </Form>
  );
}

/** 未登入時的狀態列 */
function NotConnectedRow({ text }: { text: string }) {
  return (
    <Row>
      <SIcon name="person.crop.circle" className="muted" />
      <span className="grow muted">{text}</span>
    </Row>
  );
}

// MARK: - GitHub

function GitHubPane() {
  const [gh, err] = useView<GitHubView>("github", ["connections"]);
  const loggedIn = !!gh?.connection && !!gh.loggedIn;
  return (
    <>
      <Section header="GitHub" icon="chevron.left.forwardslash.chevron.right"
        footer="omixor 只讀 GitHub:看追蹤中 repo 的 Actions 執行與開著的 PR,不會開 PR、留言或重跑。資料在 開發進度 › GitHub。">
        {!gh ? <Row><span className="muted">{err ?? "讀取中…"}</span></Row>
          : loggedIn ? <GitHubAccountRow gh={gh} />
            : <NotConnectedRow text={!gh.connection ? "還沒設定" : "還沒登入"} />}
      </Section>
      <ErrorText>{gh ? err : undefined}</ErrorText>
      {gh && !loggedIn && <GitHubSetup gh={gh} />}
      {gh && loggedIn && <GitHubRepos gh={gh} />}
    </>
  );
}

/** 已登入時的帳號列:帳號、登入方式與登出 */
function GitHubAccountRow({ gh }: { gh: GitHubView }) {
  const { busy, run } = useRun();
  const conn = gh.connection!;
  return (
    <Row>
      <SIcon name="person.crop.circle.badge.checkmark" color="green" />
      <div className="col grow" style={{ minWidth: 0 }}>
        <span>{gh.state?.account ?? "已登入"}</span>
        <span className="caption muted">登入方式:{conn.auth === "bearer" ? "個人存取權杖" : "GitHub OAuth"}</span>
      </div>
      <button className="btn small" disabled={busy} onClick={() => run(() => action("connection_logout", { id: conn.id }))}>登出</button>
    </Row>
  );
}

/** 追蹤的 repo:清單(在 GitHub 打開、停止追蹤)與新增。repo 清單是組織的設定。 */
function GitHubRepos({ gh }: { gh: GitHubView }) {
  const [newRepo, setNewRepo] = useState("");
  const [repoError, setRepoError] = useState<string>();
  const { run, toastEl } = useAct();
  const repos = gh.settings?.repos ?? [];
  const addRepo = async () => {
    try {
      await action("github_add_repo", { repo: newRepo.trim() });
      setNewRepo(""); setRepoError(undefined);
      action("github_refresh").catch(() => undefined);
    } catch (e: any) { setRepoError(String(e?.message ?? e)); }
  };
  return (
    <Section header="追蹤的 repo" icon="shippingbox" footer="輸入 owner/name(例如 tim72117/evoleap)。追蹤的 repo 是組織的設定,組織內的人都會看到;token 只存在這台。">
      {repos.length === 0 && <Row><span className="muted">還沒追蹤任何 repo</span></Row>}
      {repos.map((r) => (
        <Row key={r}>
          <SIcon name="shippingbox" size={13} className="muted" />
          <span className="grow ellipsis" style={{ minWidth: 0 }}>{r}</span>
          <button className="btn small link" onClick={() => openURL(`https://github.com/${r}`)}>在 GitHub 打開</button>
          <button className="btn small danger" onClick={() => run(() => action("github_remove_repo", { repo: r }))}>停止追蹤</button>
        </Row>
      ))}
      <Row>
        <input className="input grow" style={{ minWidth: 0 }} placeholder="owner/name" value={newRepo} onChange={(e) => setNewRepo(e.target.value)} onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && newRepo.trim() && addRepo()} />
        <button className="btn" disabled={!newRepo.trim()} onClick={addRepo}>加入</button>
      </Row>
      {repoError && <Row><span className="caption" style={{ color: "var(--red)" }}>{repoError}</span></Row>}
      {!repoError && gh.settingsError && <Row><span className="caption" style={{ color: "var(--orange)" }}>組織設定:{gh.settingsError}</span></Row>}
      {toastEl}
    </Section>
  );
}

/** 還沒設定或沒登入時的設定流程(OAuth App 或個人存取權杖) */
function GitHubSetup({ gh }: { gh: GitHubView }) {
  const conn = gh.connection;
  const st = gh.state;
  const [clientId, setClientId] = useState(conn?.oauth?.clientId ?? "");
  const [secret, setSecret] = useState("");
  const [useToken, setUseToken] = useState(false);
  const { busy, error, run } = useRun();
  const login = (id: string) => run(() => action("connection_login", { id }));
  const copyRow = (label: string, value: string) => (
    <div className="row gap8 wrap">
      <span className="caption muted" style={{ width: 180 }}>{label}</span>
      <span className="mono" style={{ userSelect: "text", wordBreak: "break-all" }}>{value}</span>
      <button className="ops-icon-btn" title="複製" onClick={() => copyText(value)}><SIcon name="doc.on.doc" size={12} /></button>
    </div>
  );
  return (
    <div className="col" style={{ gap: 12 }}>
      <div className="caption muted">連上後,開發進度 › GitHub 會列出追蹤中 repo 的 Actions 執行(含失敗的步驟與紀錄)和開著的 PR(含檢查結果);虛擬角色也能透過 MCP 讀。</div>
      {!conn ? (
        <button className="btn primary" style={{ alignSelf: "flex-start" }} disabled={busy} onClick={() => run(() => action("github_ensure_connection"))}>開始設定</button>
      ) : conn.auth === "oauth" ? (
        !conn.oauth?.clientId || !conn.oauth?.hasClientSecret ? (
          <Card>
            <div className="card-title">1. 在 GitHub 註冊一個 OAuth App(只要做一次)</div>
            <div className="muted">按下面的按鈕會打開 GitHub 的「New OAuth App」頁。名稱和網址會自動帶入;如果沒帶到,照下面填:</div>
            {copyRow("Application name", "omixor")}
            {copyRow("Homepage URL", HOMEPAGE)}
            {copyRow("Authorization callback URL", CALLBACK)}
            <div className="caption muted">「Enable Device Flow」不用勾。按 Register application 後,在 App 頁按「Generate a new client secret」。</div>
            <button className="btn" style={{ alignSelf: "flex-start" }} onClick={() => openURL(REGISTER_URL)}>打開 GitHub 註冊頁</button>
            <div className="divider" />
            <div className="card-title">2. 把 Client ID 和 Client secret 貼到這裡</div>
            <input className="input" placeholder="Client ID" value={clientId} onChange={(e) => setClientId(e.target.value)} />
            <input className="input" type="password" placeholder="Client secret(存在鑰匙圈)" value={secret} onChange={(e) => setSecret(e.target.value)} />
            <button className="btn primary" style={{ alignSelf: "flex-start" }} disabled={busy || !clientId.trim() || (!secret && !conn.oauth?.hasClientSecret)} onClick={() => run(async () => {
              await action("connection_upsert", { ...conn, oauth: { ...conn.oauth, clientId: clientId.trim() } });
              if (secret.trim()) await action("connection_secret", { id: conn.id, secret: secret.trim() });
              setSecret("");
              await action("connection_login", { id: conn.id });
            })}>儲存並用瀏覽器登入</button>
          </Card>
        ) : (
          <Card>
            <div className="card-title">用瀏覽器登入 GitHub</div>
            <div className="muted">會打開 GitHub 的授權頁,按 Authorize 後回到 omixor 就完成了。權限只要 read:user;公開 repo 的 PR 與 Actions 不用額外權限。</div>
            <div className="row gap8 wrap">
              <button className="btn primary" disabled={busy || !!st?.busy} onClick={() => login(conn.id)}>用瀏覽器登入</button>
              {(busy || st?.busy) && <><SIcon name="arrow.triangle.2.circlepath" spin className="muted" /><span className="caption">等瀏覽器授權…</span></>}
              <span className="grow" />
              <button className="btn link" onClick={() => run(async () => {
                await action("connection_upsert", { ...conn, oauth: { ...conn.oauth, clientId: "" } });
                await action("connection_secret", { id: conn.id, secret: "" });
                setClientId("");
              })}>重新設定 OAuth App</button>
            </div>
          </Card>
        )
      ) : <GitHubTokenStep conn={conn} />}
      {(st?.error || error) && <Banner>{st?.error || error}</Banner>}
      {conn?.auth === "oauth" && (
        <details className="ops-disclosure" open={useToken} onToggle={(e) => setUseToken((e.target as HTMLDetailsElement).open)}>
          <summary>改用個人存取權杖</summary>
          <div style={{ paddingTop: 6 }}><GitHubTokenStep conn={conn} /></div>
        </details>
      )}
    </div>
  );
}

function GitHubTokenStep({ conn }: { conn: Connection }) {
  const [token, setToken] = useState("");
  const { busy, error, run } = useRun();
  return (
    <div className="col gap8">
      <div className="caption muted">在 GitHub › Settings › Developer settings › Fine-grained tokens 建一個 token,Repository access 選要追蹤的 repo,權限給 Actions、Pull requests、Checks、Metadata 的 Read-only。</div>
      <div className="row gap8">
        <input className="input grow" style={{ minWidth: 0 }} type="password" placeholder="github_pat_…(存在鑰匙圈)" value={token} onChange={(e) => setToken(e.target.value)} />
        <button className="btn" disabled={busy || !token.trim()} onClick={() => run(async () => {
          await action("connection_upsert", { ...conn, auth: "bearer" });
          await action("connection_bearer", { id: conn.id, token });
          setToken("");
        })}>使用這個 token</button>
      </div>
      {conn.auth === "bearer" && <button className="btn link" style={{ alignSelf: "flex-start" }} onClick={() => run(() => action("connection_upsert", { ...conn, auth: "oauth" }))}>改回網頁 OAuth 登入</button>}
      {error && <Banner>{error}</Banner>}
    </div>
  );
}

// MARK: - GitLab

function GitLabPane() {
  const [gl, err] = useView<GitLabView>("gitlab", ["connections"]);
  const ready = !!gl?.connection && !!gl.configured && !!gl.loggedIn;
  return (
    <>
      <Section header="GitLab(公司自架)" icon="chevron.left.forwardslash.chevron.right"
        footer="omixor 只讀 GitLab:看追蹤中專案的 Pipelines 與開著的 Merge Requests,不會開 MR、留言或重跑 pipeline。資料在 開發進度 › GitLab。">
        {!gl ? <Row><span className="muted">{err ?? "讀取中…"}</span></Row>
          : ready ? <LogoutRow gl={gl} />
            : <NotConnectedRow text={!gl.connection || !gl.configured ? "還沒設定伺服器" : "還沒登入"} />}
      </Section>
      <ErrorText>{gl ? err : undefined}</ErrorText>
      {gl && <InsecureBanner gl={gl} />}
      {gl && !ready && <GitLabSetup gl={gl} />}
      {gl && ready && (
        <>
          {/* key:伺服器設定從後端更新時重設表單 */}
          <ServerCard key={`${gl.baseURL}|${gl.caCert}|${gl.insecure}`} gl={gl} />
          <GitLabProjects gl={gl} />
        </>
      )}
    </>
  );
}
