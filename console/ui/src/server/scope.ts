// 這個畫面接的是哪一種後端。omixor server(/console)的登入狀態會帶 capabilities(server/webauth.go):只有列出來的頁面與分頁有後端,
// 其他一律不顯示。console 沒有 capabilities = 全部都有,行為不變。不依賴 React(contract/serverAdapter.test.ts)。

export type Capabilities = { sections: string[]; devTabs: string[] };

let caps: Capabilities | undefined;

export function setCapabilities(c: Capabilities | undefined) {
  caps = c;
}

/** 接的是 omixor server(畫面功能受限、資料走核心 REST) */
export const onServer = () => caps !== undefined;

/** 功能開關的 key(「頁面」或「頁面.分頁」)在這個後端有沒有 */
export function backendHas(key: string): boolean {
  if (!caps) return true;
  const [section, tab] = key.split(".");
  if (!caps.sections.includes(section)) return false;
  return !tab || section !== "development" || caps.devTabs.includes(tab);
}

/** 開發進度的分頁(沒有功能開關的分頁也要過這裡) */
export const backendDevTabs = <T extends string>(tabs: T[]): T[] => (caps ? tabs.filter((t) => caps!.devTabs.includes(t)) : tabs);

const ATTACHMENT = /^\/api\/attachments\/([0-9a-f]{32}\.(png|jpg|gif|webp))$/;

/** 內文裡存的附件網址(/api/attachments/<id>)換成這個後端能直接顯示的網址;內容本身不改 */
export function attachmentSrc(src: string, org: string): string {
  const m = ATTACHMENT.exec(src);
  return caps && m && org ? `/api/web/orgs/${encodeURIComponent(org)}/attachments/${m[1]}` : src;
}
