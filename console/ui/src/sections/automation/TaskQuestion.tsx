// 角色卡住時留下的問題:摺起時只顯示開頭兩行,可展開全文;派發列表的列與「需要處理」區塊共用
import { useState } from "react";
import { Markdown } from "../../components/Markdown";
import { useDirectory } from "../../lib/directory";
import type { AgentTask } from "../../models";
import css from "./automation.module.css";
import { blockQuestion, excerpt } from "./needsHelpModel";

export function TaskQuestion({ task, agentName, expanded: initial = false }: { task: AgentTask; agentName: string; expanded?: boolean }) {
  const dir = useDirectory();
  const [open, setOpen] = useState(initial);
  const q = blockQuestion(task);
  if (!q.text) return <span className="caption muted">角色沒有留下說明</span>;
  const { short, truncated } = excerpt(q.text);
  return (
    <div className={"col gap4 " + css.questionBox}>
      <span className="caption muted">{q.author ? dir.authorName(q.author) : agentName}</span>
      <Markdown text={truncated && !open ? short + "…" : q.text} />
      {truncated && <button className={"btn link " + css.start} onClick={() => setOpen(!open)}>{open ? "收起" : "展開全部"}</button>}
    </div>
  );
}
