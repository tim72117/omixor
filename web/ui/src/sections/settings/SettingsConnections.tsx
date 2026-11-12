// 設定 › 連線:Ops 要呼叫的外部服務。每個連線 = 網址 + 一種通用的認證方式(Swift 的 SettingsConnectionsPane)
import { useEffect, useState } from "react";
import { action, useRun, useView } from "../../api";
import { Spinner, Switch } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { AUTH_DETAIL, AUTH_KINDS, AUTH_LABEL, type AuthKind, type Connection, type ConnectionState, type ConnectionsView } from "../../components/models";
import { EmptyState, ErrorText, Modal, Pill } from "../../components/ui";
import { Form, Row, Section } from "./form";

const host = (u: string) => { try { return new URL(u).host || u; } catch { return u; } };
const validURL = (u: string) => { try { return !!new URL(u).host; } catch { return false; } };

function blank(id: string, name: string, baseURL: string, auth: AuthKind): Connection {
  return {
    id, name, baseURL, auth, isProduction: false, testPath: "", note: "",
    oauth: { issuer: "", authorizationEndpoint: "", tokenEndpoint: "", registrationEndpoint: "", clientId: "", hasClientSecret: false, scopes: "openid email offline_access", resource: "", extraParams: "", sendIdToken: false },
    approval: { consoleURL: "", startPath: "/api/cli-auth/start", approvePath: "/login/cli?id={id}", exchangePath: "/api/cli-auth/exchange", logoutPath: "/api/cli-auth/logout" },
  };
}

/** 寬鬆補齊欄位(後端缺欄位時用預設值) */
const normalize = (c: Connection): Connection => {
  const b = blank(c.id, c.name, c.baseURL, c.auth ?? "oauth");
  return { ...b, ...c, oauth: { ...b.oauth, ...(c.oauth ?? {}) }, approval: { ...b.approval, ...(c.approval ?? {}) } };
};

export function SettingsConnections() {
  const [data, err] = useView<ConnectionsView>("connections", ["orgs"]);
  const conns = (Array.isArray(data?.connections) ? data!.connections : []).map(normalize);
  const states = data?.states ?? {};
  const [selection, setSelection] = useState<string>();
  const [adding, setAdding] = useState(false);
  useEffect(() => { if ((!selection || !conns.some((c) => c.id === selection)) && conns.length) setSelection(conns[0].id); }, [conns.map((c) => c.id).join(",")]);
  if (err) return <EmptyState title="讀不到連線" icon="exclamationmark.triangle" message={err} />;
  const sel = conns.find((c) => c.id === selection);
  return (
    <div className="split">
      <div className="split-list" style={{ width: 240 }}>
        <div className="grow">
          {conns.map((c) => {
            const st = states[c.id];
            return (
              <div key={c.id} className={"bx-list-row" + (selection === c.id ? " on" : "")} style={{ alignItems: "center" }} onClick={() => setSelection(c.id)}>
                <span className="dot" style={{ width: 8, height: 8, background: st?.account ? "var(--green)" : st?.error ? "var(--orange)" : "color-mix(in srgb, var(--gray) 40%, transparent)" }} />
                <div className="col grow" style={{ minWidth: 0 }}>
                  <span className="row gap4"><span className="ellipsis">{c.name}</span>{c.isProduction && <Pill text="正式" color="red" />}</span>
                  <span className="caption muted ellipsis">{AUTH_LABEL[c.auth]} · {host(c.baseURL)}</span>
                </div>
              </div>
            );
          })}
        </div>
        <div style={{ padding: 8, borderTop: "1px solid var(--line)" }}><button className="btn link" onClick={() => setAdding(true)}><Icon name="plus" size={12} />新增連線</button></div>
      </div>
      <div className="split-detail">
        {sel ? <Editor key={sel.id} original={sel} state={states[sel.id] ?? {}} />
          : <EmptyState title="選一個連線" icon="key.horizontal" message="每個連線是一個外部服務的網址加上認證方式。登入後,omixor 的其他功能(例如旁白發佈)就能用這個身分呼叫它的 API。" />}
      </div>
      {adding && <NewSheet existing={conns.map((c) => c.id)} onClose={() => setAdding(false)} onCreated={setSelection} />}
    </div>
  );
}

