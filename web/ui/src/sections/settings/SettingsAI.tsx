// 設定 › AI 治理:總開關、各環境自主上限、預算、允許的工具、核准規則與稽核紀錄(Swift 的 SettingsAIGovernancePane)
// 這些偏好設定在 Swift 版存在 UserDefaults;這裡存在瀏覽器的 localStorage(鍵名相同)。
import { useState } from "react";
import { action, tool, useRun, useView } from "../../api";
import { AIBadge, ActorAvatar, Switch, go, num, useDirectory, useStored } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { AUTONOMIES, AUTONOMY_COLOR, AUTONOMY_DETAIL, AUTONOMY_LABEL, arr, type ActivityItem, type Approval, type Automation, type AutomationRun, type Autonomy } from "../../components/models";
import { ErrorText, Pill, Segmented } from "../../components/ui";
import { SECTIONS } from "../../types";
import { Form, Row, Section } from "./form";
import { canGo } from "../../features";

const rank = (a: Autonomy) => AUTONOMIES.indexOf(a);
const ENVS = [
  { id: "production", label: "Production", short: "prod", color: "red", def: "approve" as Autonomy },
  { id: "staging", label: "Staging", short: "stg", color: "orange", def: "auto" as Autonomy },
  { id: "development", label: "Development", short: "dev", color: "teal", def: "auto" as Autonomy },
];
/** 以目前使用者身分寫一筆動態(Swift 的 app.log(.person(app.me), …)) */
const log = (icon: string, text: string, section = "automation") => tool("activity_post", { icon, text, section }).catch(() => undefined);

