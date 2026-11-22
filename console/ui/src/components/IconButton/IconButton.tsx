import { color } from "../../lib/color";
import { Icon } from "../Icon";
import s from "./IconButton.module.css";

/** 只有圖示的按鈕;點擊不往上傳(放在可點的列裡也不會觸發列) */
export function IconButton({ icon, title, onClick, tint, disabled, size = 14, className }: { icon: string; title?: string; onClick: () => void; tint?: string; disabled?: boolean; size?: number; className?: string }) {
  return (
    <button className={s.btn + (className ? " " + className : "")} title={title} disabled={disabled} onClick={(e) => { e.stopPropagation(); onClick(); }}>
      <Icon name={icon} size={size} color={tint ? color(tint) : undefined} />
    </button>
  );
}

/** 給自己組按鈕內容時用的 class:iconButtonClass.normal(有內距、hover 底色)、iconButtonClass.bare(無內距、hover 變淡) */
export const iconButtonClass = { normal: s.btn, bare: s.bare };
