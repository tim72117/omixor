// 啟動時選擇連接對象:有設定遠端 omixor 時,這次執行第一次打開畫面先選「本機」或某個遠端
// (遠端再選授權方式,例如 evoleap);選好才進主畫面(接著照舊走本機的 LDAP 登入或直接進入)。
// 沒有設定遠端就不會出現。左下角的 AccountBox 之後隨時可以切換,「登出」後也會回到這裡。
import { useEffect, useState, type ReactNode } from "react";
import { action, useRun, useView } from "../api";
import { Icon } from "../components/Icon";
import { useAuth } from "./auth";

export type AuthOption = { id: string; label: string };
export type AccountServer = { id: string; name: string; url: string; auth: AuthOption[] };
export type Account = {
  mode: "local" | "remote"; server: string; serverId: string; servers: AccountServer[]; gate: boolean;
  localStorage: "postgres" | "files"; loggedIn: boolean; email?: string; name?: string; role?: string; error?: string;
};

/** 切換連接對象:本機,或遠端 server + 授權方式(遠端會開瀏覽器等授權,所以可能要等很久) */
export function useConnect() {
  const { busy, error, run } = useRun();
  const [pending, setPending] = useState<string>();
  const connect = async (mode: "local" | "remote", server?: string, auth?: string, label?: string) => {
    setPending(label);
    try {
      return await run(async () => { await action("mode_set", { mode, server, auth }); });
    } finally {
      setPending(undefined);
    }
  };
  return { busy, error, pending, connect };
}

export function ConnectGate({ children }: { children: ReactNode }) {
  const auth = useAuth();
  // 登入狀態變了(例如 LDAP 登入完、登出)就重新問一次
  return <Inner key={String(auth?.loggedIn)}>{children}</Inner>;
}

function Inner({ children }: { children: ReactNode }) {
  const [acc, err] = useView<Account>("account", ["account"]);
  if (!acc && !err) return <div className="empty">載入中…</div>;
  // 讀不到(例如還沒登入 LDAP)就先放行,讓既有的登入頁處理
  if (!acc || !acc.gate) return <>{children}</>;
  return <Picker acc={acc} />;
}

function Picker({ acc }: { acc: Account }) {
  const { busy, error, pending, connect } = useConnect();
  const [choosing, setChoosing] = useState<AccountServer>(); // 這個 server 有多種授權,選哪一種
  useEffect(() => void (document.title = "選擇連接對象 · omixor"), []);
  const cancel = () => setChoosing(undefined);
  const pick = (s: AccountServer) => {
    if (s.auth.length > 1) setChoosing(s);
    else void connect("remote", s.id, s.auth[0]?.id, s.auth[0]?.label ?? s.name);
  };
  return (
    <div className="login-bg">
      <div className="login-card connect-card">
        <div className="row gap8" style={{ justifyContent: "center" }}>
          <Icon name="server.rack" size={22} />
          <span className="h2">omixor</span>
        </div>
        <div className="muted caption" style={{ textAlign: "center" }}>選擇連接對象</div>

        {busy ? (
          <div className="col gap8" style={{ alignItems: "center", padding: "14px 0" }}>
            <div>請在瀏覽器完成{pending ? ` ${pending} ` : ""}核准…</div>
            <div className="caption muted">核准後會自動進入。</div>
            <button className="btn" onClick={cancel}>取消</button>
          </div>
        ) : choosing ? (
          <>
            <div className="caption muted">{choosing.name} 要用哪種方式登入?</div>
            {choosing.auth.map((a) => (
              <button key={a.id} className="connect-option" onClick={() => void connect("remote", choosing.id, a.id, a.label)}>
                <Icon name="key.horizontal" size={18} />
                <span className="col grow"><span>{a.label}</span></span>
              </button>
            ))}
            <button className="btn link" onClick={cancel}>返回</button>
          </>
        ) : (
          <>
            <button className="connect-option" onClick={() => void connect("local")}>
              <Icon name="internaldrive" size={18} />
              <span className="col grow">
                <span>本機</span>
                <span className="caption muted">本機資料與本機登入</span>
              </span>
            </button>
            {acc.servers.map((s) => (
              <button key={s.id} className="connect-option" onClick={() => pick(s)}>
                <Icon name="cloud" size={18} />
                <span className="col grow">
                  <span>{s.name}</span>
                </span>
              </button>
            ))}
          </>
        )}
        {error && <div className="error-text">{error}</div>}
      </div>
    </div>
  );
}
