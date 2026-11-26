// 啟用 LDAP 時,沒登入就顯示登入頁;API 回 401(session 過期)也會回到登入頁
import { useEffect, useState, type ReactNode } from "react";
import { ErrorText } from "../components/ErrorText";
import { Logo } from "../components/Logo";
import { login, refreshAuth, useAuth } from "./auth";
import css from "./auth.module.css";

// 任何 API 回 401 就重新確認登入狀態
const origFetch = window.fetch.bind(window);
window.fetch = async (...args: Parameters<typeof fetch>) => {
  const r = await origFetch(...args);
  const url = String(args[0] instanceof Request ? args[0].url : args[0]);
  if (r.status === 401 && url.includes("/api/") && !url.includes("/api/auth/")) refreshAuth().catch(() => {});
  return r;
};

export function AuthGate({ children }: { children: ReactNode }) {
  const s = useAuth();
  if (!s) return <div className={css.loading}>載入中…</div>;
  if (s.mode === "ldap" && !s.loggedIn) return <LoginPage />;
  return (
    <>
      {children}
      {s.noAdmins && s.loggedIn && (
        <div className={css.noAdmins} role="alert">
          LDAP 登入已啟用但管理員帳號名單是空的,目前沒有人是管理員。請在主機上設 OMIXOR_LDAP_ADMINS=&lt;LDAP 帳號,逗號分隔&gt; 後重新啟動;
          或用 OMIXOR_AUTH=off(只開本機)啟動,到 設定 › 認證 填名單。
        </div>
      )}
    </>
  );
}

function LoginPage() {
  const [u, setU] = useState("");
  const [p, setP] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string>();
  useEffect(() => void (document.title = "登入 · omixor"), []);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(undefined);
    try {
      await login(u, p);
      document.title = "omixor";
    } catch (e: any) {
      setErr(e.message);
      setP("");
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className={css.bg}>
      <form className={css.card} onSubmit={submit}>
        <div className={"row gap8 " + css.center}>
          <Logo size={28} />
          <span className="h2">omixor</span>
        </div>
        <div className={"muted caption " + css.centerText}>用公司的 LDAP 帳號登入</div>
        <input className="input" placeholder="帳號" autoComplete="username" autoFocus value={u} onChange={(e) => setU(e.target.value)} />
        <input className="input" placeholder="密碼" type="password" autoComplete="current-password" value={p} onChange={(e) => setP(e.target.value)} />
        <ErrorText>{err}</ErrorText>
        <button className={"btn primary " + css.center} disabled={busy || !u.trim() || !p} type="submit">
          {busy ? "登入中…" : "登入"}
        </button>
      </form>
    </div>
  );
}
