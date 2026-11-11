// 共用元件:對應 Swift 版 Components/Components.swift(Card、Pill、MetricTile、EmptyStateView、PageScroll)
import type { CSSProperties, ReactNode } from "react";
import { Icon } from "./Icon";

export function Card({ title, subtitle, children, style, right }: { title?: ReactNode; subtitle?: ReactNode; children?: ReactNode; style?: CSSProperties; right?: ReactNode }) {
  return (
    <div className="card" style={style}>
      {(title || subtitle || right) && (
        <div className="card-head">
          <div>
            {title && <div className="card-title">{title}</div>}
            {subtitle && <div className="caption muted">{subtitle}</div>}
          </div>
          {right}
        </div>
      )}
      {children}
    </div>
  );
}

export const COLORS: Record<string, string> = {
  red: "var(--red)", orange: "var(--orange)", yellow: "var(--yellow)", green: "var(--green)", teal: "var(--teal)",
  blue: "var(--blue)", indigo: "var(--indigo)", purple: "var(--purple)", pink: "var(--pink)", gray: "var(--gray)",
  secondary: "var(--fg2)", mint: "var(--teal)", cyan: "var(--teal)", brown: "var(--orange)",
};
export const color = (c?: string) => (c ? COLORS[c] ?? c : "var(--fg2)");

export function Pill({ text, color: c = "secondary", icon }: { text: ReactNode; color?: string; icon?: string }) {
  const col = color(c);
  return (
    <span className="pill" style={{ color: col, background: `color-mix(in srgb, ${col} 14%, transparent)` }}>
      {icon && <Icon name={icon} size={11} />}
      {text}
    </span>
  );
}

export function MetricTile({ title, value, sub, icon, tint }: { title: string; value: ReactNode; sub?: ReactNode; icon?: string; tint?: string }) {
  return (
    <div className="card metric">
      <div className="caption muted row gap4">{icon && <Icon name={icon} size={12} color={color(tint)} />}{title}</div>
      <div className="metric-value">{value}</div>
      {sub && <div className="caption muted">{sub}</div>}
    </div>
  );
}

export function EmptyState({ title, icon = "doc", message }: { title: string; icon?: string; message?: ReactNode }) {
  return (
    <div className="empty">
      <Icon name={icon} size={30} className="muted" />
      <div className="empty-title">{title}</div>
      {message && <div className="muted caption" style={{ maxWidth: 420, textAlign: "center" }}>{message}</div>}
    </div>
  );
}

export function PageScroll({ children }: { children: ReactNode }) {
  return <div className="page-scroll"><div className="page-inner">{children}</div></div>;
}

export function Segmented<T extends string | number>({ value, options, onChange, width }: { value: T; options: { value: T; label: ReactNode }[]; onChange: (v: T) => void; width?: number }) {
  return (
    <div className="segmented" style={width ? { width } : undefined}>
      {options.map((o) => (
        <button key={String(o.value)} className={o.value === value ? "on" : ""} onClick={() => onChange(o.value)}>{o.label}</button>
      ))}
    </div>
  );
}

export function Dot({ c, size = 8 }: { c: string; size?: number }) {
  return <span style={{ width: size, height: size, borderRadius: size, background: color(c), display: "inline-block", flex: "none" }} />;
}

export function ErrorText({ children }: { children?: ReactNode }) {
  if (!children) return null;
  return <div className="error-text"><Icon name="exclamationmark.triangle" size={12} /> {children}</div>;
}

/** 左清單、右詳情的版面 */
export function SplitView({ list, detail, listWidth = 320 }: { list: ReactNode; detail: ReactNode; listWidth?: number }) {
  return (
    <div className="split">
      <div className="split-list" style={{ width: listWidth }}>{list}</div>
      <div className="split-detail">{detail}</div>
    </div>
  );
}

export function Modal({ open, onClose, title, children, width = 520 }: { open: boolean; onClose: () => void; title?: ReactNode; children: ReactNode; width?: number }) {
  if (!open) return null;
  return (
    <div className="modal-bg" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" style={{ width }}>
        {title && <div className="modal-title">{title}</div>}
        {children}
      </div>
    </div>
  );
}
