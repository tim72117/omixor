// AI 自動化 › 派發:看哪一個角色被派了哪些任務(跨所有角色的總表),以及每個角色目前的負載
import { useState } from "react";
import { AgentAvatar, go, useDirectory } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { PRIORITY_COLOR, PRIORITY_LABEL, TASK_COLOR, TASK_ICON, TASK_LABEL, taskOpen, type AgentTask, type AgentTaskStatus } from "../../components/models";
import { Card, EmptyState, Pill, color } from "../../components/ui";
import { relative } from "../../fmt";
import { useAuto } from "./autoShared";

type Scope = "open" | "all";

export function AutoDispatch({ openTask }: { openTask: (task: AgentTask) => void }) {
  const d = useAuto();
  const dir = useDirectory();
  const [scope, setScope] = useState<Scope>("open");
  const [agentId, setAgentId] = useState("");
  if (!d.agents) {
    return d.agentsError ? <EmptyState title="讀不到虛擬角色" icon="exclamationmark.triangle" message={d.agentsError} /> : <div className="muted" style={{ padding: 20 }}>載入中…</div>;
  }
  const { agents, tasks } = d.agents;
  const byId = new Map(agents.map((a) => [a.id, a]));
  const count = (id: string, st: AgentTaskStatus) => tasks.filter((t) => t.agentId === id && t.status === st).length;
  const rows = tasks
    .filter((t) => (scope === "all" || taskOpen(t.status)) && (!agentId || t.agentId === agentId))
    .sort((a, b) => (taskOpen(a.status) ? 0 : 1) - (taskOpen(b.status) ? 0 : 1) || b.updatedAt.localeCompare(a.updatedAt));
  const issueOf = (t: AgentTask) => t.links.find((l) => /^ISS-\d+$/i.test(l));

  return (
    <div className="col gap12" style={{ padding: 16, overflowY: "auto", flex: 1, minHeight: 0 }}>
      <Card title="各角色目前的派發" subtitle="點角色可只看他被派的任務">
        {agents.length === 0 && <span className="muted">還沒有角色</span>}
        <div className="col">
          {agents.map((a) => {
            const open = tasks.filter((t) => t.agentId === a.id && taskOpen(t.status)).length;
            return (
              <button key={a.id} className="bx-row-btn" style={{ opacity: a.active ? 1 : 0.5, background: agentId === a.id ? "var(--hover)" : undefined }}
                onClick={() => setAgentId(agentId === a.id ? "" : a.id)}>
                <AgentAvatar agent={a} size={24} />
                <span className="grow ellipsis" style={{ textAlign: "left" }}>{a.name}<span className="caption muted"> · {a.role}</span></span>
                {!a.active && <Pill text="停用" color="gray" />}
                <Pill text={`待領取 ${count(a.id, "queued")}`} color="secondary" />
                <Pill text={`進行中 ${count(a.id, "inProgress")}`} color="blue" />
                {count(a.id, "blocked") > 0 && <Pill text={`需要協助 ${count(a.id, "blocked")}`} color="orange" />}
                <span className="caption muted num" style={{ width: 60, textAlign: "right" }}>{open} 件未完成</span>
              </button>
            );
          })}
        </div>
      </Card>
      <Card title="派發列表" subtitle={`${rows.length} 筆`}>
        <div className="row gap8">
          <select className="select" value={scope} onChange={(e) => setScope(e.target.value as Scope)}>
            <option value="open">只看未完成</option><option value="all">全部(含完成、取消)</option>
          </select>
          <select className="select" value={agentId} onChange={(e) => setAgentId(e.target.value)}>
            <option value="">全部角色</option>{agents.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
          </select>
        </div>
        {rows.length === 0 && <span className="muted">沒有符合的任務</span>}
        {rows.map((t) => {
          const a = byId.get(t.agentId);
          const iss = issueOf(t);
          return (
            <div key={t.id} className="row gap8" style={{ padding: "6px 0", borderBottom: "1px solid var(--line)", alignItems: "center" }}>
              <div className="row gap6" style={{ width: 150, alignItems: "center" }}>
                {a ? <AgentAvatar agent={a} size={22} /> : <Icon name="person.crop.circle" size={20} className="muted" />}
                <span className="ellipsis">{a?.name ?? t.agentId}</span>
              </div>
              <button className="bx-row-btn grow" style={{ minWidth: 0 }} onClick={() => openTask(t)}>
                <Icon name={TASK_ICON[t.status]} size={13} color={color(TASK_COLOR[t.status])} />
                <span className="mono caption muted">{t.id}</span>
                <span className="ellipsis grow" style={{ textAlign: "left", color: taskOpen(t.status) ? undefined : "var(--fg2)" }}>{t.title}</span>
              </button>
              {iss && <button className="btn link" onClick={() => go("development", iss)}>{iss}</button>}
              {(t.priority === "urgent" || t.priority === "high") && <Pill text={PRIORITY_LABEL[t.priority]} color={PRIORITY_COLOR[t.priority]} />}
              <Pill text={TASK_LABEL[t.status]} color={TASK_COLOR[t.status]} />
              <span className="caption muted ellipsis" style={{ width: 110 }} title="派發者">{dir.authorName(t.createdBy)}</span>
              <span className="caption muted" style={{ width: 70, textAlign: "right" }}>{relative(t.updatedAt)}</span>
            </div>
          );
        })}
      </Card>
    </div>
  );
}
