// AutoTextarea 的列數整理(純邏輯)。
import { test } from "node:test";
import assert from "node:assert/strict";
import { measuredHeight, normalizeRows } from "../src/components/AutoTextarea/autoTextareaModel.ts";

test("正常範圍原樣回傳", () => assert.deepEqual(normalizeRows(2, 8), { min: 2, max: 8 }));
test("最小至少 1 列", () => assert.deepEqual(normalizeRows(0, 4), { min: 1, max: 4 }));
test("最大不小於最小", () => assert.deepEqual(normalizeRows(5, 3), { min: 5, max: 5 }));
test("非整數與 NaN 不會壞掉", () => assert.deepEqual(normalizeRows(Number.NaN, 2.7), { min: 1, max: 2 }));

test("量得到時加上框線", () => assert.equal(measuredHeight(40, 2), 42));
test("量不到(隱藏中,scrollHeight 為 0)回傳 undefined,不寫 0px", () => assert.equal(measuredHeight(0, 0), undefined));
