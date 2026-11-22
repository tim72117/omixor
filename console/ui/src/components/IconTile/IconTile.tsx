import { Icon } from "../Icon";
import s from "./IconTile.module.css";

/** 紫色漸層圓角方塊裡的白色圖示(AI 自動化的圖示);dimmed 是停用 */
export function IconTile({ icon, size = 34, dimmed }: { icon: string; size?: number; dimmed?: boolean }) {
  return (
    <span className={s.tile + (dimmed ? " " + s.dim : "")} style={{ width: size, height: size, borderRadius: size * 0.26 }}>
      <Icon name={icon} size={size * 0.48} color="white" />
    </span>
  );
}
