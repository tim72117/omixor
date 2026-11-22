// 顏色名稱(Swift 的 Color 名)→ CSS 變數
import type { Person } from "../models";

export const COLORS: Record<string, string> = {
  red: "var(--red)", orange: "var(--orange)", yellow: "var(--yellow)", green: "var(--green)", teal: "var(--teal)",
  blue: "var(--blue)", indigo: "var(--indigo)", purple: "var(--purple)", pink: "var(--pink)", gray: "var(--gray)",
  secondary: "var(--fg2)", mint: "var(--teal)", cyan: "var(--teal)", brown: "var(--orange)",
};
/** 顏色名稱轉 CSS 顏色;不認得的原樣回傳(可直接給 #hex 或 var(--x)),沒給是次要文字色 */
export const color = (c?: string) => (c ? COLORS[c] ?? c : "var(--fg2)");

/** Swift 的 colorHex(0xRRGGBB)→ #rrggbb;沒有就用頭像預設底色 */
export const hexColor = (n?: number) => (n == null ? "var(--avatar-fill)" : `#${n.toString(16).padStart(6, "0")}`);
/** 人員頭像底色 */
export const personColor = (p?: Person) => hexColor(p?.colorHex);
