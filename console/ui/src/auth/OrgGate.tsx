// 還沒有組織時的畫面(不自動建立預設組織;登入成功就能進來,不在連線時擋下):
//   - 管理員(本機:web 管理員,沒啟用登入時就是本機使用者;遠端:伺服器管理員):直接建立第一個組織
//   - 其他人:說明要請管理員處理。遠端背景每 30 秒更新組織清單;組織清單一有內容就自動進入主畫面
// 可以登出或換連接對象。已經有組織時直接顯示主畫面。
import { useEffect, useState, type ReactNode } from "react";
import { action, useRun, useView } from "../api";
import { ErrorText } from "../components/ErrorText";
import { Icon } from "../components/Icon";
import { logout, useAuth } from "./auth";
import css from "./auth.module.css";
import { useCanSwitchTarget, type Account } from "./ConnectGate";

type OrgIndex = {
  current: string;
  orgs: { id: string }[];
  /** 本機模式讀不到組織的原因(沒有資料庫等) */
  error?: string;
  /** 開了範本時,第一個組織的預填值 */
  suggested?: { name: string; symbol: string; color: string };
};

export function OrgGate({ children }: { children: ReactNode }) {
  const [acc] = useView<Account>("account", ["account"]);
  const [idx] = useView<OrgIndex>("orgs");
  if (!acc || !idx || idx.orgs.length > 0) return <>{children}</>;
  return <NoOrgs acc={acc} idx={idx} />;
}

function NoOrgs({ acc, idx }: { acc: Account; idx: OrgIndex }) {
  const { busy, error, run } = useRun();
  const [name, setName] = useState(idx.suggested?.name ?? "");
  const remote = acc.mode === "remote";
  const canSwitch = useCanSwitchTarget(); // 沒啟用登入,或登入者是 web 管理員
  const admin = remote ? acc.role === "admin" : canSwitch;
  const server = acc.servers.find((s) => s.id === acc.serverId)?.name ?? acc.server;
  useEffect(() => void (document.title = "沒有組織 · omixor"), []);
  const create = () =>
    run(async () => {
      await action("org_create", { name: name.trim(), symbol: idx.suggested?.symbol ?? "building.2", color: idx.suggested?.color ?? "blue", switch: true });
    });
  const auth = useAuth();
  // 本機模式只有設定了遠端才有別的連接對象可換
  const showSwitch = canSwitch && (remote || acc.servers.length > 0);
  const showLogout = remote ? canSwitch || auth?.mode === "ldap" : auth?.mode === "ldap";
  // 不是 web 管理員:連接對象是大家共用的,只能登出自己的 LDAP
  const leave = (signOut: boolean) =>
    run(async () => {
      if (!canSwitch || !remote) return signOut ? logout() : action("gate_reset");
      if (signOut) await action("remote_logout");
      await action("gate_reset");
    });
  return (
    <div className={css.bg}>
      <div className={css.card + " " + css.connectCard}>
        <div className={"row gap8 " + css.center}>
          <Icon name="building.2" size={22} />
          <span className="h2">還沒有組織</span>
        </div>
        {remote && (
          <div className={"muted caption " + css.centerText}>
            已登入 {server}:{acc.email ?? acc.name ?? ""}
            {admin ? "(伺服器管理員)" : ""}
          </div>
        )}
        {remote && !acc.loggedIn ? (
          <div className="caption">{acc.error ?? "登入已失效,請重新連接。"}</div>
        ) : idx.error ? (
          <div className="caption">讀不到組織:{idx.error}</div>
        ) : admin ? (
          <>
            <div className="caption">{remote ? "這台伺服器上" : "資料庫裡"}還沒有任何組織。建立第一個組織後就會進入主畫面,之後可以在「設定 › 組織」{remote ? "加入成員" : "改名稱、圖示或新增其他組織"}。</div>
            <input className="input" placeholder="組織名稱(例如 我的團隊)" value={name} autoFocus
              onChange={(e) => setName(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter" && !e.nativeEvent.isComposing && name.trim() && !busy) void create(); }} />
            <button className="btn primary" disabled={!name.trim() || busy} onClick={() => void create()}>
              {busy ? "建立中…" : "建立組織"}
            </button>
          </>
        ) : !remote ? (
          <div className="caption">還沒有任何組織。請管理員登入並建立第一個組織;建好後這個畫面會自動進入主畫面(沒有的話請重新整理)。</div>
        ) : (
          <div className="caption">
            你還沒有被加進這台伺服器上的任何組織。請組織管理員把你({acc.email ?? "這個帳號"})加進組織;
            加入後這個畫面會在 30 秒內自動進入主畫面。
          </div>
        )}
        <ErrorText>{error}</ErrorText>
        <div className={"row gap8 " + css.center}>
          {showSwitch && <button className="btn link" disabled={busy} onClick={() => void leave(false)}>換連接對象</button>}
          {showLogout && <button className="btn link" disabled={busy} onClick={() => void leave(true)}>登出</button>}
        </div>
      </div>
    </div>
  );
}
