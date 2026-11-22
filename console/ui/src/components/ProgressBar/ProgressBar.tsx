// 進度條:Bar(細長條)、ProgressBar(條 + 百分比,PipeProgressBar)、UsageBar(0…100 使用率,依數值變色)
import type { CSSProperties } from "react";
import { color } from "../../lib/color";
import s from "./ProgressBar.module.css";

/** 細長進度條;value 0...1 */
export function Bar({ value, c = "blue", height = 6, track, className, style }: { value: number; c?: string; height?: number; track?: string; className?: string; style?: CSSProperties }) {
  return (
    <div className={s.bar + (className ? " " + className : "")} style={{ height, background: track, ...style }}>
      <div style={{ width: `${Math.min(1, Math.max(0, value)) * 100}%`, background: color(c) }} />
    </div>
  );
}

/** 進度條 + 百分比;value 0...1 */
export function ProgressBar({ value, c = "blue", showLabel = true, className, style }: { value: number; c?: string; showLabel?: boolean; className?: string; style?: CSSProperties }) {
  return (
    <div className={"row gap6 " + s.progress + (className ? " " + className : "")} style={style}>
      <div className={s.bar} style={{ height: 6 }}><div style={{ width: `${Math.max(2, Math.min(1, Math.max(0, value)) * 100)}%`, background: color(c), transition: "width .6s" }} /></div>
      {showLabel && <span className={"num faint " + s.pct}>{Math.round(value * 100)}%</span>}
    </div>
  );
}

/** 0...100 的使用率條 */
export function UsageBar({ value, label, className }: { value: number; label?: string; className?: string }) {
  const c = value > 85 ? "red" : value > 70 ? "orange" : "green";
  return (
    <div className={"row gap8 " + s.progress + (className ? " " + className : "")}>
      {label && <span className={"caption muted " + s.label}>{label}</span>}
      <Bar value={value / 100} c={c} />
      <span className={"caption muted num " + s.value}>{Math.trunc(value)}%</span>
    </div>
  );
}
