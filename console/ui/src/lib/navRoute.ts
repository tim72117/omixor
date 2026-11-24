// 網址路由 ⇄ 畫面位置的純函式(不碰 window / React,可以直接單元測試;contract/navRoute.test.ts)。
//
// 格式:  #/<section>[/<focusId>][?<key>=<value>&…]
//   section   頁面 id(SectionId),不認得的整個網址視為無效
//   focusId   其他頁跳過來時要選中的項目(一次性的「意圖」,頁面用完會清掉)
//   query     頁面自己的子狀態(分頁、選中的項目):key 只允許 [a-z][a-z0-9_]*,值是不透明字串
//
// 安全:網址是使用者(或別人貼的連結)可以任意改的輸入。解析一律當資料看待:
//   - 結構不對(沒有 #/、多餘的路徑、壞掉的 % 編碼、不認得的頁面)→ null,呼叫端退回預設頁
//   - query 裡不合規則的項目(key 不合法、值太長、含控制字元、超過數量上限)直接丟掉,不影響其他項目
//   - 編碼一律 encodeURIComponent;值只用來比對與選取(React 會跳脫顯示),不會被當成程式或網址執行
// 網址會留在瀏覽器歷史與書籤:只能放 id 與分頁名稱這類識別字,不要放 token、密碼、金鑰或個資。

export type RouteParams = Readonly<Record<string, string>>;
export type Route = { section: string; focusId?: string; params: RouteParams };

export const MAX_VALUE_LENGTH = 200;
export const MAX_PARAMS = 8;
const KEY_RE = /^[a-z][a-z0-9_]{0,23}$/;
// eslint-disable-next-line no-control-regex
const CONTROL_RE = /[\u0000-\u001f\u007f]/;

const validValue = (v: string) => v.length <= MAX_VALUE_LENGTH && !CONTROL_RE.test(v);

function decode(s: string): string | undefined {
  try { return decodeURIComponent(s); } catch { return undefined; }
}

/** 只留下合規的項目(空值視為沒有),依 key 排序,最多 MAX_PARAMS 個 */
export function cleanParams(params: Readonly<Record<string, string | undefined>>): RouteParams {
  const out: Record<string, string> = {};
  for (const k of Object.keys(params).sort()) {
    const v = params[k];
    if (v && KEY_RE.test(k) && validValue(v) && Object.keys(out).length < MAX_PARAMS) out[k] = v;
  }
  return out;
}

/** 組出路由;focusId 空字串視為沒有 */
export function makeRoute(section: string, focusId?: string | null, params: Readonly<Record<string, string | undefined>> = {}): Route {
  const focus = focusId && validValue(focusId) ? focusId : undefined;
  return { section, ...(focus ? { focusId: focus } : {}), params: cleanParams(params) };
}

/** 路由 → "#/section[/focus][?k=v…]"(同一個路由永遠編成同一個字串,可拿來比較) */
export function encodeRoute(r: Route): string {
  const q = Object.entries(cleanParams(r.params)).map(([k, v]) => `${k}=${encodeURIComponent(v)}`).join("&");
  const focus = r.focusId && validValue(r.focusId) ? "/" + encodeURIComponent(r.focusId) : "";
  return `#/${encodeURIComponent(r.section)}${focus}${q ? "?" + q : ""}`;
}

function parseQuery(q: string): RouteParams {
  const out: Record<string, string | undefined> = {};
  for (const part of q.split("&")) {
    if (!part) continue;
    const i = part.indexOf("=");
    const k = decode(i < 0 ? part : part.slice(0, i));
    const v = decode(i < 0 ? "" : part.slice(i + 1));
    if (k !== undefined && v !== undefined && !(k in out)) out[k] = v; // 重複的 key 以第一個為準
  }
  return cleanParams(out);
}

/** location.hash → 路由;格式不對或頁面不認得回傳 null */
export function parseRoute(hash: string, isSection: (s: string) => boolean): Route | null {
  if (!hash.startsWith("#/")) return null;
  const body = hash.slice(2);
  const qi = body.indexOf("?");
  const path = qi < 0 ? body : body.slice(0, qi);
  const segs = path.split("/");
  if (segs.length > 2) return null;
  const section = decode(segs[0]);
  if (section === undefined || !isSection(section)) return null;
  const focusId = segs.length === 2 ? decode(segs[1]) : "";
  if (focusId === undefined) return null;
  return makeRoute(section, focusId, qi < 0 ? {} : parseQuery(body.slice(qi + 1)));
}

export const sameRoute = (a: Route, b: Route) => encodeRoute(a) === encodeRoute(b);

/** 改子狀態:undefined / 空字串 = 移除該項。一次性的 focusId 在這時已經用掉,所以一併拿掉 */
export function withParams(r: Route, patch: Readonly<Record<string, string | undefined>>): Route {
  return makeRoute(r.section, undefined, { ...r.params, ...patch });
}

/** 拿掉 focusId(其他都不變) */
export const withoutFocus = (r: Route): Route => makeRoute(r.section, undefined, r.params);
