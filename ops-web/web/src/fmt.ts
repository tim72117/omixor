// 格式化,對應 Swift 的 Fmt
const rtf = new Intl.RelativeTimeFormat("zh-TW", { numeric: "auto", style: "short" });

export function relative(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso).getTime();
  const s = (d - Date.now()) / 1000;
  const a = Math.abs(s);
  if (a < 60) return rtf.format(Math.round(s), "second");
  if (a < 3600) return rtf.format(Math.round(s / 60), "minute");
  if (a < 86400) return rtf.format(Math.round(s / 3600), "hour");
  if (a < 86400 * 30) return rtf.format(Math.round(s / 86400), "day");
  return rtf.format(Math.round(s / (86400 * 30)), "month");
}

export function dateTime(iso?: string | null): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString("zh-TW", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false });
}

export function duration(sec?: number | null): string {
  if (sec == null) return "—";
  const s = Math.round(sec);
  if (s < 60) return `${s} 秒`;
  if (s < 3600) return `${Math.floor(s / 60)} 分 ${s % 60} 秒`;
  return `${Math.floor(s / 3600)} 小時 ${Math.floor((s % 3600) / 60)} 分`;
}

export const pct = (v: number, digits = 0) => `${v.toFixed(digits)}%`;
