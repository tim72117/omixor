// 側邊欄下方:目前登入的人與登出(沒啟用 LDAP 時只顯示名字)
import { Icon } from "../components/Icon";
import { logout, useAuth } from "./auth";

export function UserMenu() {
  const s = useAuth();
  if (!s?.user) return null;
  return (
    <div className="row gap6" style={{ padding: "6px 8px" }} title={s.user.dn ?? s.user.id}>
      <Icon name="person.crop.circle" size={16} />
      <div className="col grow">
        <span className="ellipsis">{s.user.name}</span>
        <span className="caption muted ellipsis">{s.mode === "ldap" ? s.user.id + (s.user.admin ? " · 管理員" : "") : "本機模式"}</span>
      </div>
      {s.mode === "ldap" && (
        <button className="btn link" onClick={() => logout()} title="登出">登出</button>
      )}
    </div>
  );
}
