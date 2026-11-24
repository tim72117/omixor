// src/lib/navRoute.ts 的單元測試(npm test):網址 ⇄ 路由的編碼與解碼、無效輸入的處理。
import { test } from "node:test";
import assert from "node:assert/strict";
import { encodeRoute, makeRoute, MAX_PARAMS, MAX_VALUE_LENGTH, parseRoute, sameRoute, withParams, withoutFocus } from "../src/lib/navRoute.ts";

const SECTIONS = new Set(["overview", "development", "settings", "automation"]);
const isSection = (s: string) => SECTIONS.has(s);
const parse = (h: string) => parseRoute(h, isSection);

test("編碼:頁面、focusId、query(依 key 排序)", () => {
  assert.equal(encodeRoute(makeRoute("overview")), "#/overview");
  assert.equal(encodeRoute(makeRoute("development", "ISS-5")), "#/development/ISS-5");
  assert.equal(encodeRoute(makeRoute("development", undefined, { tab: "issues", issue: "abc" })), "#/development?issue=abc&tab=issues");
  assert.equal(encodeRoute(makeRoute("development", "", { tab: undefined })), "#/development");
});

test("來回轉換:特殊字元都還原", () => {
  const focus = ["#12", "github:owner/name", "a b&c=d?e#f%g", "中文 id", "../x", "/"];
  for (const f of focus) {
    const r = makeRoute("development", f, { tab: "issues", q: f });
    assert.deepEqual(parse(encodeRoute(r)), r, f);
  }
  // focusId 裡的 / 要編碼,不能多出一段路徑
  assert.equal(encodeRoute(makeRoute("development", "github:o/n")), "#/development/github%3Ao%2Fn");
});

test("解碼:缺 #/、不認得的頁面、多餘路徑、壞掉的 % 編碼 → null", () => {
  for (const h of ["", "#", "#/", "#overview", "/overview", "#/nope", "#/development/a/b", "#/%E0%A4%A", "#/development/%E0%A4%A", "#//x"]) {
    assert.equal(parse(h), null, h);
  }
});

test("解碼:尾端斜線與空 focusId 視為沒有", () => {
  assert.deepEqual(parse("#/development/"), makeRoute("development"));
  assert.deepEqual(parse("#/development/?tab=board"), makeRoute("development", undefined, { tab: "board" }));
});

test("解碼:query 裡不合規的項目丟掉,其他照留", () => {
  const long = "x".repeat(MAX_VALUE_LENGTH + 1);
  const r = parse(`#/development?tab=board&Bad=1&1x=2&ok_1=%E4%B8%AD&ctl=a%00b&long=${long}&broken=%E0%A4%A&empty=&tab=dup`);
  assert.deepEqual(r, makeRoute("development", undefined, { tab: "board", ok_1: "中" }));
});

test("解碼:query 數量有上限", () => {
  const q = Array.from({ length: 20 }, (_, i) => `k${String(i).padStart(2, "0")}=v`).join("&");
  assert.equal(Object.keys(parse("#/development?" + q)!.params).length, MAX_PARAMS);
});

test("解碼:值是資料不是程式(尖括號、引號原樣保留,由 React 跳脫顯示)", () => {
  const r = parse("#/development?tab=%3Cscript%3E%22");
  assert.equal(r!.params.tab, '<script>"');
  assert.equal(encodeRoute(r!), "#/development?tab=%3Cscript%3E%22");
});

test("同一個路由只有一種寫法,sameRoute 比的是內容", () => {
  assert.ok(sameRoute(parse("#/development?b=2&a=1")!, parse("#/development?a=1&b=2")!));
  assert.ok(!sameRoute(makeRoute("development"), makeRoute("development", "x")));
});

test("withParams:合併、移除,而且拿掉一次性的 focusId", () => {
  const r = makeRoute("development", "ISS-1", { tab: "board", issue: "a" });
  assert.deepEqual(withParams(r, { issue: "b", tab: undefined }), makeRoute("development", undefined, { issue: "b" }));
  assert.deepEqual(withoutFocus(r), makeRoute("development", undefined, { tab: "board", issue: "a" }));
});
