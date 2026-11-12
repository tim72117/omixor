// 側欄左下角:資料來源與帳號。本機(本機資料 + 本機授權)和遠端(evoleap 登入 omixor server)完全分開,
// 切換時組織清單、Issue 等都換成另一邊的。切到遠端若還沒登入,會開瀏覽器用 evoleap 登入。
import { useState } from "react";
import { action, useRun, useView } from "../api";
import { Icon } from "../components/Icon";

type Account = {
  mode: "local" | "remote"; server: string; localStorage: "postgres" | "files";
  loggedIn: boolean; email?: string; name?: string; role?: string; error?: string; defaultServer: string;
};

export function AccountBox() {
  const [acc] = useView<Account>("account", ["account", "orgs"]);
  const [open, setOpen] = useState(false);
  const [server, setServer] = useState("");
  const { busy, error, run } = useRun();
  if (!acc) return null;
  const remote = acc.mode === "remote";
  const label = remote ? (acc.loggedIn ? acc.email : "遠端:未登入") : "本機";
  const sub = remote ? acc.server.replace(/^https?:\/\//, "") : acc.localStorage === "postgres" ? "本機資料庫" : "本機檔案";
  const go = (mode: "local" | "remote") => run(async () => { await action("mode_set", { mode, server: server || acc.server || acc.defaultServer }); setOpen(false); });
  return (
    <div className="account-box">
      <button className="account-current" onClick={() => { setServer(acc.server || acc.defaultServer); setOpen(!open); }} title={acc.error ?? undefined}>
        <Icon name={remote ? "cloud" : "internaldrive"} size={16} />
        <div className="col grow" style={{ textAlign: "left", minWidth: 0 }}>
          <span className="ellipsis">{label}</span>
          <span className="caption muted ellipsis">{sub}</span>
        </div>
        {remote && acc.error && <span className="dot" style={{ background: "var(--orange)" }} />}
        <Icon name="chevron.up.chevron.down" size={12} className="muted" />
      </button>
      {open && (
        <div className="menu account-menu">
          <button className="menu-item" disabled={busy} onClick={() => go("local")}>
            <Icon name="internaldrive" size={14} /> 本機 {!remote && <span className="muted">✓</span>}
            <span className="caption muted">本機資料與授權</span>
          </button>
          <button className="menu-item" disabled={busy} onClick={() => go("remote")}>
            <Icon name="cloud" size={14} /> 遠端 omixor server {remote && <span className="muted">✓</span>}
            <span className="caption muted">{busy ? "登入中…請在瀏覽器核准" : "evoleap 登入"}</span>
          </button>
          <div style={{ padding: "4px 8px" }}>
            <input className="input" value={server} onChange={(e) => setServer(e.target.value)} placeholder="omixor server 網址" />
          </div>
          {remote && acc.loggedIn && (
            <div className="caption muted" style={{ padding: "2px 10px" }}>{acc.name ? acc.name + " · " : ""}{acc.role === "admin" ? "伺服器管理員" : "成員"}</div>
          )}
          {(error || (remote && acc.error)) && <div className="caption" style={{ padding: "2px 10px", color: "var(--red)" }}>{error ?? acc.error}</div>}
          {remote && (
            <>
              <div className="menu-sep" />
              <button className="menu-item" disabled={busy} onClick={() => run(async () => { await action("remote_logout"); setOpen(false); })}>
                <Icon name="power" size={14} /> 登出遠端
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}
