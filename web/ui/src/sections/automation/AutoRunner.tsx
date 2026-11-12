// AI 自動化 › 角色自動執行:派發給開了「自動執行」的角色時,omixor 在這台 Mac 上啟動 Claude Code(訂閱額度)以角色身分處理並回報。
// 設定是這台機器的(存在組織資料夾的 agent-runner.json,不進核心);App 版照同一套。
import { useState } from "react";
import { action, useRun, useView } from "../../api";
import { Icon } from "../../components/Icon";
import type { Agent, AgentRunConfig, AgentRunnerView, RunnerRecord } from "../../components/models";
import { Card, ErrorText, Modal, Pill } from "../../components/ui";
import { dateTime, duration, relative } from "../../fmt";

const EFFORTS = ["low", "medium", "high", "xhigh", "max"];
const EFFORT_LABEL: Record<string, string> = { low: "低", medium: "中", high: "高", xhigh: "很高", max: "最高" };
const MODELS = ["", "opus", "sonnet", "haiku"];
const OUTCOME: Record<string, { label: string; color: string; icon: string }> = {
  done: { label: "完成", color: "green", icon: "checkmark.circle" },
  blocked: { label: "需要協助", color: "orange", icon: "hand.raised" },
  error: { label: "錯誤", color: "red", icon: "exclamationmark.triangle" },
};
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
  const [v] = useView<AgentRunnerView>("agent_runner", ["agents", "orgs"]);
  const [editing, setEditing] = useState(false);
  const row = v?.agents?.find((a) => a.id === agent.id);
  const cfg = row?.config ?? blankConfig();
  const running = v?.current && v.current.agentId === agent.id ? v.current : null;
  return (
    <Card title="自動執行" subtitle="派發給這個角色時,在這台 Mac 上自動啟動 Claude Code 處理並回報"
      right={<button className="btn small" disabled={!v} onClick={() => setEditing(true)}>設定…</button>}>
      <div className="row gap8" style={{ alignItems: "center" }}>
        <Icon name={cfg.autoRun ? "play.circle" : "xmark.circle"} size={15} color={cfg.autoRun ? "var(--green)" : "var(--fg2)"} />
        <span>{cfg.autoRun ? "已開啟" : "關閉(任務要在 Claude Code 手動領取)"}</span>
        {running && <Pill text={`處理中 ${running.taskId}`} color="blue" icon="arrow.triangle.2.circlepath" />}
      </div>
      {cfg.autoRun && <span className="caption muted">{runConfigSummary(cfg)}</span>}
      {v && !v.claudeFound && cfg.autoRun && <span className="caption" style={{ color: "var(--orange)" }}>找不到 claude(Claude Code CLI),任務會留在待領取</span>}
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
    onClose();
  });
  return (
    <Modal open onClose={onClose} width={560}>
      <span className="modal-title">「{agent.name}」的自動執行</span>
      <label className="row gap6"><input type="checkbox" checked={draft.autoRun} onChange={(e) => set({ autoRun: e.target.checked })} />開啟自動執行(派發後自動領取並以這個角色身分處理)</label>
      <div className="grid g2">
        <label className="bx-field"><span>模型</span>
          <select className="select" value={customModel ? "custom" : draft.model} onChange={(e) => set({ model: e.target.value === "custom" ? "claude-" : e.target.value })}>
            {MODELS.map((m) => <option key={m} value={m}>{m || "CLI 預設"}</option>)}
            <option value="custom">完整 id…</option>
          </select>
          {customModel && <input className="input" value={draft.model} placeholder="例如 claude-opus-5-5" onChange={(e) => set({ model: e.target.value })} />}
        </label>
        <label className="bx-field"><span>推論強度</span>
          <select className="select" value={draft.effort} onChange={(e) => set({ effort: e.target.value })}>
            <option value="">CLI 預設</option>
            {efforts.map((x) => <option key={x} value={x}>{EFFORT_LABEL[x] ?? x}({x})</option>)}
          </select>
        </label>
      </div>
      <label className="bx-field"><span>工作資料夾(這台 Mac 上的絕對路徑)</span>
        <input className="input mono" value={draft.workdir} placeholder="空白 = 不給檔案工具,只能用 omixor 工具" onChange={(e) => set({ workdir: e.target.value })} />
        <span className="caption muted">有設定時才開 Read、Glob、Grep、Edit、Write,在這個資料夾執行。資料夾要存在,不能包含 omixor 的資料或憑證資料夾。</span>
      </label>
      <label className="row gap6" style={{ opacity: draft.workdir.trim() ? 1 : 0.5 }}>
        <input type="checkbox" disabled={!draft.workdir.trim()} checked={draft.allowBash && !!draft.workdir.trim()} onChange={(e) => set({ allowBash: e.target.checked })} />
        允許執行指令(Bash)
      </label>
      <label className="row gap6">逾時
        <input className="input" type="number" min={1} max={480} style={{ width: 80 }} value={draft.timeoutMinutes}
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

/** 自動化區的「自動執行」狀態:找不到 claude、在跑的任務(停止)、最近的執行紀錄 */
export function AgentRunnerCard({ openTask }: { openTask?: (taskId: string) => void }) {
  const [v, err] = useView<AgentRunnerView>("agent_runner", ["agents", "orgs"]);
  const { busy, error, run } = useRun();
  const [confirmStop, setConfirmStop] = useState(false);
  if (!v) return err ? <Card title="自動執行"><ErrorText>{err}</ErrorText></Card> : null;
  const on = v.agents.filter((a) => a.config.autoRun);
  const cur = v.current;
  return (
    <Card title="自動執行" subtitle="開了自動執行的角色被派發任務時,omixor 在這台 Mac 上以 Claude Code 自動處理,一次一筆"
      right={<div className="row gap6">
        {v.claudeFound ? <Pill text="claude 已就緒" color="green" icon="checkmark.circle" /> : <Pill text="找不到 claude" color="orange" icon="exclamationmark.triangle" />}
        {!v.loop && <Pill text="背景排程未啟動" color="gray" />}
      </div>}>
      {v.error && <ErrorText>{v.error}</ErrorText>}
      {!v.claudeFound && <span className="caption muted">請先安裝 Claude Code 並用訂閱帳號登入(終端機執行 claude 再輸入 /login),或設定環境變數 OMIXOR_CLAUDE_BIN。</span>}
      <span className="caption muted">{on.length ? `開啟的角色:${on.map((a) => a.name).join("、")}` : "還沒有角色開啟自動執行(在「虛擬角色」的角色頁設定)"}</span>
      {v.lastError && <span className="caption" style={{ color: "var(--orange)", userSelect: "text" }}>{v.lastError}</span>}
      {cur ? (
        <div className="col gap6" style={{ padding: 10, borderRadius: 8, background: "color-mix(in srgb, var(--blue) 8%, transparent)" }}>
          <div className="row gap8" style={{ alignItems: "center" }}>
            <Icon name="arrow.triangle.2.circlepath" size={13} color="var(--blue)" />
            <button className="btn link mono" onClick={() => openTask?.(cur.taskId)}>{cur.taskId}</button>
            <span className="ellipsis grow">{cur.title}</span>
            <span className="caption muted">{cur.agentName} · {relative(cur.startedAt)}開始</span>
            <button className="btn small danger" disabled={busy} onClick={() => setConfirmStop(true)}>停止</button>
          </div>
          {cur.lastOutput && <span className="caption muted" style={{ whiteSpace: "pre-wrap", userSelect: "text", maxHeight: 120, overflowY: "auto" }}>{cur.lastOutput}</span>}
        </div>
      ) : <span className="muted">目前沒有在執行的任務</span>}
      <ErrorText>{error}</ErrorText>
      {v.history.length > 0 && (
        <div className="col">
          <span className="caption muted" style={{ fontWeight: 600 }}>最近 {v.history.length} 筆</span>
          {v.history.map((r, i) => <RecordRow key={`${r.taskId}-${r.startedAt}-${i}`} r={r} openTask={openTask} />)}
        </div>
      )}
      <Modal open={confirmStop} onClose={() => setConfirmStop(false)} width={400} title={`停止 ${cur?.taskId ?? ""}?任務會回報「需要協助」(人工停止)。`}>
        <div className="row gap8" style={{ justifyContent: "flex-end" }}>
          <button className="btn" onClick={() => setConfirmStop(false)}>取消</button>
          <button className="btn red" disabled={busy} onClick={() => run(async () => { await action("agent_runner_stop"); setConfirmStop(false); })}>停止</button>
        </div>
      </Modal>
    </Card>
  );
}

function RecordRow({ r, openTask }: { r: RunnerRecord; openTask?: (taskId: string) => void }) {
  const o = OUTCOME[r.outcome] ?? { label: r.outcome, color: "gray", icon: "questionmark.circle" };
  return (
    <div className="row gap8" style={{ padding: "5px 0", borderBottom: "1px solid var(--line)", alignItems: "center" }}>
      <Icon name={o.icon} size={13} color={`var(--${o.color})`} />
      <button className="btn link mono" onClick={() => openTask?.(r.taskId)}>{r.taskId}</button>
      <span className="ellipsis grow" title={r.error || r.title}>{r.title}{r.error ? <span className="caption muted"> · {r.error}</span> : null}</span>
      <span className="caption muted ellipsis" style={{ width: 90 }}>{r.agentName}</span>
      <Pill text={o.label} color={o.color} />
      <span className="caption muted num" style={{ width: 80, textAlign: "right" }} title="耗時">{duration(r.durationSeconds)}</span>
      <span className="caption muted num" style={{ width: 60, textAlign: "right" }} title="Claude 估計花費(換算成 API 價格;用訂閱不另外收費)">${(r.costUsd ?? 0).toFixed(2)}</span>
      <span className="caption muted" style={{ width: 90, textAlign: "right" }}>{dateTime(r.finishedAt)}</span>
    </div>
  );
}
