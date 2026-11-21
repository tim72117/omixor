// 登入回應處理(src/auth/loginError.ts):非 JSON 回應、429 的 Retry-After、503 / 500 的說明
import { test } from "node:test";
import assert from "node:assert/strict";
import { parseAuthResponse, waitText } from "../src/auth/loginError.ts";

// 丟出的錯誤訊息(沒丟就讓測試失敗);shim 的 assert 沒有 throws,自己包
function errOf(f: () => unknown): string {
  try {
    f();
  } catch (e) {
    return (e as Error).message;
  }
  throw new Error("應該丟出錯誤");
}

test("成功:取 result 或整個物件", () => {
  assert.deepEqual(parseAuthResponse(200, true, null, '{"result":{"id":"bob"}}'), { id: "bob" });
  assert.deepEqual(parseAuthResponse(200, true, null, '{"mode":"ldap"}'), { mode: "ldap" });
});

test("帳號不存在與密碼錯誤是同一句 401", () => {
  assert.equal(errOf(() => parseAuthResponse(401, false, null, '{"error":"帳號或密碼錯誤"}')), "帳號或密碼錯誤");
});

test("429:帶等待時間", () => {
  const m = errOf(() => parseAuthResponse(429, false, "125", '{"error":"登入失敗太多次,請 3 分鐘後再試","retryAfter":125}'));
  assert.ok(m.includes("登入失敗太多次") && m.includes("3 分鐘"), m);
  { const m = errOf(() => parseAuthResponse(429, false, "30", '{"error":"太多次"}')); assert.ok(m.includes("30 秒"), m); }
  assert.equal(errOf(() => parseAuthResponse(429, false, null, "")), "嘗試太多次,請稍後再試");
});

test("503 / 500:沒有訊息時給固定說明", () => {
  assert.equal(errOf(() => parseAuthResponse(503, false, null, '{"error":"LDAP 的服務帳號設定有問題,請聯絡管理員"}')), "LDAP 的服務帳號設定有問題,請聯絡管理員");
  { const m = errOf(() => parseAuthResponse(503, false, null, "")); assert.ok(m.includes("登入服務暫時不能使用"), m); }
  { const m = errOf(() => parseAuthResponse(500, false, null, "<html>oops</html>")); assert.ok(m.includes("伺服器發生錯誤"), m); }
});

test("非 JSON 回應:易懂的錯誤,不是 JSON 解析錯誤", () => {
  { const m = errOf(() => parseAuthResponse(200, true, null, "<html>login</html>")); assert.ok(m.includes("回應格式不對") && !/JSON|Unexpected/i.test(m), m); }
  { const m = errOf(() => parseAuthResponse(502, false, null, "<html>Bad Gateway</html>")); assert.ok(m.includes("伺服器發生錯誤"), m); }
  { const m = errOf(() => parseAuthResponse(404, false, null, "Not Found")); assert.ok(m.includes("HTTP 404"), m); }
});

test("waitText", () => {
  assert.equal(waitText(0), "");
  assert.equal(waitText(45), "45 秒");
  assert.equal(waitText(61), "2 分鐘");
});
