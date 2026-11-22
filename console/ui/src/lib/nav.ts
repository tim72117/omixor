import { guardedNavigate } from "../features";
import type { SectionId } from "../types";

/** 跳到別頁並選中項目(Swift 的 app.go);系統設定關掉的頁面不跳,改在畫面上方提示 */
export const go = (section: SectionId, focusId?: string) => guardedNavigate(section, focusId);
