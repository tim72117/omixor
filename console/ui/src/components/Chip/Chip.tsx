import { Icon } from "../Icon";
import s from "./Chip.module.css";

/** 小標籤(預設等寬字);sections/infrastructure/infraShared.tsx 另有一個 Chip(ox-chip),phase 2 合併 */
export function Chip({ text, icon, mono = true }: { text: string; icon?: string; mono?: boolean }) {
  return (
    <span className={s.tag + (mono ? " " + s.mono : "")}>
      {icon && <Icon name={icon} size={10} />}
      {text}
    </span>
  );
}
