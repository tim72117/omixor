// 啟用 LDAP 時,沒登入就顯示登入頁;API 回 401(session 過期)也會回到登入頁
import { useEffect, useState, type ReactNode } from "react";
import { Icon } from "../components/Icon";
import { login, refreshAuth, useAuth } from "./auth";

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
  if (!s) return <div className="empty">載入中…</div>;
  if (s.mode === "ldap" && !s.loggedIn) return <LoginPage />;
  return <>{children}</>;
}

function LoginPage() {
  const [u, setU] = useState("");
  const [p, setP] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string>();
  useEffect(() => void (document.title = "登入 · Ops"), []);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(undefined);
    try {
      await login(u, p);
      document.title = "Ops";
    } catch (e: any) {
      setErr(e.message);
      setP("");
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="login-bg">
      <form className="login-card" onSubmit={submit}>
        <div className="row gap8" style={{ justifyContent: "center" }}>
          <Icon name="server.rack" size={22} />
          <span className="h2">Ops</span>
        </div>
        <div className="muted caption" style={{ textAlign: "center" }}>用公司的 LDAP 帳號登入</div>
        <input className="input" placeholder="帳號" autoComplete="username" autoFocus value={u} onChange={(e) => setU(e.target.value)} />
        <input className="input" placeholder="密碼" type="password" autoComplete="current-password" value={p} onChange={(e) => setP(e.target.value)} />
        {err && <div className="error-text">{err}</div>}
        <button className="btn primary" disabled={busy || !u.trim() || !p} type="submit" style={{ justifyContent: "center" }}>
          {busy ? "登入中…" : "登入"}
        </button>
      </form>
    </div>
  );
}
