// 搜尋框(合併原本 bits.tsx 的 bx-search 與 omixorKit.tsx 的 ox-search)
import type { CSSProperties } from "react";
import { Icon } from "../Icon";
import { iconButtonClass } from "../IconButton";
import s from "./SearchField.module.css";

/**
 * variant:"outlined"(預設,白底加框)或 "filled"(淡灰底、無框)。
 * width 同時設成最大寬度;mono 讓輸入框用等寬字。
 */
export function SearchField({ value, onChange, placeholder, width, mono, className, inputClassName, style, variant = "outlined" }: {
  value: string; onChange: (v: string) => void; placeholder?: string; width?: number | string; mono?: boolean;
  className?: string; inputClassName?: string; style?: CSSProperties; variant?: "outlined" | "filled";
}) {
  const filled = variant === "filled";
  const inputCls = [mono ? "mono" : "", inputClassName ?? ""].filter(Boolean).join(" ") || undefined;
  return (
    <div className={s.root + " " + (filled ? s.filled : s.outlined) + (className ? " " + className : "")} style={{ ...(width != null ? { width, maxWidth: width } : null), ...style }}>
      <Icon name="magnifyingglass" size={filled ? 14 : 13} className="muted" />
      <input className={inputCls} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
      {value && (
        <button className={filled ? iconButtonClass.bare : iconButtonClass.normal} onClick={() => onChange("")} title={filled ? "清除搜尋" : "清除"}>
          <Icon name="xmark.circle.fill" size={filled ? 14 : 13} className="faint" />
        </button>
      )}
    </div>
  );
}
