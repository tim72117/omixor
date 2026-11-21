// 轉移角色的任務:刪角色時名下有任務(409)要先轉移,或在角色頁單獨轉移(不刪角色)
import { useState } from "react";
import { action, ApiError, useRun } from "../../api";
import { TASK_COLOR, TASK_LABEL, type Agent, type AgentTaskStatus } from "../../components/models";
import { ErrorText, Modal, Pill } from "../../components/ui";

const ORDER: AgentTaskStatus[] = ["queued", "inProgress", "blocked", "done", "canceled"];

/** 各狀態的任務數(依固定順序,0 的不列) */
export function TaskCounts({ counts }: { counts: Partial<Record<AgentTaskStatus, number>> }) {
  return (
    <span className="row gap6 wrap">
      {ORDER.filter((s) => (counts[s] ?? 0) > 0).map((s) => <Pill key={s} text={`${TASK_LABEL[s]} ${counts[s]}`} color={TASK_COLOR[s]} />)}
    </span>
  );
}

/**
 * 轉移任務的視窗。
 *  - mode "delete":刪角色時名下有任務(後端回 409);選目標後重送刪除(帶 transferTo),後端在同一個交易裡轉移再刪
 *  - mode "transfer":不刪角色的單獨轉移,可選範圍(只轉待領取與需要協助 / 全部)
 * 目標只列其他啟用中的角色。
 */
export function TransferTasksModal({ from, agents, counts: initialCounts, mode, onClose, onDone }: {
  from: Agent;
  agents: Agent[];
  counts: Partial<Record<AgentTaskStatus, number>>;
  mode: "delete" | "transfer";
  onClose: () => void;
  onDone: () => void;
}) {
  const targets = agents.filter((a) => a.id !== from.id && a.active);
  const [to, setTo] = useState("");
  const [scope, setScope] = useState<"open" | "all">("open");
  const { busy, error, run } = useRun();
  const [counts, setCounts] = useState(initialCounts); // 重送被擋(又有新任務)時,用後端回的最新筆數更新
  const total = ORDER.reduce((n, s) => n + (counts[s] ?? 0), 0);
  const open = (counts.queued ?? 0) + (counts.blocked ?? 0);
  const go = () => run(async () => {
    try {
      if (mode === "delete") await action("agent_delete", { id: from.id, transferTo: to });
      else await action("agent_task_transfer", { from: from.id, to, scope });
    } catch (e) {
      if (e instanceof ApiError && e.data?.taskCounts) setCounts(e.data.taskCounts as Partial<Record<AgentTaskStatus, number>>);
      throw e;
    }
    onDone();
  });
  return (
    <Modal open onClose={onClose} width={480} title={mode === "delete" ? `刪除「${from.name}」前要先轉移任務` : `轉移「${from.name}」的任務`}>
      <div className="col gap10">
        <span>
          {mode === "delete"
            ? `「${from.name}」名下還有 ${total} 筆任務,要先轉給其他角色才能刪除:`
            : `「${from.name}」名下共有 ${total} 筆任務:`}
        </span>
        <TaskCounts counts={counts} />
        <label className="bx-field">
          <span>轉移給</span>
          <select className="select" value={to} onChange={(e) => setTo(e.target.value)}>
            <option value="">選一個角色…</option>
            {targets.map((a) => <option key={a.id} value={a.id}>{a.name}{a.role ? `(${a.role})` : ""}</option>)}
          </select>
          {targets.length === 0 && <span className="caption muted">沒有其他啟用中的角色,請先新增或啟用一個角色</span>}
        </label>
        {mode === "transfer" && (
          <div className="bx-field">
            <span>範圍</span>
            <label className="row gap6"><input type="radio" checked={scope === "open"} onChange={() => setScope("open")} />只轉待領取與需要協助({open} 筆;進行中、已完成、已取消的留在原角色)</label>
            <label className="row gap6"><input type="radio" checked={scope === "all"} onChange={() => setScope("all")} />全部({total} 筆;進行中的先退回待領取並作廢憑證)</label>
          </div>
        )}
        <span className="caption muted">
          {mode === "delete"
            ? "進行中(已被領走)的任務會先退回待領取、憑證作廢;已完成、已取消的也一起轉;每筆會加一則「由 … 轉移」備註。指派給它的 Issue 會改指派給目標角色。任何一步失敗都會全部還原。"
            : "每筆被轉移的任務會加一則「由 … 轉移」備註。"}
        </span>
        <ErrorText>{error}</ErrorText>
        <div className="row gap8" style={{ justifyContent: "flex-end" }}>
          <button className="btn" onClick={onClose}>取消</button>
          <button className={"btn " + (mode === "delete" ? "red" : "primary")} disabled={!to || busy || (mode === "transfer" && (total === 0 || (scope === "open" && open === 0)))} onClick={go}>{mode === "delete" ? "轉移並刪除" : "轉移"}</button>
        </div>
      </div>
    </Modal>
  );
}
