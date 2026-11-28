/** Markdown 連結能不能點:只收 http、https、mailto,以及沒有協定的(相對路徑、#錨點) */
export function safeHref(href: string): boolean {
  // 瀏覽器解析網址時會忽略夾在中間的換行、tab 與開頭的控制字元,先拿掉再判斷
  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(href.replace(/[\u0000- ]/g, ""));
  return !scheme || /^(https?|mailto)$/i.test(scheme[1]);
}
