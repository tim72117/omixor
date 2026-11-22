// 圖表共用的顏色與座標軸樣式(不 import recharts:只要顏色的頁面不會因此下載 recharts)
export const SERIES_COLORS = ["var(--blue)", "var(--green)", "var(--orange)", "var(--purple)", "var(--pink)", "var(--teal)", "var(--indigo)", "var(--red)", "var(--yellow)", "var(--gray)"];
export const axisStyle = { fontSize: 11, fill: "var(--fg2)" };
/** XAxis / YAxis 的共用設定:<XAxis {...axisProps} />(刻度字樣 + 軸線顏色) */
export const axisProps = { tick: axisStyle, stroke: "var(--line)" };
