// 一筆執行的日誌:警示標籤與展開的時間線(執行日誌頁籤使用)。完整日誌用到才向核心讀(agent_runner_log),不預先抓。
import { useActionQuery } from "../../api";
import { ErrorText } from "../../components/ErrorText";
import { Pill } from "../../components/Pill";
import { dateTime } from "../../fmt";
import type { RunLog, RunLogEntry, RunnerRecord } from "../../models";
import css from "./automation.module.css";

/** 一眼看出這筆執行有問題:角色沒回報(由 omixor 補)、整個過程沒呼叫任何工具 */
export function RunWarnings({ r }: { r: RunnerRecord }) {
  return (
    <>
      {r.reportedBy === "runner" && <Pill text="由 omixor 補回報" color="orange" icon="exclamationmark.triangle" />}
      {r.runId && r.toolCalls === 0 && r.serverCalls === 0 && <Pill text="沒有呼叫任何工具" color="red" icon="exclamationmark.triangle" />}
    </>
  );
}

/** 展開的執行日誌:依序的時間線;文字摘錄預設收合 */
export function RunLogPanel({ runId }: { runId: string }) {
  const [res, err] = useActionQuery<{ log: RunLog | null }>("agent_runner_log", { run: runId });
  const log = res?.log;
  if (err) return <ErrorText>{err}</ErrorText>;
  if (!res) return <span className="caption muted">讀取中…</span>;
  if (!log) return <span className="caption muted">找不到這筆的日誌(可能已超過保留筆數)</span>;
  return (
    <div className={"col gap6 " + css.runLog}>
      <span className="caption muted">
        {log.model || "模型未知"} · MCP {log.mcpStatus || "未知"} · 工具呼叫 {log.toolCalls}、伺服器確認 {log.serverCalls}
        · 回報者 {log.reportedBy === "role" ? "角色自己" : log.reportedBy === "runner" ? "omixor 補回報" : "未知"}
        {log.truncated ? " · 項目過多,後面的已省略" : ""}
      </span>
      {log.entries.map((e, i) => <RunLogLine key={i} e={e} />)}
    </div>
  );
}

const LOG_KIND: Record<string, string> = { start: "開始", text: "文字", tool_call: "呼叫", tool_result: "結果", server_call: "伺服器", error: "錯誤", end: "結束" };

function RunLogLine({ e }: { e: RunLogEntry }) {
  const bad = e.status === "error" || e.kind === "error";
  if (e.kind === "text") {
    return <details className="caption"><summary className="muted">{dateTime(e.at)} 文字:{(e.text ?? "").slice(0, 40)}…</summary><span className={css.output}>{e.text}</span></details>;
  }
  return (
    <div className={"row gap8 caption " + (bad ? css.warn : "")}>
      <span className={"muted num " + css.w110}>{dateTime(e.at)}</span>
      <span className={css.w60}>{LOG_KIND[e.kind] ?? e.kind}</span>
      {e.tool && <span className="mono">{e.tool}</span>}
      {e.args && <span className="muted ellipsis grow" title={e.args}>{e.args}</span>}
      {e.status && <Pill text={e.status === "ok" ? "ok" : "error"} color={e.status === "ok" ? "green" : "red"} />}
      {e.kind !== "tool_call" && e.text && <span className={"ellipsis grow " + (bad ? "" : "muted")} title={e.text}>{e.text}</span>}
    </div>
  );
}
