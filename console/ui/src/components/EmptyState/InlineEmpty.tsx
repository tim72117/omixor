import { SIcon } from "../SIcon";
import s from "./EmptyState.module.css";

/** 卡片內的小空狀態 */
export function InlineEmpty({ icon, text, c = "secondary" }: { icon: string; text: string; c?: string }) {
  return <div className={s.inline}><SIcon name={icon} color={c} />{text}</div>;
}
