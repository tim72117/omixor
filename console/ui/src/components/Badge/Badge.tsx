// 以 Pill 為底的徽章:健康狀態、執行狀態、環境(原本在 omixorKit.tsx);AIBadge 在 AIBadge.tsx(bits 與 omixorKit 原本各一份,已合併)
import { ENV, HEALTH, RUN, type EnvKey, type HealthKey, type RunStatusKey } from "../../models";
import { Pill } from "../Pill";
import { SIcon } from "../SIcon";

/** 健康狀態;compact 只顯示圖示(滑鼠移上去看文字) */
export function HealthBadge({ h, compact }: { h: HealthKey; compact?: boolean }) {
  return compact
    ? <span title={HEALTH[h]?.label}><SIcon name={HEALTH[h]?.icon} color={HEALTH[h]?.color} /></span>
    : <Pill text={HEALTH[h]?.label ?? h} color={HEALTH[h]?.color} icon={HEALTH[h]?.icon} />;
}

export function RunStatusBadge({ s }: { s: RunStatusKey }) {
  return <Pill text={RUN[s]?.label ?? s} color={RUN[s]?.color} icon={RUN[s]?.icon} />;
}

export function EnvBadge({ env }: { env: EnvKey }) {
  return <Pill text={ENV[env]?.short ?? env} color={ENV[env]?.color} />;
}
