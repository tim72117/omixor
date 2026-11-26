// 啟用 LDAP 時每個人各自的目前組織(契約見 console/internal/web/membership.go):選擇存在瀏覽器(依使用者分開),
// 每個 API 請求帶在 X-Omixor-Org;伺服器不記。分頁各自拿著載入時的值,切換的那個分頁重新載入。
// 沒啟用登入時沒有使用者,什麼都不帶,切換組織照舊改伺服器的目前組織。

const key = (user: string) => `omixor.org.${user}`;

let user = "";
let selected = "";

/** 登入者(只在 LDAP 模式給;要在發出任何組織資料的請求之前) */
export function setOrgUser(id: string) {
  if (id === user) return;
  user = id;
  try {
    selected = localStorage.getItem(key(id)) ?? "";
  } catch {
    selected = "";
  }
}

/** 記住這個人選的組織("" = 清掉,用伺服器的預設) */
export function selectOrg(id: string) {
  selected = id;
  if (!user) return;
  try {
    if (id) localStorage.setItem(key(user), id);
    else localStorage.removeItem(key(user));
  } catch {
    /* 存不了就只記在這個分頁 */
  }
}

export const selectedOrg = () => selected;

export const orgHeaders = (): Record<string, string> => (selected ? { "X-Omixor-Org": selected } : {});

/** 回應是「你選的組織不能用了」(409 orgChanged):改用伺服器給的組織並重新載入 */
export function followOrgChange(status: number, body: any) {
  if (status !== 409 || !body?.orgChanged || typeof window === "undefined") return;
  selectOrg(typeof body.org === "string" ? body.org : "");
  window.location.reload();
}
