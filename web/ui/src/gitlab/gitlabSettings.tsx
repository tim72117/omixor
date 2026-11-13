// GitLab 的設定元件(設定 › 程式碼倉庫 用):伺服器網址與 CA 憑證、登入流程(OAuth 應用程式或個人存取權杖)、登出、追蹤的專案。
// 從 GitLabTab 搬出來;開發進度 › GitLab 只看資料,只從這裡借 InsecureBanner(不驗證憑證的安全提醒)。
// 資料:view gitlab;動作:gitlab_settings、gitlab_ensure_connection、gitlab_add_project、gitlab_remove_project、connection_*(見 docs/PORTING.md)。
import { useState } from "react";
import { action, useRun } from "../api";
import { Card } from "../components/ui";
import { Banner, Check, copyText, SIcon, useAct } from "../components/opsKit";
import { Row, Section } from "../sections/settings/form";
import type { GitLabView } from "./GitLabTab";

type Connection = NonNullable<GitLabView["connection"]>;
const openURL = (u: string) => u && window.open(u, "_blank", "noopener");
const err = (e: any) => String(e?.message ?? e);

export function InsecureBanner({ gl }: { gl: GitLabView }) {
  if (!gl.insecure) return null;
  return <Banner tint="red">目前「不驗證憑證」:連線可能被攔截,token 也可能外流。只能在測試環境用;正式環境請改成指定公司的 CA 憑證檔。</Banner>;
}

export function ServerCard({ gl, onSaved }: { gl: GitLabView; onSaved?: () => void }) {
  const [base, setBase] = useState(gl.baseURL ?? "");
  const [ca, setCA] = useState(gl.caCert ?? "");
  const [insecure, setInsecure] = useState(!!gl.insecure);
  const { busy, error, run } = useRun();
  const changedBase = gl.configured && base.trim().replace(/\/+$/, "") !== gl.baseURL;
  return (
    <Card>
      <div className="card-title">1. 公司的 GitLab 在哪裡</div>
      <input className="input" placeholder="https://gitlab.corp.example.com" value={base} onChange={(e) => setBase(e.target.value)} />
      <div className="caption muted">只要網址(不用 /api/v4)。omixor 只讀,token 只會送到這一台主機。</div>
      <div className="caption muted" style={{ marginTop: 4 }}>自簽憑證時,指定公司的 CA 憑證檔(PEM 格式,例如 ~/certs/corp-ca.pem):</div>
      <input className="input mono" placeholder="CA 憑證檔路徑(選填)" value={ca} disabled={insecure} onChange={(e) => setCA(e.target.value)} />
      <Check on={insecure} onChange={setInsecure}><span className="caption">不驗證憑證(只給測試環境)</span></Check>
      {insecure && <Banner tint="red">不驗證憑證時,連線可能被攔截、token 可能外流。正式環境請改用 CA 憑證檔。</Banner>}
      {changedBase && <div className="caption" style={{ color: "var(--orange)" }}>換網址會登出,要重新登入。</div>}
      <button className="btn primary" style={{ alignSelf: "flex-start" }} disabled={busy || !base.trim()} onClick={() => run(async () => {
        await action("gitlab_settings", { baseURL: base.trim(), caCert: insecure ? "" : ca.trim(), insecure });
        onSaved?.();
      })}>儲存</button>
      {error && <Banner>{error}</Banner>}
    </Card>
  );
}

/** 已登入時的帳號列:帳號、伺服器、登入方式與登出 */
export function LogoutRow({ gl }: { gl: GitLabView }) {
  const { busy, run } = useRun();
  if (!gl.connection) return null;
  return (
    <Row>
      <SIcon name="person.crop.circle.badge.checkmark" color="green" />
      <div className="col grow" style={{ minWidth: 0 }}>
        <span>{gl.state?.account ?? "已登入"}</span>
        <span className="caption muted ellipsis">
          <span className="mono">{gl.baseURL}</span> · 登入方式:{gl.connection.auth === "bearer" ? "個人存取權杖" : "GitLab OAuth"}
        </span>
      </div>
      <button className="btn small" disabled={busy} onClick={() => run(() => action("connection_logout", { id: gl.connection!.id }))}>登出</button>
    </Row>
  );
}

