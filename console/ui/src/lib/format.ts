// 格式化(對應 Swift 的 Fmt)。原本分散在 bits.tsx(hm、shortDuration、num、percent)與 omixorKit.tsx(t、now、dur、pct…)。

/** ISO 字串 → 毫秒;空值是 NaN */
export const t = (iso?: string | null) => (iso ? new Date(iso).getTime() : NaN);
/** 同 t,但沒有時間時當作現在(用在「還沒結束」的區間,例如事件持續時間) */
export const tOrNow = (iso?: string | null) => (iso ? new Date(iso).getTime() : Date.now());
export const now = () => Date.now();

/** HH:mm(toLocaleTimeString;空值是「—」) */
export const hm = (iso?: string | null) => (iso ? new Date(iso).toLocaleTimeString("zh-TW", { hour: "2-digit", minute: "2-digit", hour12: false }) : "—");

/** Fmt.duration:45s、3m 5s、1h 2m;空值或 NaN 是「—」 */
export function dur(sec?: number | null): string {
  if (sec == null || isNaN(sec)) return "—";
  const s = Math.floor(sec);
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}
/** 同 dur(bits.tsx 的舊名) */
export const shortDuration = dur;

/** Fmt.percent */
export const pct = (v: number, digits = 1) => `${v.toFixed(digits)}%`;
/** 同 pct(bits.tsx 的舊名) */
export const percent = pct;

/** Fmt.number:1.2k、3.4M */
export function num(v: number): string {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
  return v.toFixed(0);
}

export function monthDay(iso: string | number): string {
  const d = new Date(iso);
  return `${d.getMonth() + 1}月${d.getDate()}日`;
}
export function monthDayTime(iso: string | number): string {
  const d = new Date(iso);
  return `${monthDay(iso)} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}
/** HH:mm(自己補零;接受毫秒) */
export function hhmm(iso: string | number): string {
  const d = new Date(iso);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}
export function startOfDay(ms: number): number {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}
export const isToday = (iso: string) => startOfDay(t(iso)) === startOfDay(now());
