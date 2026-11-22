// 伺服器與服務頁內部共用(Swift 的 InfraShared.swift)
import type { ReactNode } from "react";
import { Pill } from "../../components/Pill";
import { Bar } from "../../components/ProgressBar";
import { SIcon } from "../../components/SIcon";
import type { MetricPoint } from "../../models";
import css from "./infrastructure.module.css";
import { cardTitleClass } from "../../components/Card";

const M64 = (1n << 64n) - 1n;
/** 穩定的字串雜湊(FNV-1a 64) */
export function infraHash(s: string): bigint {
  let h = 1469598103934665603n;
  for (const b of new TextEncoder().encode(s)) { h ^= BigInt(b); h = (h * 1099511628211n) & M64; }
  return h;
}

/** 由目前數值往回推一段看起來合理的歷史曲線;同一個 seed 形狀固定,最後一點等於 current */
export function synthSeries(seed: string, current: number, spanSec: number, count = 48, volatility = 0.15, floor = 0): MetricPoint[] {
  if (count < 2) return [];
  let state = infraHash(seed) | 1n;
  const next = () => {
    state = (state * 6364136223846793005n + 1442695040888963407n) & M64;
    return Number(state >> 11n) / 2 ** 53;
  };
  const end = Math.floor(Date.now() / 60000) * 60000;
  const step = (spanSec * 1000) / (count - 1);
  const phase = next() * Math.PI * 2;
  const base = Math.max(Math.abs(current), 0.0001);
  const pts: MetricPoint[] = [];
  for (let i = 0; i < count; i++) {
    const t = i / (count - 1);
    const wave = Math.sin(t * Math.PI * 4 + phase) * 0.5 + Math.sin(t * Math.PI * 11 + phase * 2) * 0.25;
    const noise = next() - 0.5;
    const weight = 1 - t * t;
    const v = i === count - 1 ? current : current + base * volatility * (wave + noise) * (0.3 + weight);
    pts.push({ date: new Date(end - spanSec * 1000 + step * i).toISOString(), value: Math.max(floor, v) });
  }
  return pts;
}

export const usageColor = (v: number) => (v > 85 ? "red" : v > 70 ? "orange" : "green");
export const budgetColor = (r: number) => (r < 0.2 ? "red" : r < 0.5 ? "orange" : "green");

export function TierPill({ tier }: { tier: number }) {
  return <span title={`關鍵等級 ${tier}(1 = 最關鍵)`}><Pill text={`T${tier}`} color={tier <= 1 ? "purple" : tier === 2 ? "blue" : "gray"} /></span>;
}

export function BudgetBar({ remaining, height = 6, width }: { remaining: number; height?: number; width?: number }) {
  return <Bar value={remaining} c={budgetColor(remaining)} height={height} style={{ width, flex: width ? "none" : 1 }} />;
}

/**
 * 服務/主機的圓角標籤(可點時是按鈕、尾端有 ›)。和 components/Chip 外觀不同(膠囊圓角、--fs-base、圖示可上色、可點),所以留在這裡。
 */
export function Chip({ text, c = "secondary", icon, onClick, title }: { text: string; c?: string; icon?: string; onClick?: () => void; title?: string }) {
  const body = <>{icon && <SIcon name={icon} color={c} size={12} />}<span className="ellipsis">{text}</span>{onClick && <SIcon name="chevron.right" size={9} className="faint" />}</>;
  return onClick ? <button className={css.chip} title={title} onClick={onClick}>{body}</button> : <span className={css.chip} title={title}>{body}</span>;
}

export function SectionHeader({ title, trailing }: { title: string; trailing?: ReactNode }) {
  return <div className={css.baseline}><span className={cardTitleClass}>{title}</span><span className="grow" />{trailing && <span className="caption muted">{trailing}</span>}</div>;
}

export function minutes(m: number) {
  if (m < 1) return `${(m * 60).toFixed(0)} 秒`;
  if (m < 120) return `${m.toFixed(0)} 分鐘`;
  return `${(m / 60).toFixed(1)} 小時`;
}
