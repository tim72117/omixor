// 開發 › 看板:依狀態分欄顯示目前組織真的 Issue。分欄、排序、篩選、搜尋的規則只在核心(omixor-core/model 的 issue_query.go):
// 畫面呼叫 issue_board(後端用核心的 model.BuildIssueBoard 處理快取的 Issue),只負責顯示;改狀態走 issue_update。
//   待處理 = open 且沒指派角色;進行中 = open 且指派給角色;已完成 = closed(每欄依優先度 → 最近更新 → 編號)
// 拖曳:欄的 dropStatus 和 Issue 現在的 status 不同才送修改(拖到「已完成」= 關閉,從「已完成」拖出 = 重新打開);
// 待處理 ↔ 進行中 狀態相同(要改指派,會牽動角色的任務),看板上不做,請到 Issue 分頁改。
// 拖曳游標:能放的欄 dropEffect = move 並高亮;不能放的欄(原欄、待處理 ↔ 進行中)dropEffect = none(禁止游標、瀏覽器不觸發 drop),
// 待處理 ↔ 進行中 另外在欄內顯示一行提示。dragover 時讀不到 dataTransfer 的資料,所以 dragstart 時把 Issue 記在 dragging。
import { useState } from "react";
import { tool, useActionQuery, useDebounced } from "../../api";
import { color, EmptyState, Pill } from "../../components/ui";
import { boardDropStatus } from "../../components/models";
import { copyText, SIcon, useAct, useContextMenu, useList, useOmixor } from "../../components/omixorKit";
import { RepoFilterSelect, usedRepos, useDev } from "./devShared";
import { PRIORITY, showPriorityPill, type Issue } from "./DevIssuesTab";

type Priority = Issue["priority"];
const PRIORITIES: Priority[] = ["urgent", "high", "normal", "low"];

export type BoardColumnKey = "todo" | "doing" | "done";
/** issue_board 回傳的一欄(同核心的 model.BoardColumn) */
export type BoardColumn = { key: BoardColumnKey; title: string; dropStatus: "open" | "closed" | null; issues: Issue[] };
/** issue_board 的回應(同核心 GET /api/orgs/{org}/issues/board 的 model.IssueBoard) */
export type IssueBoard = { columns: BoardColumn[]; labels: string[]; total: number; matched: number };

// 各欄的顯示(圖示、顏色、說明);欄的順序、標題、內容都照後端回傳
const COLUMN_LOOK: Record<string, { icon: string; color: string; help: string }> = {
  todo: { icon: "circle.dotted", color: "secondary", help: "未解決、還沒指派給角色的 Issue" },
  doing: { icon: "circle.lefthalf.filled", color: "blue", help: "未解決、已指派給角色的 Issue" },
  done: { icon: "checkmark.circle.fill", color: "green", help: "已關閉的 Issue;拖到這裡會關閉,拖回去會重新打開" },
};
const look = (k: string) => COLUMN_LOOK[k] ?? { icon: "circle", color: "secondary", help: "" };

// MARK: - 畫面

