// AI 自動化 › 派發頂端的「自動執行」精簡狀態列:claude 狀態、執行中的任務(可停止)、最近的錯誤,以及到「執行日誌」頁籤的連結。
// 歷史與日誌不在這裡,在 AutoLog.tsx;角色頁的設定卡在 AutoRunner.tsx。
import { useState } from "react";
import { action, refreshTopics, useRun, useView } from "../../api";
import { Card } from "../../components/Card";
import { ErrorText } from "../../components/ErrorText";
import { Icon } from "../../components/Icon";
import { Modal } from "../../components/Modal";
import { Pill } from "../../components/Pill";
import { relative } from "../../fmt";
import type { AgentRunnerView } from "../../models";
import css from "./automation.module.css";

export function RunnerStrip({ openTask, openLog }: { openTask: (taskId: string) => void; openLog: () => void }) {
  const [v, err] = useView<AgentRunnerView>("agent_runner", ["agents", "orgs", "system"]);
  const { busy, error, run } = useRun();
  const [confirmStop, setConfirmStop] = useState(false);
  if (!v) return err ? <Card title="自動執行"><ErrorText>{err}</ErrorText></Card> : null;
  const cur = v.current;
  const loginHint = v.claudeFound && !v.claudeLoggedIn && !v.claudeLoginChecking;
  return (
    <Card title="自動執行" subtitle="開了自動執行的角色被派發任務時,在這台 Mac 上以 Claude Code 自動處理,一次一筆"
      right={<div className="row gap6">
        {!v.claudeFound ? <Pill text="找不到 claude" color="orange" icon="exclamationmark.triangle" />
          : v.claudeLoggedIn ? <Pill text="claude 已就緒" color="green" icon="checkmark.circle" />
          : v.claudeLoginChecking ? <Pill text="檢查中…" color="gray" />
          : <Pill text="claude 尚未登入" color="orange" icon="exclamationmark.triangle" />}
        {v.disabled && <Pill text="系統設定已關閉" color="orange" icon="slash.circle" />}
        {!v.loop && <Pill text="背景排程未啟動" color="gray" />}
        <button className="btn small" onClick={openLog}>查看執行日誌</button>
      </div>}>
      {v.disabled && <span className={"caption " + css.warn}>{v.disabledReason ?? "「自動執行」在系統設定裡關閉了,不會領新的任務(進行中的會做完)"}</span>}
      {v.error && <ErrorText>{v.error}</ErrorText>}
      {loginHint && <span className={"caption " + css.warn}>{v.claudeLoginHint ?? "找到 claude,但還沒登入:在終端機執行 claude,再輸入 /login"}</span>}
      {!v.claudeFound && <span className="caption muted">請先安裝 Claude Code 並用訂閱帳號登入(終端機執行 claude 再輸入 /login),或設定環境變數 OMIXOR_CLAUDE_BIN。</span>}
      {v.lastError && <span className={"caption " + css.warn + " " + css.selectable}>{v.lastError}</span>}
      {cur ? (
        <div className={"col gap6 " + css.runningBox}>
          <div className="row gap8">
            <Icon name="arrow.triangle.2.circlepath" size={13} color="var(--blue)" />
            <button className="btn link mono" onClick={() => openTask(cur.taskId)}>{cur.taskId}</button>
            <span className="ellipsis grow">{cur.title}</span>
            <span className="caption muted">{cur.agentName} · {relative(cur.startedAt)}開始</span>
            <button className="btn small danger" disabled={busy} onClick={() => setConfirmStop(true)}>停止</button>
          </div>
          {cur.lastOutput && <span className={"caption muted " + css.output}>{cur.lastOutput}</span>}
        </div>
      ) : <span className="muted">目前沒有在執行的任務</span>}
      <ErrorText>{error}</ErrorText>
      <Modal open={confirmStop} onClose={() => setConfirmStop(false)} width={400} title={`停止 ${cur?.taskId ?? ""}?任務會回報「需要協助」(人工停止)。`}>
        <div className={"row gap8 " + css.end}>
          <button className="btn" onClick={() => setConfirmStop(false)}>取消</button>
          <button className="btn red" disabled={busy} onClick={() => run(async () => { await action("agent_runner_stop"); refreshTopics(["agent_runner"]); setConfirmStop(false); })}>停止</button>
        </div>
      </Modal>
    </Card>
  );
}
