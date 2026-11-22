import type { ReactNode } from "react";
import { color } from "../../lib/color";
import { SIcon } from "../SIcon";
import s from "./Card.module.css";

/** 標題列:標題、數量徽章、右側「全部 >」連結(OverviewCardHeader) */
export function CardHeader({ title, count, countColor = "secondary", linkTitle = "全部", onLink, icon }: {
  title: string; count?: number; countColor?: string; linkTitle?: string; onLink?: () => void; icon?: ReactNode;
}) {
  const plain = countColor === "secondary";
  return (
    <div className="row gap8">
      {icon}
      <span className={s.title}>{title}</span>
      {count != null && count > 0 && (
        <span className={s.count} style={plain ? undefined : { background: color(countColor), color: "white" }}>{count}</span>
      )}
      <span className="grow" />
      {onLink && <button className="btn link caption" onClick={onLink}>{linkTitle} <SIcon name="chevron.right" size={10} /></button>}
    </div>
  );
}