function Editor({ original, state }: { original: Connection; state: ConnectionState }) {
  const [draft, setDraft] = useState(original);
  const [secret, setSecret] = useState("");
  const [bearer, setBearer] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const { busy, error, run } = useRun();
  useEffect(() => setDraft(original), [JSON.stringify(original)]);
  const dirty = JSON.stringify(draft) !== JSON.stringify(original);
  const set = (p: Partial<Connection>) => setDraft((d) => ({ ...d, ...p }));
  const setO = (p: Partial<Connection["oauth"]>) => setDraft((d) => ({ ...d, oauth: { ...d.oauth, ...p } }));
  const setA = (p: Partial<Connection["approval"]>) => setDraft((d) => ({ ...d, approval: { ...d.approval, ...p } }));
  const act = (name: string, args: Record<string, unknown> = { id: original.id }) => run(() => action(name, args));
  const field = (label: string, value: string, onChange: (v: string) => void, placeholder?: string, type = "text") => (
    <Row label={label}><input className="input" type={type} style={{ width: 360 }} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} /></Row>
  );
  return (
    <Form width={720}>
      <Section>
        <Row>
          <Icon name={state.account ? "checkmark.seal.fill" : "key.horizontal"} size={22} color={state.account ? "var(--green)" : "var(--fg2)"} />
          <div className="col grow">
            <span style={{ fontWeight: 600 }}>{state.account ? `已登入:${state.account}` : original.auth === "none" ? "不用登入" : "尚未登入"}</span>
            {state.lastTest && <span className="caption muted">{state.lastTest}</span>}
            {state.error && <span className="caption" style={{ color: "var(--orange)", userSelect: "text" }}>{state.error}</span>}
          </div>
          {state.busy || busy ? <Spinner /> : (
            <>
              {(original.auth === "oauth" || original.auth === "approval") && (state.account
                ? <button className="btn" onClick={() => act("connection_logout")}>登出</button>
                : <button className="btn primary" disabled={dirty} onClick={() => act("connection_login")}>登入…</button>)}
              <button className="btn" disabled={dirty || !original.testPath} onClick={() => act("connection_test")}>測試連線</button>
            </>
          )}
        </Row>
        {dirty && <Row><span className="caption muted">先儲存變更再登入或測試</span></Row>}
      </Section>
      <ErrorText>{error}</ErrorText>
      <Section header="服務">
        {field("名稱", draft.name, (v) => set({ name: v }))}
        {field("網址", draft.baseURL, (v) => set({ baseURL: v }), "https://api.example.com")}
        {field("測試路徑", draft.testPath, (v) => set({ testPath: v }), "/api/me(登入後用來確認身分,可留空)")}
        <Row label="正式環境(發佈前會特別提醒)"><Switch on={draft.isProduction} onChange={(v) => set({ isProduction: v })} /></Row>
        {field("備註", draft.note, (v) => set({ note: v }))}
      </Section>
      <Section header="認證">
        <Row label="認證方式">
          <select className="select" value={draft.auth} onChange={(e) => set({ auth: e.target.value as AuthKind })}>{AUTH_KINDS.map((k) => <option key={k} value={k}>{AUTH_LABEL[k]}</option>)}</select>
        </Row>
        <Row><span className="caption muted">{AUTH_DETAIL[draft.auth]}</span></Row>
        {draft.auth === "oauth" && (
          <>
            <Row label="授權伺服器(issuer)">
              <input className="input" style={{ width: 290 }} value={draft.oauth.issuer} placeholder="留空 = 從服務網址自動探索" onChange={(e) => setO({ issuer: e.target.value })} />
              <button className="btn" disabled={!validURL(draft.baseURL)} onClick={() => run(async () => { await action("connection_upsert", draft as unknown as Record<string, unknown>); await action("connection_discover", { id: original.id }); })}>探索</button>
            </Row>
            {field("授權端點", draft.oauth.authorizationEndpoint, (v) => setO({ authorizationEndpoint: v }), "探索後自動填入")}
            {field("Token 端點", draft.oauth.tokenEndpoint, (v) => setO({ tokenEndpoint: v }), "探索後自動填入")}
            {field("註冊端點(動態註冊)", draft.oauth.registrationEndpoint, (v) => setO({ registrationEndpoint: v }), "伺服器支援時自動填入")}
            {field("client_id", draft.oauth.clientId, (v) => setO({ clientId: v }), "留空 = 自動註冊(伺服器要支援)")}
            <Row label="client secret(選填,存鑰匙圈)">
              <input className="input" type="password" style={{ width: 260 }} value={secret} autoComplete="off"
                placeholder={original.oauth.hasClientSecret ? "已設定,輸入新的會取代" : "桌面用戶端通常不需要"} onChange={(e) => setSecret(e.target.value)} />
              <button className="btn" disabled={!secret && !original.oauth.hasClientSecret}
                onClick={() => run(async () => { await action("connection_secret", { id: original.id, secret }); setSecret(""); })}>
                {original.oauth.hasClientSecret && !secret ? "清除" : "存入"}
              </button>
            </Row>
            {field("scopes", draft.oauth.scopes, (v) => setO({ scopes: v }))}
            {field("resource(RFC 8707)", draft.oauth.resource, (v) => setO({ resource: v }), "選填:token 只給這個服務用")}
            {field("額外參數", draft.oauth.extraParams, (v) => setO({ extraParams: v }), "例如 Google:access_type=offline&prompt=consent")}
            <Row label="呼叫 API 時送 id_token(Google IAP / Cloud Run)"><Switch on={draft.oauth.sendIdToken} onChange={(v) => setO({ sendIdToken: v })} /></Row>
            <Row><span className="caption muted">回呼網址:http://127.0.0.1:&lt;隨機埠&gt;/callback(原生 app 標準做法,RFC 8252)。在 Google 等主控台建用戶端時選「桌面應用程式」。</span></Row>
          </>
        )}
        {draft.auth === "approval" && (
          <>
            {field("核准頁網站", draft.approval.consoleURL, (v) => setA({ consoleURL: v }), "留空 = 和服務網址相同")}
            <Row>
              <details className="bx-disclosure" style={{ width: "100%" }}>
                <summary>進階:路徑</summary>
                <div className="col gap6" style={{ paddingTop: 6 }}>
                  {([["登記", "startPath"], ["核准頁", "approvePath"], ["換 token", "exchangePath"], ["登出", "logoutPath"]] as const).map(([l, k]) => (
                    <label key={k} className="row gap8"><span style={{ width: 70 }} className="muted">{l}</span><input className="input grow" value={draft.approval[k]} onChange={(e) => setA({ [k]: e.target.value })} /></label>
                  ))}
                </div>
              </details>
            </Row>
          </>
        )}
        {(draft.auth === "bearer" || draft.auth === "basic") && (
          <Row label={draft.auth === "basic" ? "帳號:Token" : "Token"}>
            <input className="input" type="password" style={{ width: 260 }} autoComplete="off" value={bearer}
              placeholder={state.account ? "已設定,輸入新的會取代" : draft.auth === "basic" ? "帳號:token,例如 me@example.com:API token" : "貼上 API key 或存取權杖"} onChange={(e) => setBearer(e.target.value)} />
            <button className="btn" disabled={!bearer || dirty} onClick={() => run(async () => { await action("connection_bearer", { id: original.id, token: bearer }); setBearer(""); })}>存入鑰匙圈</button>
          </Row>
        )}
      </Section>
      <Section>
        <Row>
          <button className="btn danger" onClick={() => setConfirmDelete(true)}>刪除連線</button>
          <div className="grow" />
          <button className="btn" disabled={!dirty} onClick={() => setDraft(original)}>還原</button>
          <button className="btn primary" disabled={!dirty || !draft.name || !validURL(draft.baseURL) || busy}
            onClick={() => act("connection_upsert", draft as unknown as Record<string, unknown>)}>儲存</button>
        </Row>
      </Section>
      <Modal open={confirmDelete} onClose={() => setConfirmDelete(false)} width={400} title={`刪除「${original.name}」?`}>
        <div className="row gap8" style={{ justifyContent: "flex-end" }}>
          <button className="btn" onClick={() => setConfirmDelete(false)}>取消</button>
          <button className="btn red" onClick={() => { setConfirmDelete(false); act("connection_delete"); }}>刪除(連同鑰匙圈裡的登入)</button>
        </div>
      </Modal>
    </Form>
  );
}