export function SettingsAI() {
  const [autosRaw] = useView<Automation[]>("automations");
  const [runsRaw] = useView<AutomationRun[]>("automation_runs");
  const [apvRaw] = useView<Approval[]>("approvals");
  const [actRaw] = useView<ActivityItem[]>("activity");
  const autos = arr<Automation>(autosRaw), runs = arr<AutomationRun>(runsRaw), approvals = arr<Approval>(apvRaw), activity = arr<ActivityItem>(actRaw);
  const dir = useDirectory();
  const [enabled, setEnabled] = useStored("ops.settings.ai.enabled", true);
  const [ceilProd, setCeilProd] = useStored<Autonomy>("ops.settings.ai.ceiling.production", "approve");
  const [ceilStg, setCeilStg] = useStored<Autonomy>("ops.settings.ai.ceiling.staging", "auto");
  const [ceilDev, setCeilDev] = useStored<Autonomy>("ops.settings.ai.ceiling.development", "auto");
  const [budget, setBudget] = useStored("ops.settings.ai.budgetUSD", 500);
  const [budgetAlert, setBudgetAlert] = useStored("ops.settings.ai.budgetAlert", true);
  const [over, setOver] = useStored("ops.settings.ai.overBudget", "pause");
  const [twoPerson, setTwoPerson] = useStored("ops.settings.ai.twoPersonHighRisk", true);
  const [reqRollback, setReqRollback] = useStored("ops.settings.ai.requireRollback", true);
  const [timeout, setTimeoutMin] = useStored("ops.settings.ai.approvalTimeout", 60);
  const [disabledTools, setDisabledTools] = useState<Set<string>>(new Set());
  const [blocked, setBlocked] = useState(["kubectl delete namespace", "terraform destroy", "DROP TABLE", "rm -rf /", "git push --force main"]);
  const [newBlocked, setNewBlocked] = useState("");
  const [audit, setAudit] = useState<"all" | "ai" | "human">("all");
  const { error, run } = useRun();

  const ceil: Record<string, [Autonomy, (v: Autonomy) => void]> = { production: [ceilProd, setCeilProd], staging: [ceilStg, setCeilStg], development: [ceilDev, setCeilDev] };
  const strictest = [ceilProd, ceilStg, ceilDev].sort((a, b) => rank(a) - rank(b))[0];
  const overCeiling = autos.filter((a) => a.enabled && rank(a.autonomy) > rank(ceilProd));
  const since = Date.now() - 30 * 86400000;
  const spend = runs.filter((r) => new Date(r.startedAt).getTime() >= since).reduce((s, r) => s + r.costUSD, 0);
  const ratio = budget > 0 ? spend / budget : 0;
  const counts = new Map<string, number>();
  autos.forEach((a) => a.allowedTools.forEach((t) => counts.set(t, (counts.get(t) ?? 0) + 1)));
  const tools = [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  const items = activity.filter((i) => {
    const ai = i.actor.kind === "ai";
    return audit === "ai" ? ai : audit === "human" ? !ai && i.section === "automation" : ai || i.section === "automation";
  });
  const addBlocked = () => {
    const v = newBlocked.trim();
    if (!v || blocked.includes(v)) return;
    setBlocked([...blocked, v]);
    setNewBlocked("");
    log("nosign", `新增 AI 禁止指令「${v}」`);
  };
  const exportCSV = () => {
    const esc = (s: string) => `"${s.replace(/"/g, '""')}"`;
    const lines = ["時間,執行者,類型,內容,區塊", ...items.map((i) => [i.date, dir.name(i.actor), i.actor.kind === "ai" ? "AI" : i.actor.kind === "person" ? "人員" : "系統", i.text, SECTIONS.find((s) => s.id === i.section)?.title ?? i.section].map(esc).join(","))];
    const blob = new Blob([lines.join("\n")], { type: "text/csv;charset=utf-8" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = `ops-ai-audit-${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(a.href);
  };
  const pending = approvals.filter((a) => a.state === "pending").length;
  const barColor = ratio > 1 ? "var(--red)" : ratio > 0.8 ? "var(--orange)" : "var(--green)";

  return (
    <Form>
      <Section header="總開關" icon="power">
        <Row label={<span style={{ fontWeight: 500 }}>允許 AI 自動化執行變更</span>}
          sub={<span style={{ color: enabled ? undefined : "var(--orange)" }}>{enabled ? "AI 會依下方上限自動執行或請求核准。" : "已暫停:所有 AI 自動化只產生建議,不會做任何變更。"}</span>}>
          <Switch on={enabled} onChange={(v) => { setEnabled(v); log(v ? "play.circle" : "pause.circle", v ? "恢復 AI 自動化執行" : "暫停所有 AI 自動化執行(緊急停止)"); }} />
        </Row>
        <Row label="目前啟用的自動化"><span className="num">{autos.filter((a) => a.enabled).length} / {autos.length}</span></Row>
      </Section>
      <Section header="各環境的 AI 自主上限" icon="slider.horizontal.3" footer={`個別自動化的設定不能超過所在環境的上限。目前最嚴格的上限是「${AUTONOMY_LABEL[strictest]}」。`}>
        {ENVS.map((e) => (
          <Row key={e.id} label={<span className="row gap8"><Pill text={e.short} color={e.color} /><span className="col"><span>{e.label}</span><span className="caption muted">{AUTONOMY_DETAIL[ceil[e.id][0]]}</span></span></span>}>
            <select className="select" disabled={!enabled} value={ceil[e.id][0]} onChange={(ev) => ceil[e.id][1](ev.target.value as Autonomy)}>
              {AUTONOMIES.map((a) => <option key={a} value={a}>{AUTONOMY_LABEL[a]}</option>)}
            </select>
          </Row>
        ))}
        {overCeiling.length > 0 && (
          <Row>
            <div className="col gap8 grow">
              <span className="row gap6" style={{ color: "var(--orange)", fontWeight: 500 }}><Icon name="exclamationmark.triangle.fill" size={13} />{overCeiling.length} 個自動化的自主程度高於 Production 上限「{AUTONOMY_LABEL[ceilProd]}」</span>
              {overCeiling.map((a) => (
                <div key={a.id} className="row gap8">
                  <Icon name={a.icon} size={14} color="var(--purple)" /><span>{a.name}</span><Pill text={AUTONOMY_LABEL[a.autonomy]} color={AUTONOMY_COLOR[a.autonomy]} />
                  <div className="grow" />
                  <button className="btn small" onClick={() => run(() => action("automation_autonomy", { id: a.id, autonomy: ceilProd }))}>降為「{AUTONOMY_LABEL[ceilProd]}」</button>
                </div>
              ))}
              <ErrorText>{error}</ErrorText>
            </div>
          </Row>
        )}
      </Section>
      <Section header="預算" icon="dollarsign.circle">
        <Row label="每月預算上限">
          <input className="input num" type="number" min={0} step={50} style={{ width: 110, textAlign: "right" }} value={budget} onChange={(e) => setBudget(Math.max(0, +e.target.value || 0))} />
          <span className="muted">USD</span>
        </Row>
        <Row>
          <div className="col gap6 grow">
            <div className="row"><span className="grow">最近 30 天花費</span><span className="num" style={{ color: ratio > 1 ? "var(--red)" : ratio > 0.8 ? "var(--orange)" : "var(--fg2)" }}>${spend.toFixed(2)} / ${budget.toFixed(0)}</span></div>
            <div className="row gap8">
              <div className="grow" style={{ height: 6, borderRadius: 6, background: "color-mix(in srgb, var(--fg) 8%, transparent)" }}>
                <div style={{ width: `${Math.min(100, ratio * 100)}%`, height: 6, borderRadius: 6, background: barColor }} />
              </div>
              <span className="caption muted num" style={{ width: 36, textAlign: "right" }}>{Math.floor(ratio * 100)}%</span>
            </div>
            <span className="caption muted">{runs.length} 次執行 · {num(runs.reduce((s, r) => s + r.tokens, 0))} tokens</span>
          </div>
        </Row>
        <Row label="用到 80% 時通知管理員"><Switch on={budgetAlert} onChange={setBudgetAlert} /></Row>
        <Row label="超過上限時">
          <select className="select" value={over} onChange={(e) => setOver(e.target.value)}>
            <option value="pause">暫停非必要的自動化</option><option value="suggest">全部降為只建議</option><option value="notify">只通知,不限制</option>
          </select>
        </Row>
      </Section>
      <Section header="允許的工具" icon="wrench.and.screwdriver" footer="關閉的工具會從所有自動化的可用工具中移除,正在使用它的步驟會改為請求核准。">
        {tools.length === 0 && <Row><span className="muted">目前沒有自動化宣告要使用的工具</span></Row>}
        {tools.map(([name, users]) => (
          <Row key={name} label={<span className="row gap8"><span className="mono">{name}</span><span className="caption muted">{users} 個自動化使用</span></span>}>
            <Switch on={!disabledTools.has(name)} onChange={(on) => {
              setDisabledTools((s) => { const n = new Set(s); if (on) n.delete(name); else n.add(name); return n; });
              log(on ? "checkmark.shield" : "nosign", `${on ? "允許" : "禁止"} AI 使用工具 ${name}`);
            }} />
          </Row>
        ))}
      </Section>
      <Section header="禁止的指令" icon="hand.raised" footer="不論自主程度,AI 都不會執行符合這些關鍵字的指令。">
        {blocked.map((c) => (
          <Row key={c}>
            <Icon name="nosign" size={13} color="var(--red)" /><span className="mono grow">{c}</span>
            <button className="bx-icon-btn" title="移除" onClick={() => setBlocked(blocked.filter((x) => x !== c))}><Icon name="minus.circle" size={13} /></button>
          </Row>
        ))}
        <Row>
          <input className="input mono grow" placeholder="新增禁止的指令或關鍵字" value={newBlocked} onChange={(e) => setNewBlocked(e.target.value)} onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && addBlocked()} />
          <button className="btn" disabled={!newBlocked.trim()} onClick={addBlocked}>新增</button>
        </Row>
      </Section>
      <Section header="核准規則" icon="checkmark.seal">
        <Row label="高風險動作需要兩人核准"><Switch on={twoPerson} onChange={setTwoPerson} /></Row>
        <Row label="AI 提出的變更必須附回滾計畫"><Switch on={reqRollback} onChange={setReqRollback} /></Row>
        <Row label="核准請求逾時">
          <select className="select" value={timeout} onChange={(e) => setTimeoutMin(+e.target.value)}>
            <option value={15}>15 分鐘</option><option value={30}>30 分鐘</option><option value={60}>1 小時</option><option value={240}>4 小時</option>
          </select>
        </Row>
        <Row label="等待核准">{canGo("automation") && <button className="btn link" disabled={!pending} onClick={() => go("automation")}>{pending} 項,前往處理</button>}</Row>
      </Section>
      <Section header="稽核紀錄" icon="list.bullet.clipboard">
        <Row>
          <div style={{ width: 280 }}><Segmented value={audit} onChange={setAudit} options={[{ value: "all", label: "全部" }, { value: "ai", label: "AI 執行" }, { value: "human", label: "人員變更" }]} /></div>
          <div className="grow" />
          <button className="btn" disabled={!items.length} onClick={exportCSV}><Icon name="square.and.arrow.up" size={12} />匯出 CSV</button>
        </Row>
        {items.length === 0 && <Row><span className="muted">沒有符合的稽核紀錄</span></Row>}
        {items.slice(0, 30).map((i) => (
          <Row key={i.id} style={{ alignItems: "flex-start" }}>
            <span className="caption num muted" style={{ width: 84, flex: "none" }}>{(() => { const d = new Date(i.date); return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`; })()}</span>
            <ActorAvatar actor={i.actor} size={18} />
            <div className="col grow">
              <span className="row gap4 caption"><span style={{ fontWeight: 500 }}>{dir.name(i.actor)}</span>{i.actor.kind === "ai" && <AIBadge />}</span>
              <span className="caption muted">{i.text}</span>
            </div>
          </Row>
        ))}
        {items.length > 30 && <Row><span className="caption muted">只顯示最近 30 筆,完整紀錄請匯出 CSV。</span></Row>}
      </Section>
    </Form>
  );
}

