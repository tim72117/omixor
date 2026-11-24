// AutoTextarea 的純邏輯:把外部傳入的列數整理成合理範圍(至少 1 列、最大不小於最小)。
export function normalizeRows(minRows: number, maxRows: number): { min: number; max: number } {
  const min = Math.max(1, Math.floor(minRows) || 1);
  const max = Math.max(min, Math.floor(maxRows) || min);
  return { min, max };
}

// 依 scrollHeight 算出該套用的高度(px);元素在 display:none 之類量不到時 scrollHeight 為 0,回傳 undefined,
// 呼叫端就不設內聯高度,退回 rows 預設,等變可見再量,避免寫入 "0px" 把輸入框藏起來。borderExtra 是 border-box 要補的上下框線。
export function measuredHeight(scrollHeight: number, borderExtra: number): number | undefined {
  return scrollHeight > 0 ? scrollHeight + borderExtra : undefined;
}
