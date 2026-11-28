// 還沒有組織時的畫面(不自動建立預設組織;登入成功就能進來,不在連線時擋下):
//   - web 管理員(沒啟用登入時就是本機使用者):直接建立第一個組織
//   - 其他人:說明要請管理員處理;組織清單一有內容就自動進入主畫面
// 啟用登入時可以登出。已經有組織時直接顯示主畫面。
import { useEffect, useState, type ReactNode } from "react";
import { action, useRun, useView } from "../api";
import { ErrorText } from "../components/ErrorText";
import { Icon } from "../components/Icon";
import { logout, useAuth, useIsWebAdmin } from "./auth";
import css from "./auth.module.css";

type OrgIndex = {
  current: string;
  orgs: { id: string }[];
  /** 讀不到組織的原因(沒有資料庫等) */
  error?: string;
  /** 開了範本時,第一個組織的預填值 */
  suggested?: { name: string; symbol: string; color: string };
};

export function OrgGate({ children }: { children: ReactNode }) {
  const [idx] = useView<OrgIndex>("orgs");
  if (!idx || idx.orgs.length > 0) return <>{children}</>;
  return <NoOrgs idx={idx} />;
}

function NoOrgs({ idx }: { idx: OrgIndex }) {
  const { busy, error, run } = useRun();
  const [name, setName] = useState(idx.suggested?.name ?? "");
  const admin = useIsWebAdmin();
  const auth = useAuth();
  useEffect(() => void (document.title = "沒有組織 · omixor"), []);
  const create = () =>
    run(async () => {
      await action("org_create", { name: name.trim(), symbol: idx.suggested?.symbol ?? "building.2", color: idx.suggested?.color ?? "blue", switch: true });
    });
  return (
    <div className={css.bg}>
      <div className={css.card + " " + css.wideCard}>
        <div className={"row gap8 " + css.center}>
          <Icon name="building.2" size={22} />
          <span className="h2">還沒有組織</span>
        </div>
        {idx.error ? (
          <div className="caption">讀不到組織:{idx.error}</div>
        ) : admin ? (
          <>
            <div className="caption">資料庫裡還沒有任何組織。建立第一個組織後就會進入主畫面,之後可以在「設定 › 組織」改名稱、圖示或新增其他組織。</div>
            <input className="input" placeholder="組織名稱(例如 我的團隊)" value={name} autoFocus
              onChange={(e) => setName(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter" && !e.nativeEvent.isComposing && name.trim() && !busy) void create(); }} />
            <button className="btn primary" disabled={!name.trim() || busy} onClick={() => void create()}>
              {busy ? "建立中…" : "建立組織"}
            </button>
          </>
        ) : (
          <div className="caption">{auth?.mode === "server"
            ? "你的帳號還沒有加入任何組織。請組織管理員把你加進去;加入後這個畫面會自動進入主畫面(沒有的話請重新整理)。"
            : "還沒有任何組織。請管理員登入並建立第一個組織;建好後這個畫面會自動進入主畫面(沒有的話請重新整理)。"}</div>
        )}
        <ErrorText>{error}</ErrorText>
        {(auth?.mode === "ldap" || auth?.mode === "server") && (
          <div className={"row gap8 " + css.center}>
            <button className="btn link" disabled={busy} onClick={() => void run(logout)}>登出</button>
          </div>
        )}
      </div>
    </div>
  );
}
