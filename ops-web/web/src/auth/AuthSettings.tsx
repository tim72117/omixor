// 設定 › 認證:LDAP 登入設定(只有管理員能看)
import { useEffect, useState } from "react";
import { Card, ErrorText, Pill, Segmented } from "../components/ui";
import { getConfig, refreshAuth, saveConfig, testConfig, useAuth, type AuthUser, type LdapConfig } from "./auth";

const PRESETS = {
  openldap: { userFilter: "(uid=%s)", idAttr: "uid", nameAttr: "cn", mailAttr: "mail", groupFilter: "(|(member=%s)(uniqueMember=%s))" },
  ad: { userFilter: "(&(objectClass=user)(sAMAccountName=%s))", idAttr: "sAMAccountName", nameAttr: "displayName", mailAttr: "mail", groupFilter: "(&(objectClass=group)(member:1.2.840.113556.1.4.1941:=%s))" },
};

export function AuthSettings() {
  const auth = useAuth();
  const [cfg, setCfg] = useState<LdapConfig>();
  const [loadErr, setLoadErr] = useState<string>();
  const [method, setMethod] = useState<"search" | "direct">("search");
  const [bindPw, setBindPw] = useState("");
  const [tu, setTu] = useState("");
  const [tp, setTp] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string>();
  const [tested, setTested] = useState<AuthUser>();
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    getConfig()
      .then((c) => { setCfg(c); setMethod(c.ldap.userDNTemplate ? "direct" : "search"); })
      .catch((e) => setLoadErr(e.message));
  }, []);

  if (loadErr) return <Card title="認證"><ErrorText>{loadErr}</ErrorText></Card>;
  if (!cfg) return <div className="muted">載入中…</div>;

  const l = cfg.ldap;
  const set = (patch: Partial<LdapConfig["ldap"]>) => { setCfg({ ...cfg, ldap: { ...l, ...patch } }); setTested(undefined); setSaved(false); };
  const effective = (): LdapConfig => ({
    ...cfg,
    ldap: method === "direct" ? { ...l } : { ...l, userDNTemplate: "" },
  });
  const pw = bindPw ? bindPw : undefined;

  const run = async (f: () => Promise<void>) => {
    setBusy(true); setErr(undefined);
    try { await f(); } catch (e: any) { setErr(e.message); } finally { setBusy(false); }
  };

  const field = (label: string, key: keyof LdapConfig["ldap"], placeholder = "", hint?: string) => (
    <label className="col gap4">
      <span className="caption muted">{label}</span>
      <input className="input mono" value={String(l[key] ?? "")} placeholder={placeholder} onChange={(e) => set({ [key]: e.target.value } as any)} />
      {hint && <span className="caption faint">{hint}</span>}
    </label>
  );

  return (
    <div className="col gap12" style={{ maxWidth: 760 }}>
      <Card title="網頁版登入" subtitle="MCP 不受影響:MCP 和 Swift 版一樣只聽本機(127.0.0.1),不需要登入。">
        <Segmented
          value={cfg.mode}
          options={[{ value: "off", label: "不用登入(只限本機)" }, { value: "ldap", label: "LDAP 帳號登入" }]}
          onChange={(m) => { setCfg({ ...cfg, mode: m }); setSaved(false); }}
        />
        <div className="caption muted">
          目前:{auth?.mode === "ldap" ? <Pill text="已啟用 LDAP" color="green" /> : <Pill text="不用登入" />}
          {auth?.mode === "ldap" && auth.user && <> · 你是 {auth.user.name}({auth.user.admin ? "管理員" : "一般使用者"})</>}
        </div>
        <div className="caption faint">
          要讓其他電腦連進來:先在這裡啟用 LDAP,再用 OPS_WEB_ADDR=0.0.0.0:5191 與 OPS_WEB_HOSTS=&lt;主機名稱&gt; 重開,並放在 HTTPS 反向代理後面。設定錯了把自己鎖在外面時,用 OPS_AUTH=off 啟動就能回到本機模式。
        </div>
      </Card>

      {cfg.mode === "ldap" && (
        <>
          <Card title="伺服器">
            <div className="grid g2">
              {field("網址", "url", "ldaps://ldap.corp.example.com:636", "ldap://…:389 或 ldaps://…:636")}
              {field("CA 憑證(PEM 檔路徑)", "caCert", "/etc/ssl/corp-ca.pem", "自簽憑證才需要;空白用系統的")}
            </div>
            <div className="row gap12 wrap">
              <label className="row gap4"><input type="checkbox" checked={l.startTLS} onChange={(e) => set({ startTLS: e.target.checked })} /> ldap:// 時先 StartTLS(建議)</label>
              <label className="row gap4"><input type="checkbox" checked={l.insecureSkipVerify} onChange={(e) => set({ insecureSkipVerify: e.target.checked })} /> 不驗證伺服器憑證</label>
              {l.insecureSkipVerify && <Pill text="只能用在測試環境:密碼可能被攔截" color="red" icon="exclamationmark.triangle" />}
              {l.url.startsWith("ldap://") && !l.startTLS && <Pill text="密碼會以明文傳送" color="orange" icon="exclamationmark.triangle" />}
            </div>
          </Card>

          <Card title="怎麼找到使用者" right={
            <div className="row gap6">
              <button className="btn link" onClick={() => set(PRESETS.openldap)}>OpenLDAP 預設</button>
              <button className="btn link" onClick={() => set(PRESETS.ad)}>Active Directory 預設</button>
            </div>
          }>
            <Segmented value={method} onChange={(m) => { setMethod(m); setTested(undefined); }}
              options={[{ value: "search", label: "服務帳號搜尋(建議)" }, { value: "direct", label: "直接用帳號組 DN" }]} />
            {method === "search" ? (
              <div className="grid g2">
                {field("服務帳號 DN", "bindDN", "cn=ops-svc,ou=svc,dc=corp,dc=example,dc=com")}
                <label className="col gap4">
                  <span className="caption muted">服務帳號密碼 {l.hasBindPassword && <Pill text="已存在鑰匙圈" color="green" />}</span>
                  <input className="input" type="password" value={bindPw} placeholder={l.hasBindPassword ? "不改就留白" : ""} onChange={(e) => { setBindPw(e.target.value); setTested(undefined); }} />
                </label>
                {field("搜尋起點(Base DN)", "baseDN", "ou=people,dc=corp,dc=example,dc=com")}
                {field("使用者篩選", "userFilter", "(uid=%s)", "%s 換成登入帳號(會自動跳脫)")}
              </div>
            ) : (
              <div className="grid g2">
                {field("使用者 DN 範本", "userDNTemplate", "uid=%s,ou=people,dc=corp,dc=example,dc=com", "AD 可以寫 %s@corp.example.com")}
                {field("搜尋起點(讀名稱與群組用,可留白)", "baseDN", "dc=corp,dc=example,dc=com")}
              </div>
            )}
            <div className="grid g3">
              {field("帳號屬性(Ops 裡的使用者 id)", "idAttr", "uid")}
              {field("顯示名稱屬性", "nameAttr", "cn")}
              {field("Email 屬性", "mailAttr", "mail")}
            </div>
          </Card>

          <Card title="群組" subtitle="群組 DN 留白表示不限制。">
            <div className="grid g2">
              {field("允許登入的群組", "requiredGroup", "cn=ops-users,ou=groups,dc=corp,dc=example,dc=com")}
              {field("管理員群組(可以改這頁)", "adminGroup", "cn=ops-admins,ou=groups,dc=corp,dc=example,dc=com")}
              {field("群組篩選", "groupFilter", "(|(member=%s)(uniqueMember=%s))", "%s 換成使用者 DN;也會看使用者的 memberOf")}
              {field("群組搜尋起點", "groupBaseDN", "ou=groups,dc=corp,dc=example,dc=com", "空白用上面的搜尋起點")}
            </div>
          </Card>

          <Card title="測試與儲存" subtitle="用一組真的帳號試登入;啟用時一定要用管理員群組的帳號測試成功,才能存,避免把自己鎖在外面。">
            <div className="row gap8 wrap">
              <input className="input" placeholder="測試帳號" value={tu} onChange={(e) => setTu(e.target.value)} />
              <input className="input" type="password" placeholder="密碼" value={tp} onChange={(e) => setTp(e.target.value)} />
              <button className="btn" disabled={busy || !tu || !tp} onClick={() => run(async () => setTested(await testConfig(effective(), pw, tu, tp)))}>
                測試登入
              </button>
            </div>
            {tested && (
              <div className="caption">
                <Pill text="登入成功" color="green" icon="checkmark.circle" /> {tested.name}({tested.id}){tested.email && ` · ${tested.email}`}
                {" · "}{tested.admin ? "管理員" : <span style={{ color: "var(--orange)" }}>不是管理員</span>}
                <div className="mono faint">{tested.dn}</div>
                {tested.groups?.length ? <div className="faint">群組:{tested.groups.join("、")}</div> : null}
              </div>
            )}
          </Card>
        </>
      )}

      <div className="row gap8">
        <button className="btn primary" disabled={busy || (cfg.mode === "ldap" && (!tu || !tp))}
          onClick={() => run(async () => { await saveConfig(effective(), pw, tu, tp); setSaved(true); setBindPw(""); await refreshAuth(); })}>
          儲存
        </button>
        {saved && <span className="caption" style={{ color: "var(--green)" }}>已儲存。所有人(包括你)要重新登入。</span>}
        <ErrorText>{err}</ErrorText>
      </div>
    </div>
  );
}
