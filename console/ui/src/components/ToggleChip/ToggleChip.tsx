import { color } from "../../lib/color";
import s from "./ToggleChip.module.css";

/** 可切換的篩選膠囊(Swift 的 MonToggleChip) */
export function ToggleChip({ title, count, c, on, onClick, onContextMenu }: { title: string; count?: number; c: string; on: boolean; onClick: () => void; onContextMenu?: (e: React.MouseEvent) => void }) {
  const col = color(c);
  return (
    <button
      className={s.chip}
      onClick={onClick}
      onContextMenu={onContextMenu}
      style={{
        background: on ? `color-mix(in srgb, ${col} 14%, transparent)` : "color-mix(in srgb, var(--fg) 4%, transparent)",
        borderColor: on ? `color-mix(in srgb, ${col} 35%, transparent)` : "var(--line)",
        color: on ? "var(--fg)" : "var(--fg2)",
      }}
    >
      <span className={s.dot} style={{ background: col, opacity: on ? 1 : 0.35 }} />
      {title}
      {count != null && <span className="muted num">{count}</span>}
    </button>
  );
}
