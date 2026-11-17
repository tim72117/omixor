// 遠端模式、伺服器上還沒有我能用的組織時的畫面(登入成功就能進來,不在連線時擋下):
//   - 伺服器管理員:直接建立第一個組織
//   - 一般成員:顯示目前帳號,請管理員把你加進組織;背景每 30 秒更新組織清單,被加進去就自動進入主畫面
// 兩種都可以登出或換連接對象。本機模式、或已經有組織時直接顯示主畫面。
import { useEffect, useState, type ReactNode } from "react";
import { action, useRun, useView } from "../api";
import { Icon } from "../components/Icon";
import type { Account } from "./ConnectGate";

type OrgIndex = { current: string; orgs: { id: string }[] };

export function OrgGate({ children }: { children: ReactNode }) {
  const [acc] = useView<Account>("account", ["account"]);
  const [idx] = useView<OrgIndex>("orgs");
  if (!acc || acc.mode !== "remote" || !idx || idx.orgs.length > 0) return <>{children}</>;
  return <NoOrgs acc={acc} />;
}

function NoOrgs({ acc }: { acc: Account }) {
  const { busy, error, run } = useRun();
  const [name, setName] = useState("");
  const admin = acc.role === "admin";
  const server = acc.servers.find((s) => s.id === acc.serverId)?.name ?? acc.server;
  useEffect(() => void (document.title = "沒有組織 · omixor"), []);
  const create = () =>
    run(async () => {
      await action("org_create", { name: name.trim(), symbol: "building.2", color: "blue", switch: true });
    });
  const leave = (logout: boolean) =>
    run(async () => {
      if (logout) await action("remote_logout");
      await action("gate_reset");
    });
  return (
    <div className="login-bg">
      <div className="login-card connect-card">
        <div className="row gap8" style={{ justifyContent: "center" }}>
          <Icon name="building.2" size={22} />
          <span className="h2">還沒有組織</span>
        </div>
        <div className="muted caption" style={{ textAlign: "center" }}>
          已登入 {server}:{acc.email ?? acc.name ?? ""}
          {admin ? "(伺服器管理員)" : ""}
        </div>
        {!acc.loggedIn ? (
          <div className="caption">{acc.error ?? "登入已失效,請重新連接。"}</div>
        ) : admin ? (
          <>
            <div className="caption">這台伺服器上還沒有任何組織。建立第一個組織後就會進入主畫面,之後可以在「設定 › 組織」加入成員。</div>
            <input className="input" placeholder="組織名稱(例如 evoleap)" value={name} autoFocus
              onChange={(e) => setName(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter" && !e.nativeEvent.isComposing && name.trim() && !busy) void create(); }} />
            <button className="btn primary" disabled={!name.trim() || busy} onClick={() => void create()}>
              {busy ? "建立中…" : "建立組織"}
            </button>
          </>
        ) : (
          <div className="caption">
            你還沒有被加進這台伺服器上的任何組織。請組織管理員把你({acc.email ?? "這個帳號"})加進組織;
            加入後這個畫面會在 30 秒內自動進入主畫面。
          </div>
        )}
        {error && <div className="error-text">{error}</div>}
        <div className="row gap8" style={{ justifyContent: "center" }}>
          <button className="btn link" disabled={busy} onClick={() => void leave(false)}>換連接對象</button>
          <button className="btn link" disabled={busy} onClick={() => void leave(true)}>登出</button>
        </div>
      </div>
    </div>
  );
}
