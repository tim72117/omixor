// 網址參數(?role=…)帶的 id 可能已不存在(角色被刪、分享過期的連結):只認清單裡有的,其餘當作沒指定。
// 純函式,不依賴 React,方便單元測試。

/** value 在 knownIds 裡就回 value;空值或不在清單裡回 undefined */
export function pickKnown(value: string | undefined, knownIds: readonly string[]): string | undefined {
  return value && knownIds.includes(value) ? value : undefined;
}
