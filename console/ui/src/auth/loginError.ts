// 登入與認證設定 API 的回應處理(不依賴 React,可以用 npm test 直接跑)。
// 後端的登入狀態碼:401 帳號或密碼錯誤(帳號不存在也是同一句)、429 失敗太多次被鎖(帶 Retry-After 秒數)、
// 503 LDAP 連不上或服務帳號有問題(固定句子)、500 內部錯誤(例如 session 沒存起來)。

/** 秒數轉成「N 分鐘 / N 秒」 */
export function waitText(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return "";
  return sec >= 60 ? `${Math.ceil(sec / 60)} 分鐘` : `${Math.ceil(sec)} 秒`;
}

/**
 * 把一個 fetch 回應變成成功的結果,或丟出易懂的錯誤:
 * 回應不是 JSON(反向代理的錯誤頁、空回應)給「伺服器回應格式不對」而不是 JSON 解析的英文錯誤;
 * 429 補上 Retry-After 的等待時間;503 / 500 沒有訊息時給固定說明。
 */
export function parseAuthResponse(status: number, ok: boolean, retryAfter: string | null, raw: string): any {
  let out: any;
  try {
    out = raw === "" ? undefined : JSON.parse(raw);
  } catch {
    out = undefined;
    if (ok) throw new Error(`伺服器回應格式不對(HTTP ${status}),請稍後再試`);
  }
  const msg = typeof out?.error === "string" && out.error ? out.error : "";
  if (msg || !ok) {
    let text = msg;
    if (!text) {
      text =
        status === 429 ? "嘗試太多次,請稍後再試"
        : status === 503 ? "登入服務暫時不能使用,請稍後再試;若持續發生請聯絡管理員"
        : status >= 500 ? "伺服器發生錯誤,請稍後再試;若持續發生請聯絡管理員"
        : `伺服器回應格式不對(HTTP ${status}),請稍後再試`;
    }
    if (status === 429) {
      const w = waitText(Number(retryAfter));
      if (w && !text.includes(w)) text += `(約 ${w}後可再試)`;
    }
    throw new Error(text);
  }
  return out?.result ?? out;
}
