// 側欄左下角:資料來源與帳號。本機(本機資料與本機登入)和遠端(設定好的 omixor server,用 evoleap 等方式授權)完全分開,
// 切換時組織清單、Issue 等都換成另一邊的。沒有設定遠端就只有「本機」。
// 有可以登出的東西(遠端登入、LDAP、或有設定遠端)時,一直看得到「登出」:登出後回到選擇連接對象或 LDAP 登入頁。
import { useState } from "react";
import { action, useRun, useView } from "../api";
import { Icon } from "../components/Icon";
import { logout, useAuth } from "./auth";
import { useConnect, type Account, type AccountServer } from "./ConnectGate";

export function AccountBox() {
  const [acc] = useView<Account>("account", ["account", "orgs"]);
  const auth = useAuth();
  const [open, setOpen] = useState(false);
  const [choosing, setChoosing] = useState<AccountServer>(); // 有多種授權的 server:二次選擇
  const { busy, error, pending, connect } = useConnect();
  const out = useRun();
  if (!acc) return null;
  const remote = acc.mode === "remote";
  const ldap = auth?.mode === "ldap" && !!auth.loggedIn;
  const hasServers = acc.servers.length > 0;
  const cur = acc.servers.find((s) => s.id === acc.serverId);
  // 一個區塊顯示「我是誰、資料從哪來」:遠端是遠端登入的帳號;本機是這個網頁的本機使用者(LDAP 登入的帳號或預設使用者)
  const me = auth?.user;
  const label = remote ? (acc.loggedIn ? acc.email : `${cur?.name ?? "遠端"}:未登入`) : (me?.name ?? "本機使用者");
  const sub = remote
    ? (cur?.name ?? "遠端") + (acc.loggedIn ? (acc.role === "admin" ? " · 管理員" : " · 成員") : "")
    : "本機 · " + (acc.localStorage === "postgres" ? "資料庫" : "檔案") + (ldap && me?.admin ? " · 管理員" : "");
  const done = () => { setOpen(false); setChoosing(undefined); };
  const go = async (mode: "local" | "remote", s?: AccountServer, authId?: string, authLabel?: string) => {
    await connect(mode, s?.id, authId, authLabel);
    done();
  };
  const pickServer = (s: AccountServer) => {
    if (s.auth.length > 1) setChoosing(s);
    else void go("remote", s, s.auth[0]?.id, s.auth[0]?.label ?? s.name);
  };
  // 登出:遠端先撤銷 token 並切回本機;LDAP 登出;有設定遠端就回到「選擇連接對象」
  const canLogout = remote || ldap || hasServers;
  const doLogout = () =>
    out.run(async () => {
      if (remote) await action("remote_logout");
      if (ldap) await logout();
      if (remote || hasServers) await action("gate_reset");
      done();
    });
  const err = error ?? out.error ?? (remote ? acc.error : undefined);
  return (
    <div className="account-box">
      <button className="account-current" onClick={() => { setChoosing(undefined); setOpen(!open); }} title={acc.error ?? undefined}>
        <Icon name={remote ? "cloud" : "person.crop.circle"} size={16} />
        <div className="col grow" style={{ textAlign: "left", minWidth: 0 }}>
          <span className="ellipsis">{label}</span>
          <span className="caption muted ellipsis">{sub}</span>
        </div>
        {acc.error && <span className="dot" style={{ background: "var(--orange)" }} />}
        <Icon name="chevron.up.chevron.down" size={12} className="muted" />
      </button>
      {canLogout && (
        <button className="account-logout" disabled={out.busy || busy} onClick={() => void doLogout()} title="登出並回到選擇連接對象或登入頁">
          <Icon name="power" size={13} /> 登出
        </button>
      )}
      {open && (
        <div className="menu account-menu">
          {busy ? (
            <div className="caption muted" style={{ padding: "6px 10px" }}>請在瀏覽器完成{pending ? ` ${pending} ` : ""}核准…</div>
          ) : choosing ? (
            <>
              <div className="caption muted" style={{ padding: "4px 10px" }}>{choosing.name}:選擇登入方式</div>
              {choosing.auth.map((a) => (
                <button key={a.id} className="menu-item" onClick={() => void go("remote", choosing, a.id, a.label)}>
                  <Icon name="key.horizontal" size={14} /> {a.label}
                </button>
              ))}
              <button className="menu-item" onClick={() => setChoosing(undefined)}><Icon name="chevron.left" size={14} /> 返回</button>
            </>
          ) : (
            <>
              <button className="menu-item" onClick={() => (remote ? void go("local") : done())}>
                <Icon name="internaldrive" size={14} /> 本機 {!remote && <span className="muted">✓</span>}
                <span className="caption muted">本機資料與登入</span>
              </button>
              {acc.servers.map((s) => (
                <button key={s.id} className="menu-item" onClick={() => (remote && s.id === acc.serverId ? done() : pickServer(s))}>
                  <Icon name="cloud" size={14} /> {s.name} {remote && s.id === acc.serverId && <span className="muted">✓</span>}
                </button>
              ))}
              {remote && acc.loggedIn && (
                <div className="caption muted" style={{ padding: "2px 10px" }}>{acc.name ? acc.name + " · " : ""}{acc.role === "admin" ? "伺服器管理員" : "成員"}</div>
              )}
            </>
          )}
          {err && <div className="caption" style={{ padding: "2px 10px", color: "var(--red)" }}>{err}</div>}
        </div>
      )}
      {!open && out.error && <div className="caption" style={{ padding: "2px 10px", color: "var(--red)" }}>{out.error}</div>}
    </div>
  );
}
