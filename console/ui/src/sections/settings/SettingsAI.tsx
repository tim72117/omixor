// 設定 › AI 治理:等待核准的入口與稽核紀錄(Swift 的 SettingsAIGovernancePane)
// 總開關、自主上限、預算、工具與指令限制、核准規則還沒有後端實作,不放只存在畫面上的開關(做了才加回來)。
import { useState } from "react";
import { useView } from "../../api";
import { ActorAvatar } from "../../components/Avatar";
import { AIBadge } from "../../components/Badge";
import { Icon } from "../../components/Icon";
import { Segmented } from "../../components/Segmented";
import { useDirectory } from "../../lib/directory";
import { go } from "../../lib/nav";
import { arr, type ActivityItem, type Approval } from "../../models";
import { SECTIONS } from "../../types";
import { Form, Row, Section } from "./form";
import css from "./settings.module.css";
import { canGo } from "../../features";

export function SettingsAI() {
  const [apvRaw] = useView<Approval[]>("approvals");
  const [actRaw] = useView<ActivityItem[]>("activity");
  const approvals = arr<Approval>(apvRaw), activity = arr<ActivityItem>(actRaw);
  const dir = useDirectory();
  const [audit, setAudit] = useState<"all" | "ai" | "human">("all");

  const items = activity.filter((i) => {
    const ai = i.actor.kind === "ai";
    return audit === "ai" ? ai : audit === "human" ? !ai && i.section === "automation" : ai || i.section === "automation";
  });
  const exportCSV = () => {
    const esc = (s: string) => `"${s.replace(/"/g, '""')}"`;
    const lines = ["時間,執行者,類型,內容,區塊", ...items.map((i) => [i.date, dir.name(i.actor), i.actor.kind === "ai" ? "AI" : i.actor.kind === "person" ? "人員" : "系統", i.text, SECTIONS.find((s) => s.id === i.section)?.title ?? i.section].map(esc).join(","))];
    const blob = new Blob([lines.join("\n")], { type: "text/csv;charset=utf-8" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = `omixor-ai-audit-${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(a.href);
  };
  const pending = approvals.filter((a) => a.state === "pending").length;

  return (
    <Form>
      {canGo("automation") && (
        <Section header="核准" icon="checkmark.seal">
          <Row label="等待核准"><button className="btn link" disabled={!pending} onClick={() => go("automation")}>{pending} 項,前往處理</button></Row>
        </Section>
      )}
      <Section header="稽核紀錄" icon="list.bullet.clipboard">
        <Row>
          <div className={css.w280}><Segmented value={audit} onChange={setAudit} options={[{ value: "all", label: "全部" }, { value: "ai", label: "AI 執行" }, { value: "human", label: "人員變更" }]} /></div>
          <div className="grow" />
          <button className="btn" disabled={!items.length} onClick={exportCSV}><Icon name="square.and.arrow.up" size={12} />匯出 CSV</button>
        </Row>
        {items.length === 0 && <Row><span className="muted">沒有符合的稽核紀錄</span></Row>}
        {items.slice(0, 30).map((i) => (
          <Row key={i.id} alignTop>
            <span className={"caption num muted " + css.w84 + " " + css.none}>{(() => { const d = new Date(i.date); return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`; })()}</span>
            <ActorAvatar actor={i.actor} size={18} />
            <div className="col grow">
              <span className="row gap4 caption"><span className={css.medium}>{dir.name(i.actor)}</span>{i.actor.kind === "ai" && <AIBadge />}</span>
              <span className="caption muted">{i.text}</span>
            </div>
          </Row>
        ))}
        {items.length > 30 && <Row><span className="caption muted">只顯示最近 30 筆,完整紀錄請匯出 CSV。</span></Row>}
      </Section>
    </Form>
  );
}
