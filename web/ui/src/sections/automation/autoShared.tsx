// AI 自動化畫面內共用的小元件與輔助函式(Swift 的 AutoShared)
import { createContext, useContext } from "react";
import { useNow } from "../../components/bits";
import { Icon } from "../../components/Icon";
import {
  AUTONOMY_COLOR, AUTONOMY_ICON, AUTONOMY_LABEL, RISK_COLOR, RISK_LABEL,
  type AgentsView, type Approval, type Automation, type AutomationRun, type Autonomy, type Risk,
} from "../../components/models";
import { Pill } from "../../components/ui";

export type AutoData = {
  automations: Automation[]; runs: AutomationRun[]; approvals: Approval[]; agents?: AgentsView; agentsError?: string; loaded: boolean;
};
export const AutoCtx = createContext<AutoData>({ automations: [], runs: [], approvals: [], loaded: false });
export const useAuto = () => useContext(AutoCtx);
export const pendingOf = (d: AutoData) => d.approvals.filter((a) => a.state === "pending");
export const autoName = (d: AutoData, id: string) => d.automations.find((a) => a.id === id)?.name ?? id;

/** 穩定的雜湊(FNV-1a,和 Swift 的 AutoSeed 一樣) */
export function seedValue(s: string): number {
  let h = 1469598103934665603n;
  const bytes = new TextEncoder().encode(s);
  for (const b of bytes) h = ((h ^ BigInt(b)) * 1099511628211n) & 0xffffffffffffffffn;
  return Number(h % 1000000n);
}
export const seedUnit = (s: string) => (seedValue(s) % 10000) / 10000;

export const RiskBadge = ({ risk }: { risk: Risk }) => (
  <Pill text={RISK_LABEL[risk]} color={RISK_COLOR[risk]} icon={risk === "high" ? "exclamationmark.triangle.fill" : "shield.lefthalf.filled"} />
);
export const AutonomyBadge = ({ autonomy }: { autonomy: Autonomy }) => (
  <Pill text={AUTONOMY_LABEL[autonomy]} color={AUTONOMY_COLOR[autonomy]} icon={AUTONOMY_ICON[autonomy]} />
);

export function StepIcon({ status }: { status: string }) {
  if (status === "running") return <span className="bx-spinner" style={{ width: 14, height: 14 }} />;
  const m: Record<string, [string, string]> = {
    done: ["checkmark.circle.fill", "var(--green)"], pending: ["circle.dashed", "var(--fg2)"], failed: ["xmark.circle.fill", "var(--red)"], skipped: ["arrow.uturn.right.circle", "var(--fg2)"],
  };
  const [icon, c] = m[status] ?? m.pending;
  return <Icon name={icon} size={16} color={c} />;
}

export function fmtCountdown(sec: number): string {
  const s = Math.floor(sec);
  if (s >= 3600) return `${Math.floor(s / 3600)} 小時 ${Math.floor((s % 3600) / 60)} 分`;
  if (s >= 60) return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  return `${s} 秒`;
}

/** 倒數(到期時間) */
export function Countdown({ expiresAt }: { expiresAt: string }) {
  const now = useNow(1000);
  const left = (new Date(expiresAt).getTime() - now) / 1000;
  const urgent = left < 15 * 60;
  return (
    <span className="row gap4 caption" title={`到期時間 ${new Date(expiresAt).toLocaleString("zh-TW")}`}
      style={{ fontWeight: 500, color: left <= 0 ? "var(--fg2)" : urgent ? "var(--red)" : "var(--orange)" }}>
      <Icon name="hourglass" size={11} />
      <span className="num">{left <= 0 ? "已逾時" : `剩 ${fmtCountdown(left)}`}</span>
    </span>
  );
}

/** 安全護欄:模型沒有這些欄位,依自動化的範圍與工具推算出固定值 */
export function guardrails(a: Automation, runs: AutomationRun[]) {
  const text = [...a.scope, ...a.allowedTools, a.name, a.summary].join(" ").toLowerCase();
  const envs: string[] = [];
  if (text.includes("prod") || text.includes("正式")) envs.push("正式環境");
  if (text.includes("staging") || text.includes("預備")) envs.push("預備環境");
  if (!envs.length) envs.push("預備環境", "開發環境");
  const seed = seedValue(a.id);
  const mine = runs.filter((r) => r.automation === a.id);
  const avg = mine.length ? mine.reduce((s, r) => s + r.costUSD, 0) / mine.length : 0.12 + (seed % 30) / 100;
  const spent = avg * Math.max(a.runs30d, mine.length);
  const budget = Math.max(20, Math.ceil((spent * 1.8) / 10) * 10);
  const radius = a.autonomy === "suggest" ? "不做變更" : a.autonomy === "approve" ? `每次最多 ${2 + (seed % 4)} 個資源` : `每次最多 1 個服務、${10 + (seed % 4) * 5}% 流量`;
  const destructive = text.includes("terraform") || text.includes("kubectl") || text.includes("deploy");
  return {
    environments: envs, blastRadius: radius, monthlyBudgetUSD: budget, spentUSD: spent, maxActionsPerHour: 3 + (seed % 8),
    requiresDryRun: destructive, autoRollback: a.autonomy === "auto" || destructive, forbidden: ["刪除資料庫", "變更 IAM 權限", "關閉告警規則"],
  };
}
