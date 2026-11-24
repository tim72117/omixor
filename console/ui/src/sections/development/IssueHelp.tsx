// Issue 上的「需要協助」:這張 Issue 的任務被角色回報卡住時,看板卡與列表列顯示橘色小標,Issue 詳情列出任務、能直接回覆。
// 判斷與分組在 automation/needsHelpModel.ts(和 AI 自動化的「需要處理」頁籤同一個),任務卡也是同一個元件。
import { useEffect, useRef } from "react";
import { Pill } from "../../components/Pill";
import { go, useRouteParam } from "../../lib/nav";
import { NeedsHelpList } from "../automation/NeedsHelp";
import { useDev } from "./devShared";

/** 網址 ?section=help:打開 Issue 詳情時捲到「需要協助」 */
export const HELP_SECTION = "help";

/** 橘色「需要協助 N」小標,按下去打開那張 Issue 的詳情並停在需要協助;沒有需要協助的任務就不顯示 */
export function IssueHelpPill({ issueId }: { issueId: string }) {
  const dev = useDev();
  const n = dev.help.byIssue.get(issueId)?.length ?? 0;
  if (n === 0) return null;
  return (
    <button className="btn link" title="有任務需要協助,按一下查看並回覆" onClick={(e) => { e.stopPropagation(); dev.showIssueHelp(issueId); }}>
      <Pill text={`需要協助 ${n}`} color="orange" icon="hand.raised" />
    </button>
  );
}

/** Issue 詳情的「需要協助」區塊:列出這張 Issue 的需要處理任務 */
export function IssueHelp({ issueId }: { issueId: string }) {
  const dev = useDev();
  const [section] = useRouteParam("section");
  const ref = useRef<HTMLDivElement>(null);
  const tasks = dev.help.byIssue.get(issueId) ?? [];
  const show = section === HELP_SECTION && tasks.length > 0;
  useEffect(() => { if (show) ref.current?.scrollIntoView({ block: "start" }); }, [show]);
  if (tasks.length === 0) return null;
  return (
    <div ref={ref} className="col gap8">
      <span className="h3">需要協助</span>
      <NeedsHelpList tasks={tasks} agents={dev.help.agents} openTask={(t) => go("automation", t.id)} showIssue={false} />
    </div>
  );
}
