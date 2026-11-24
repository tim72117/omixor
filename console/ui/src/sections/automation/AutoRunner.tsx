// 角色自動執行的設定:派發給開了「自動執行」的角色時,omixor 在這台 Mac 上啟動 Claude Code(訂閱額度)以角色身分處理並回報。
// 設定是這台機器的(存在組織資料夾的 agent-runner.json,不進核心);App 版照同一套。
import { useState } from "react";
import { action, refreshTopics, useRun, useView } from "../../api";
import { Card } from "../../components/Card";
import { ErrorText } from "../../components/ErrorText";
import { Icon } from "../../components/Icon";
import { Modal, ModalTitle } from "../../components/Modal";
import { Pill } from "../../components/Pill";
import type { Agent, AgentRunConfig, AgentRunnerView } from "../../models";
import css from "./automation.module.css";
import { fieldClass } from "../../components/Field";

const EFFORTS = ["low", "medium", "high", "xhigh", "max"];
const EFFORT_LABEL: Record<string, string> = { low: "低", medium: "中", high: "高", xhigh: "很高", max: "最高" };
const MODELS = ["", "opus", "sonnet", "haiku"];
const blankConfig = (): AgentRunConfig => ({ autoRun: false, model: "", effort: "", workdir: "", allowBash: false, timeoutMinutes: 30 });

/** 設定的一行摘要 */
export function runConfigSummary(c: AgentRunConfig): string {
  const parts = [c.model || "預設模型"];
  if (c.effort) parts.push(`推論${EFFORT_LABEL[c.effort] ?? c.effort}`);
  parts.push(c.workdir ? `資料夾 ${c.workdir}${c.allowBash ? "(含 Bash)" : ""}` : "只用 omixor 工具");
  parts.push(`逾時 ${c.timeoutMinutes || 30} 分`);
  return parts.join(" · ");
}

/** 角色頁的「自動執行」卡片:目前設定與「設定…」 */
export function AgentAutoRunCard({ agent }: { agent: Agent }) {
  const [v] = useView<AgentRunnerView>("agent_runner", ["agents", "orgs", "system"]);
  const [editing, setEditing] = useState(false);
  const row = v?.agents?.find((a) => a.id === agent.id);
  const cfg = row?.config ?? blankConfig();
  const running = v?.current && v.current.agentId === agent.id ? v.current : null;
  return (
    <Card title="自動執行" subtitle="派發給這個角色時,在這台 Mac 上自動啟動 Claude Code 處理並回報"
      right={<button className="btn small" disabled={!v} onClick={() => setEditing(true)}>設定…</button>}>
      <div className="row gap8">
        <Icon name={cfg.autoRun ? "play.circle" : "xmark.circle"} size={15} color={cfg.autoRun ? "var(--green)" : "var(--fg2)"} />
        <span>{cfg.autoRun ? "已開啟" : "手動(到「AI 自動化 › 派發」按「開始執行」,或在 Claude Code 手動領取)"}</span>
        {running && <Pill text={`處理中 ${running.taskId}`} color="blue" icon="arrow.triangle.2.circlepath" />}
      </div>
      {cfg.autoRun && <span className="caption muted">{runConfigSummary(cfg)}</span>}
      {v?.disabled && cfg.autoRun && <span className={"caption " + css.warn}>{v.disabledReason ?? "「自動執行」在系統設定裡關閉了,不會領新的任務"}</span>}
      {v && !v.claudeFound && cfg.autoRun && <span className={"caption " + css.warn}>找不到 claude(Claude Code CLI),任務會留在待領取</span>}
      {v && v.claudeFound && !v.claudeLoggedIn && !v.claudeLoginChecking && cfg.autoRun && <span className={"caption " + css.warn}>{v.claudeLoginHint ?? "claude 尚未登入"},任務會留在待領取</span>}
      {editing && v && <AutoRunSheet agent={agent} original={cfg} efforts={v.efforts?.length ? v.efforts : EFFORTS} onClose={() => setEditing(false)} />}
    </Card>
  );
}

function AutoRunSheet({ agent, original, efforts, onClose }: { agent: Agent; original: AgentRunConfig; efforts: string[]; onClose: () => void }) {
  const [draft, setDraft] = useState<AgentRunConfig>({ ...original });
  const { busy, error, run } = useRun();
  const set = (p: Partial<AgentRunConfig>) => setDraft((d) => ({ ...d, ...p }));
  const customModel = !MODELS.includes(draft.model);
  const save = () => run(async () => {
    await action("agent_runner_save", { agent: agent.id, ...draft, workdir: draft.workdir.trim(), model: draft.model.trim() });
    refreshTopics(["agent_runner"]); // 存好就重讀,卡片馬上顯示新設定
    onClose();
  });
  return (
    <Modal open onClose={onClose} width={560}>
      <ModalTitle>「{agent.name}」的自動執行</ModalTitle>
      <label className="row gap6"><input type="checkbox" checked={draft.autoRun} onChange={(e) => set({ autoRun: e.target.checked })} />開啟自動執行(派發後自動領取並以這個角色身分處理)</label>
      <div className="grid g2">
        <label className={fieldClass}><span>模型</span>
          <select className="select" value={customModel ? "custom" : draft.model} onChange={(e) => set({ model: e.target.value === "custom" ? "claude-" : e.target.value })}>
            {MODELS.map((m) => <option key={m} value={m}>{m || "CLI 預設"}</option>)}
            <option value="custom">完整 id…</option>
          </select>
          {customModel && <input className="input" value={draft.model} placeholder="例如 claude-opus-5-5" onChange={(e) => set({ model: e.target.value })} />}
        </label>
        <label className={fieldClass}><span>推論強度</span>
          <select className="select" value={draft.effort} onChange={(e) => set({ effort: e.target.value })}>
            <option value="">CLI 預設</option>
            {efforts.map((x) => <option key={x} value={x}>{EFFORT_LABEL[x] ?? x}({x})</option>)}
          </select>
        </label>
      </div>
      <label className={fieldClass}><span>工作資料夾(這台 Mac 上的絕對路徑)</span>
        <input className="input mono" value={draft.workdir} placeholder="空白 = 不給檔案工具,只能用 omixor 工具" onChange={(e) => set({ workdir: e.target.value })} />
        <span className="caption muted">有設定時才開 Read、Glob、Grep、Edit、Write,在這個資料夾執行。資料夾要存在,不能包含 omixor 的資料或憑證資料夾。</span>
      </label>
      <label className={"row gap6" + (draft.workdir.trim() ? "" : " " + css.inactive)}>
        <input type="checkbox" disabled={!draft.workdir.trim()} checked={draft.allowBash && !!draft.workdir.trim()} onChange={(e) => set({ allowBash: e.target.checked })} />
        允許執行指令(Bash)
      </label>
      <label className="row gap6">逾時
        <input className={"input " + css.w80} type="number" min={1} max={480} value={draft.timeoutMinutes}
          onChange={(e) => set({ timeoutMinutes: Math.max(1, Math.min(480, +e.target.value || 30)) })} />分鐘(超過就停止並回報需要協助)
      </label>
      <ErrorText>{error}</ErrorText>
      <div className="row gap8">
        <div className="grow" />
        <button className="btn" onClick={onClose}>取消</button>
        <button className="btn primary" disabled={busy} onClick={save}>儲存</button>
      </div>
    </Modal>
  );
}