/** 還沒設定或沒登入時的設定流程(伺服器 → 應用程式或權杖 → 登入)。不驗證憑證的警告由外層顯示。 */
export function GitLabSetup({ gl }: { gl: GitLabView }) {
  const conn = gl.connection;
  const st = gl.state;
  const [clientId, setClientId] = useState(conn?.oauth?.clientId ?? "");
  const [secret, setSecret] = useState("");
  const [useToken, setUseToken] = useState(false);
  const { busy, error, run } = useRun();
  const copyRow = (label: string, value: string) => (
    <div className="row gap8 wrap">
      <span className="caption muted" style={{ width: 150 }}>{label}</span>
      <span className="mono" style={{ userSelect: "text", wordBreak: "break-all" }}>{value}</span>
      <button className="ops-icon-btn" title="複製" onClick={() => copyText(value)}><SIcon name="doc.on.doc" size={12} /></button>
    </div>
  );
  return (
    <div className="col" style={{ gap: 12 }}>
      <div className="caption muted">連上後,開發進度 › GitLab 會列出追蹤中專案的 Pipelines(含 jobs、stage 與失敗紀錄)和開著的 Merge Requests(含 pipeline 與核准狀態)。omixor 只讀,不會開 MR、留言或重跑 pipeline。</div>
      {!conn ? (
        <button className="btn primary" style={{ alignSelf: "flex-start" }} disabled={busy} onClick={() => run(() => action("gitlab_ensure_connection"))}>開始設定</button>
      ) : (
        <>
          <ServerCard gl={gl} />
          {gl.configured && (conn.auth === "oauth" ? (
            !conn.oauth?.clientId ? (
              <Card>
                <div className="card-title">2. 在 GitLab 建一個應用程式(只要做一次)</div>
                <div className="muted">打開 GitLab 的「使用者設定 › 應用程式」(管理員也可以在 Admin › Applications 建給全公司用),新增應用程式,照下面填:</div>
                {copyRow("Name", "omixor")}
                {copyRow("Redirect URI", gl.callback)}
                {copyRow("Scopes", gl.scopes)}
                <div className="caption muted">「Confidential」不要勾(omixor 是桌面程式,用 PKCE);若公司規定要勾,建好後把 Secret 也貼到下面。omixor 登入時回呼網址會帶一個隨機連接埠(http://127.0.0.1:&lt;埠&gt;/callback);如果 GitLab 回報 redirect URI 不符,代表這版 GitLab 對本機回呼也比對連接埠,請改用個人存取權杖。</div>
                <button className="btn" style={{ alignSelf: "flex-start" }} disabled={!gl.applicationsURL} onClick={() => openURL(gl.applicationsURL)}>打開 GitLab 的應用程式頁</button>
                <div className="divider" />
                <div className="card-title">3. 把 Application ID 貼到這裡</div>
                <input className="input" placeholder="Application ID" value={clientId} onChange={(e) => setClientId(e.target.value)} />
                <input className="input" type="password" placeholder="Secret(選填,存在鑰匙圈)" value={secret} onChange={(e) => setSecret(e.target.value)} />
                <button className="btn primary" style={{ alignSelf: "flex-start" }} disabled={busy || !clientId.trim()} onClick={() => run(async () => {
                  await action("connection_upsert", { ...conn, oauth: { ...conn.oauth, clientId: clientId.trim() } });
                  if (secret.trim()) await action("connection_secret", { id: conn.id, secret: secret.trim() });
                  setSecret("");
                  await action("connection_login", { id: conn.id });
                })}>儲存並用瀏覽器登入</button>
              </Card>
            ) : (
              <Card>
                <div className="card-title">用瀏覽器登入 GitLab</div>
                <div className="muted">會打開 {gl.baseURL} 的授權頁,按 Authorize 後回到 omixor 就完成了。權限:{gl.scopes}。</div>
                <div className="row gap8 wrap">
                  <button className="btn primary" disabled={busy || !!st?.busy} onClick={() => run(() => action("connection_login", { id: conn.id }))}>用瀏覽器登入</button>
                  {(busy || st?.busy) && <><SIcon name="arrow.triangle.2.circlepath" spin className="muted" /><span className="caption">等瀏覽器授權…</span></>}
                  <span className="grow" />
                  <button className="btn link" onClick={() => run(async () => {
                    await action("connection_upsert", { ...conn, oauth: { ...conn.oauth, clientId: "" } });
                    await action("connection_secret", { id: conn.id, secret: "" });
                    setClientId("");
                  })}>重新設定應用程式</button>
                </div>
              </Card>
            )
          ) : <TokenStep gl={gl} conn={conn} />)}
          {(st?.error || error) && <Banner>{st?.error || error}</Banner>}
          {gl.configured && conn.auth === "oauth" && (
            <details className="ops-disclosure" open={useToken} onToggle={(e) => setUseToken((e.target as HTMLDetailsElement).open)}>
              <summary>改用個人存取權杖</summary>
              <div style={{ paddingTop: 6 }}><TokenStep gl={gl} conn={conn} /></div>
            </details>
          )}
        </>
      )}
    </div>
  );
}

function TokenStep({ gl, conn }: { gl: GitLabView; conn: Connection }) {
  const [token, setToken] = useState("");
  const { busy, error, run } = useRun();
  return (
    <div className="col gap8">
      <div className="caption muted">在 GitLab 的「使用者設定 › 個人存取權杖」建一個 token,範圍只勾 read_api,設好到期日。</div>
      {gl.tokenURL && <button className="btn" style={{ alignSelf: "flex-start" }} onClick={() => openURL(gl.tokenURL)}>打開建立權杖頁(已帶好 read_api)</button>}
      <div className="row gap8">
        <input className="input grow" style={{ minWidth: 0 }} type="password" placeholder="glpat-…(存在鑰匙圈)" value={token} onChange={(e) => setToken(e.target.value)} />
        <button className="btn" disabled={busy || !token.trim()} onClick={() => run(async () => {
          if (conn.auth !== "bearer") await action("connection_upsert", { ...conn, auth: "bearer" });
          await action("connection_bearer", { id: conn.id, token });
          setToken("");
        })}>使用這個 token</button>
      </div>
      {conn.auth === "bearer" && <button className="btn link" style={{ alignSelf: "flex-start" }} onClick={() => run(() => action("connection_upsert", { ...conn, auth: "oauth" }))}>改回 GitLab OAuth 登入</button>}
      {error && <Banner>{error}</Banner>}
    </div>
  );
}

/** 追蹤的專案:清單(在 GitLab 打開、停止追蹤)與新增。專案清單是組織的設定。 */
export function GitLabProjects({ gl }: { gl: GitLabView }) {
  const [newProject, setNewProject] = useState("");
  const [projectError, setProjectError] = useState<string>();
  const { run, toastEl } = useAct();
  const projects = gl.settings?.projects ?? [];
  const snapProjects = gl.snapshot?.projects ?? [];
  const nameOf = (key: string) => snapProjects.find((p) => p.key === key)?.path ?? key;
  const webOf = (key: string) => snapProjects.find((p) => p.key === key)?.url ?? `${gl.baseURL}/${key}`;
  const addProject = async () => {
    try {
      await action("gitlab_add_project", { project: newProject.trim() });
      setNewProject(""); setProjectError(undefined);
      action("gitlab_refresh").catch(() => undefined);
    } catch (e) { setProjectError(err(e)); }
  };
  return (
    <Section header="追蹤的專案" icon="shippingbox" footer="可以有子群組,也可以貼專案網址或數字 id。追蹤的專案是組織的設定,組織內的人都會看到;token、CA 憑證檔與不驗證憑證只存在這台。">
      {projects.length === 0 && <Row><span className="muted">還沒追蹤任何專案</span></Row>}
      {projects.map((k) => (
        <Row key={k}>
          <SIcon name="shippingbox" size={13} className="muted" />
          <span className="grow ellipsis" style={{ minWidth: 0 }}>{nameOf(k)}</span>
          <button className="btn small link" onClick={() => openURL(webOf(k))}>在 GitLab 打開</button>
          <button className="btn small danger" onClick={() => run(() => action("gitlab_remove_project", { project: k }))}>停止追蹤</button>
        </Row>
      ))}
      <Row>
        <input className="input grow" style={{ minWidth: 0 }} placeholder="group/project 或 id" value={newProject} onChange={(e) => setNewProject(e.target.value)} onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && newProject.trim() && addProject()} />
        <button className="btn" disabled={!newProject.trim()} onClick={addProject}>加入</button>
      </Row>
      {projectError && <Row><span className="caption" style={{ color: "var(--red)" }}>{projectError}</span></Row>}
      {!projectError && gl.settingsError && <Row><span className="caption" style={{ color: "var(--orange)" }}>組織設定:{gl.settingsError}</span></Row>}
      {toastEl}
    </Section>
  );
}
