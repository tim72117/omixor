// HTTP 細節(不依賴 React;api.ts 從這裡轉出,contract 測試直接 import 這個檔)
//
// 回應標頭 X-Omixor-Version:伺服器算 view / 執行 action 前的資料版本(見 internal/web/web.go setDataVersion),
// viewStore 拿來判斷 SSE 通知是不是已經反映在手上的資料裡。

import { followOrgChange, orgHeaders } from "./orgSelection.ts";

/** status:HTTP 狀態碼(action 才有);data:回應裡 error 以外的欄位(例如刪角色 409 的 code、taskCounts、total) */
export class ApiError extends Error {
  status: number;
  data?: Record<string, any>;
  constructor(message: string, status = 0, data?: Record<string, any>) {
    super(message);
    this.status = status;
    this.data = data;
  }
}

/** 讀回應內容:非 2xx 也要讀(內容有 error 與 current、currentVersion、taskCounts 等欄位);不是 JSON(例如代理的錯誤頁)回 null */
async function readBody(r: Response): Promise<any> {
  try {
    return await r.json();
  } catch {
    return null;
  }
}

function toApiError(r: { status: number }, body: any): ApiError {
  const { error, result: _result, ...rest } = body ?? {};
  return new ApiError(error ?? `HTTP ${r.status}`, r.status, Object.keys(rest).length ? rest : undefined);
}

/**
 * POST 回應 → 結果:失敗(有 error 或非 2xx)丟 ApiError;2xx 但內容不是 {result: …}(null、HTML 錯誤頁、代理吐的空回應)
 * 丟看得懂的 ApiError,而不是之後用到 undefined 才冒出 TypeError
 */
export function unwrapPost<T>(r: { ok: boolean; status: number }, body: any): T {
  if (body?.error || !r.ok) throw toApiError(r, body);
  if (body === null || typeof body !== "object" || Array.isArray(body) || !("result" in body)) {
    throw new ApiError(`伺服器回應的格式不對(HTTP ${r.status}),可能是連線被代理或閘道改寫了;請重新整理再試`, r.status);
  }
  return body.result as T;
}

/** 回應標頭的資料版本;沒有(舊版伺服器、被代理拿掉)是 undefined */
function dataVersion(r: Response): number | undefined {
  const v = Number(r.headers.get("X-Omixor-Version") ?? "");
  return r.headers.has("X-Omixor-Version") && Number.isFinite(v) ? v : undefined;
}

export async function getJSON<T>(url: string): Promise<{ data: T; version?: number }> {
  const r = await fetch(url, { headers: orgHeaders() });
  const body = await readBody(r);
  followOrgChange(r.status, body);
  if (!r.ok) throw toApiError(r, body);
  return { data: body as T, version: dataVersion(r) };
}

export async function postJSON<T>(url: string, args: unknown): Promise<{ data: T; version?: number }> {
  const r = await fetch(url, {
    method: "POST",
    // X-Omixor-CSRF:/api/orgs… 等核心 REST API 用 cookie 寫入時要帶
    headers: { "Content-Type": "application/json", "X-Omixor-CSRF": "1", ...orgHeaders() },
    body: JSON.stringify(args ?? {}),
  });
  const body = await readBody(r);
  followOrgChange(r.status, body);
  return { data: unwrapPost<T>(r, body), version: dataVersion(r) };
}
