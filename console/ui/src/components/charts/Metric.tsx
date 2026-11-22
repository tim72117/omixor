// 數字指標卡(有 delta 與 sparkline;放在 charts/ 是因為會用到 recharts。沒有趨勢線的簡單版是 MetricTile)
import type { ReactNode } from "react";
import { cardClass } from "../Card";
import { SIcon } from "../SIcon";
import { Sparkline } from "./Sparkline";

/** 數字指標卡(Swift 的 MetricTile:delta 依 deltaGood 顯示綠/紅,可附 sparkline) */
export function Metric({ title, value, delta, deltaGood = true, icon, tint = "blue", series }: {
  title: string; value: ReactNode; delta?: string | null; deltaGood?: boolean; icon?: string; tint?: string; series?: number[];
}) {
  return (
    <div className={cardClass} style={{ gap: 6 }}>
      <div className="row gap6 muted" style={{ fontSize: "var(--fs-base)" }}>{icon && <SIcon name={icon} color={tint} />}<span className="ellipsis">{title}</span></div>
      <div className="row gap8 wrap" style={{ alignItems: "baseline", rowGap: 0 }}>
        <span className="num" style={{ fontSize: "var(--fs-3xl)", fontWeight: 600, whiteSpace: "nowrap" }}>{value}</span>
        {delta && <span className="caption" style={{ fontWeight: 500, color: deltaGood ? "var(--green)" : "var(--red)" }}>{delta}</span>}
      </div>
      {series && series.length > 0 && <Sparkline values={series} c={tint} height={28} />}
    </div>
  );
}
