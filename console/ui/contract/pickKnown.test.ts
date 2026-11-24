// src/lib/pickKnown.ts 的單元測試(npm test):網址上的 id 不在清單裡就當作沒指定。
import { test } from "node:test";
import assert from "node:assert/strict";
import { pickKnown } from "../src/lib/pickKnown.ts";

test("在清單裡:原值", () => assert.equal(pickKnown("a", ["a", "b"]), "a"));
test("不在清單裡(刪除的角色、過期連結):undefined", () => assert.equal(pickKnown("gone", ["a", "b"]), undefined));
test("沒指定或空字串:undefined", () => {
  assert.equal(pickKnown(undefined, ["a"]), undefined);
  assert.equal(pickKnown("", ["a"]), undefined);
});
test("清單是空的:undefined", () => assert.equal(pickKnown("a", []), undefined));
