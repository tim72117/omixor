// Markdown 連結的協定限制
import { test } from "node:test";
import assert from "node:assert/strict";
import { safeHref } from "../src/components/Markdown/safeHref.ts";

test("http、https、mailto、相對路徑、錨點可以點", () => {
  for (const u of ["http://a.test", "HTTPS://a.test/x?y=1", "mailto:a@b.test", "/api/x", "docs/a.md", "../a", "#top", "?q=a:b", "//a.test", "a/b:c"]) assert.equal(safeHref(u), true, u);
});

test("其他協定一律不給點", () => {
  for (const u of ["javascript:alert(1)", " JavaScript:alert(1)", "java\tscript:alert(1)", "java\nscript:alert(1)", "\u0001javascript:x", "data:text/html,x", "vbscript:x", "file:///etc/passwd", "ftp://a.test"]) assert.equal(safeHref(u), false, JSON.stringify(u));
});
