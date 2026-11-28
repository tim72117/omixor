// /console/?login_error=<代碼>(server/webauth.go)的說明。只認固定代碼,網址上的文字不會顯示出來。
const TEXT: Record<string, string> = {
  denied: "這個帳號沒有 omixor 的權限(或已停用、不是用 Google 登入)。請 omixor 管理員加入。",
  throttled: "登入嘗試太頻繁,請一分鐘後再試。",
  expired: "登入已失效(逾時、重複使用,或不是在同一個瀏覽器開始的),請再按一次登入。",
  unavailable: "暫時無法登入,請稍後再試。",
};

export const loginErrorText = (code: string | null): string | undefined => (code ? TEXT[code] ?? TEXT.unavailable : undefined);
