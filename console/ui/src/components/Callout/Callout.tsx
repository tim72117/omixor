// 提示區塊:Callout(有色邊框,PipeCallout)(AICallout 在 AICallout.tsx)、Banner(提示列)
import type { CSSProperties, ReactNode } from "react";
import { color } from "../../lib/color";
import { SIcon } from "../SIcon";
import s from "./Callout.module.css";

export function Callout({ tint, icon, children, style }: { tint: string; icon: string; children: ReactNode; style?: CSSProperties }) {
  const c = color(tint);
  return (
    <div className={s.callout} style={{ background: `color-mix(in srgb, ${c} 8%, transparent)`, borderColor: `color-mix(in srgb, ${c} 28%, transparent)`, ...style }}>
      <SIcon name={icon} size={22} color={tint} />
      <div className="col gap8 grow">{children}</div>
    </div>
  );
}

/** 錯誤或後端尚未提供時的提示列;沒有內容不顯示 */
export function Banner({ children, tint = "orange" }: { children: ReactNode; tint?: string }) {
  if (!children) return null;
  return <div className={s.banner} style={{ color: color(tint), background: `color-mix(in srgb, ${color(tint)} 8%, transparent)` }}><SIcon name="exclamationmark.triangle" size={12} />{children}</div>;
}