export function DevBoardTab() {
  const [query, setQuery] = useState("");
  const [label, setLabel] = useState("");
  const [priority, setPriority] = useState<Priority | "">("");
  const [repo, setRepo] = useState(""); // "" = 全部倉庫;NO_REPO = 未標記;其他 = 只列這個倉庫(比對規則在核心的 model.RepoMatch)
  const [issues] = useList<Issue>("issues"); // 全部(倉庫選單用,同 Issue 分頁);看板本身照 issue_board 的結果
  const q = useDebounced(query, 250);
  const [board, err] = useActionQuery<IssueBoard>("issue_board", { q, label, priority, repo }, ["issues"]);
  const [targeted, setTargeted] = useState<string>();
  /** 拖曳中的 Issue(dragstart 記下、dragend 清掉)與它原本的欄 */
  const [dragging, setDragging] = useState<{ issue: Issue; from: string }>();
  /** 拖到不能放、要提示的欄(待處理 ↔ 進行中) */
  const [blocked, setBlocked] = useState<string>();
  const { run, toastEl } = useAct();

  const labels = board?.labels ?? [];
  const total = board?.total ?? 0;
  const hasFilter = !!query || !!label || !!priority || !!repo;
  const setStatus = (i: Issue, s: "open" | "closed") => {
    if (i.status !== s) run(() => tool("issue_update", { id: i.id, status: s }));
  };
  // 只有能放的欄會觸發 drop(不能放的 dragover 設了 dropEffect = none);仍照 dropStatus 再判斷一次
  const drop = (id: string, col: BoardColumn) => {
    const i = board?.columns.flatMap((c) => c.issues).find((x) => x.id === id);
    const s = i && boardDropStatus(col.dropStatus, i.status);
    if (i && s) setStatus(i, s);
  };
  const endDrag = () => { setDragging(undefined); setTargeted(undefined); setBlocked(undefined); };

  return (
    <div className="ox-page">
      <div className="ox-filterbar">
        <input className="input" style={{ minWidth: 140, maxWidth: 240, flex: 1 }} placeholder="搜尋編號、標題、內文、標籤、留言" value={query} onChange={(e) => setQuery(e.target.value)} />
        <select className="select" value={label} onChange={(e) => setLabel(e.target.value)} title="依標籤篩選">
          <option value="">全部標籤</option>
          {labels.length > 0 && <option disabled>──────</option>}
          {labels.map((l) => <option key={l} value={l}>{l}</option>)}
          {/* 篩選中的標籤已經沒有 Issue 用了:仍列出來,選單才不會是空白 */}
          {label && !labels.some((l) => l.toLowerCase() === label.toLowerCase()) && <option value={label}>{label}</option>}
        </select>
        <select className="select" value={priority} onChange={(e) => setPriority(e.target.value as Priority | "")} title="依優先度篩選">
          <option value="">全部優先度</option><option disabled>──────</option>
          {PRIORITIES.map((p) => <option key={p} value={p}>{PRIORITY[p].label}</option>)}
        </select>
        <RepoFilterSelect value={repo} onChange={setRepo} repos={usedRepos(issues)} />
        {hasFilter && <button className="btn link" onClick={() => { setQuery(""); setLabel(""); setPriority(""); setRepo(""); }}>清除</button>}
        <span className="grow" />
        {board && <span className="muted num">{hasFilter ? `${board.matched} / ${total} 個 Issue` : `${total} 個 Issue`}</span>}
      </div>
      {err && !board ? <EmptyState title="讀不到 Issue" icon="exclamationmark.triangle" message={err} />
        : !board ? null : total === 0 ? (
          <EmptyState title="這個組織還沒有 Issue" icon="rectangle.split.3x1" message="到「Issue」分頁新增,或讓 AI 透過 MCP 的 issue_create 建立;建立後會依狀態出現在看板上。" />
        ) : (
          <div className="ox-board">
            {board.columns.map((col) => {
              const items = col.issues;
              const isTarget = targeted === col.key;
              const isBlocked = blocked === col.key;
              const lk = look(col.key);
              return (
                <div key={col.key} className={"ox-col" + (isTarget ? " target" : "")}
                  onDragOver={(e) => {
                    const ok = !!dragging && !!boardDropStatus(col.dropStatus, dragging.issue.status);
                    if (ok) {
                      e.preventDefault();
                      e.dataTransfer.dropEffect = "move";
                    } else {
                      e.dataTransfer.dropEffect = "none";
                    }
                    const t = ok ? col.key : undefined;
                    if (targeted !== t) setTargeted(t);
                    const b = !ok && !!dragging && dragging.from !== col.key ? col.key : undefined;
                    if (blocked !== b) setBlocked(b);
                  }}
                  onDragLeave={(e) => {
                    if (e.currentTarget.contains(e.relatedTarget as Node)) return;
                    setTargeted((t) => (t === col.key ? undefined : t));
                    setBlocked((b) => (b === col.key ? undefined : b));
                  }}
                  onDrop={(e) => {
                    e.preventDefault();
                    const id = dragging?.issue.id ?? e.dataTransfer.getData("text/plain");
                    endDrag();
                    drop(id, col);
                  }}>
                  <div className="row gap6" style={{ padding: "9px 10px" }} title={lk.help}>
                    <SIcon name={lk.icon} color={lk.color} />
                    <span className="card-title">{col.title}</span>
                    <span className="ox-count">{items.length}</span>
                  </div>
                  {isBlocked && <div className="ox-col-hint">待處理 ↔ 進行中 由指派角色決定,請到 Issue 分頁改指派</div>}
                  <div className="ox-col-body">
                    {items.map((i) => (
                      <IssueCard key={i.id} issue={i} onStatus={(s) => setStatus(i, s)}
                        dragging={dragging?.issue.id === i.id}
                        onDragStart={() => setDragging({ issue: i, from: col.key })} onDragEnd={endDrag} />
                    ))}
                    {items.length === 0 && <div className="ox-dashed">{isTarget ? `放開以移到「${col.title}」` : hasFilter ? "沒有符合的 Issue" : "沒有 Issue"}</div>}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      {toastEl}
    </div>
  );
}

const tagStyle: React.CSSProperties = {
  fontSize: 10.5, padding: "1px 5px", borderRadius: 4, maxWidth: "100%",
  overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
};

function IssueCard({ issue, onStatus, dragging, onDragStart, onDragEnd }: {
  issue: Issue; onStatus: (s: "open" | "closed") => void;
  /** 這張卡正在被拖曳(半透明) */
  dragging: boolean;
  onDragStart: () => void; onDragEnd: () => void;
}) {
  const o = useOmixor();
  const dev = useDev();
  const menu = useContextMenu();
  const open = issue.status === "open";
  const p = PRIORITY[issue.priority] ?? PRIORITY.normal;
  const agentName = issue.assignee ? (o.agents.find((a) => a.id === issue.assignee)?.name ?? issue.assignee) : undefined;
  const labels = issue.labels ?? [];
  return (
    <>
      <div className={"ox-work" + (dragging ? " dragging" : "")} draggable title="按一下在 Issue 分頁打開;拖到其他欄改狀態"
        onDragStart={(e) => { e.dataTransfer.setData("text/plain", issue.id); e.dataTransfer.effectAllowed = "move"; onDragStart(); }}
        onDragEnd={onDragEnd}
        onClick={() => dev.showIssue(issue.id)}
        onContextMenu={menu.open([
          { label: "打開詳細資料", onClick: () => dev.showIssue(issue.id) },
          "sep",
          open ? { label: "關閉 Issue", onClick: () => onStatus("closed") } : { label: "重新打開", onClick: () => onStatus("open") },
          "sep",
          { label: "複製編號", onClick: () => copyText(issue.id) },
        ])}>
        <div style={{ width: 3, flex: "none", background: color(p.color) }} title={`優先度:${p.label}`} />
        <div className="col grow" style={{ padding: 10, gap: 6, minWidth: 0 }}>
          <div className="row gap6">
            <span className="caption mono muted">{issue.id}</span>
            <span className="grow" />
            {issue.comments?.length > 0 && <span className="caption muted row gap4"><SIcon name="bubble.left" size={11} />{issue.comments.length}</span>}
            {showPriorityPill(issue.priority) && <Pill text={p.label} color={p.color} />}
          </div>
          <div style={{ color: open ? undefined : "var(--fg2)", overflowWrap: "anywhere", whiteSpace: "normal" }}>{issue.title}</div>
          {(agentName || issue.repo) && (
            <div className="col gap4" style={{ alignItems: "flex-start", minWidth: 0 }}>
              {agentName && (
                <span className="row gap4" title={`指派給角色:${agentName}`} style={{ ...tagStyle, color: color("purple"), background: `color-mix(in srgb, ${color("purple")} 12%, transparent)` }}>
                  <SIcon name="person.crop.circle" size={10} />{agentName}
                </span>
              )}
              {issue.repo && (
                <span className="row gap4 mono" title={`倉庫:${issue.repo}`} style={{ ...tagStyle, color: color("teal"), background: `color-mix(in srgb, ${color("teal")} 12%, transparent)` }}>
                  <SIcon name="chevron.left.forwardslash.chevron.right" size={10} />{issue.repo}
                </span>
              )}
            </div>
          )}
          {labels.length > 0 && (
            <div className="row gap4" style={{ flexWrap: "wrap" }} title={labels.join("、")}>
              {labels.slice(0, 3).map((l) => (
                <span key={l} className="row gap4 muted" style={{ ...tagStyle, maxWidth: 120, background: "color-mix(in srgb, var(--fg) 6%, transparent)" }}>
                  <SIcon name="tag" size={10} />{l}
                </span>
              ))}
              {labels.length > 3 && <span className="caption muted">+{labels.length - 3}</span>}
            </div>
          )}
        </div>
      </div>
      {menu.el}
    </>
  );
}
