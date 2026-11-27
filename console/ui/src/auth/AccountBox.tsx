// 側欄左下角:目前是誰(LDAP 登入的帳號,或沒啟用登入時的本機使用者)與資料庫狀態;有 LDAP 登入時可以登出。
import { useRun, useView } from "../api";
import { Icon } from "../components/Icon";
import type { Account } from "./account";
import css from "./auth.module.css";
import { logout, useAuth } from "./auth";

export function AccountBox() {
  const [acc] = useView<Account>("account", ["account", "orgs"]);
  const auth = useAuth();
  const out = useRun();
  if (!acc) return null;
  const ldap = auth?.mode === "ldap" && !!auth.loggedIn;
  const me = auth?.user;
  const sub = (acc.localStorage === "postgres" ? "資料庫" : "沒有資料庫") + (ldap && me?.admin ? " · 管理員" : "");
  return (
    <div className={css.box}>
      <div className={css.current} title={acc.error}>
        <Icon name="person.crop.circle" size={16} />
        <div className={"col grow " + css.currentText}>
          <span className="ellipsis">{me?.name ?? "本機使用者"}</span>
          <span className="caption muted ellipsis">{sub}</span>
        </div>
        {acc.error && <span className={css.errDot} />}
      </div>
      {ldap && (
        <button className={css.logout} disabled={out.busy} onClick={() => void out.run(logout)}>
          <Icon name="rectangle.portrait.and.arrow.right" size={13} /> 登出
        </button>
      )}
      {out.error && <div className={"caption " + css.err}>{out.error}</div>}
    </div>
  );
}