function NewSheet({ existing, onClose, onCreated }: { existing: string[]; onClose: () => void; onCreated: (id: string) => void }) {
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [auth, setAuth] = useState<AuthKind>("oauth");
  const { busy, error, run } = useRun();
  const create = () => run(async () => {
    let base = name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "conn";
    let id = base, i = 2;
    while (existing.includes(id)) id = `${base}-${i++}`;
    const r = await action<Connection | undefined>("connection_upsert", blank(id, name, url.trim(), auth) as unknown as Record<string, unknown>);
    onCreated(r?.id ?? id);
    onClose();
  });
  return (
    <Modal open onClose={onClose} width={520}>
      <span className="modal-title">新增連線</span>
      <label className="bx-field"><span>名稱</span><input className="input" autoFocus placeholder="例如 Payments API" value={name} onChange={(e) => setName(e.target.value)} /></label>
      <label className="bx-field"><span>網址</span><input className="input" placeholder="https://api.example.com" value={url} onChange={(e) => setUrl(e.target.value)} /></label>
      <label className="bx-field"><span>認證方式</span><select className="select" value={auth} onChange={(e) => setAuth(e.target.value as AuthKind)}>{AUTH_KINDS.map((k) => <option key={k} value={k}>{AUTH_LABEL[k]}</option>)}</select></label>
      <span className="caption muted">{AUTH_DETAIL[auth]}</span>
      <ErrorText>{error}</ErrorText>
      <div className="row gap8" style={{ justifyContent: "flex-end" }}>
        <button className="btn" onClick={onClose}>取消</button>
        <button className="btn primary" disabled={!name || !validURL(url) || busy} onClick={create}>建立</button>
      </div>
    </Modal>
  );
}
