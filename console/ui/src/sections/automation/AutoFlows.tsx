// 自動化流程:每個流程的自主程度、啟用狀態、權限與成效;詳細視窗有流程圖與安全護欄(Swift 的 AutoFlowsView)
import { Fragment, useState } from "react";
import { action, useRun } from "../../api";
import { AIBadge } from "../../components/Badge";
import { Card, cardClass } from "../../components/Card";
import { Chip } from "../../components/Chip";
import { EmptyState } from "../../components/EmptyState";
import { ErrorText } from "../../components/ErrorText";
import { Icon } from "../../components/Icon";
import { IconTile } from "../../components/IconTile";
import { Flow } from "../../components/Layout";
import { useContextMenu } from "../../components/Menu";
import { MetricTile } from "../../components/MetricTile";
import { Modal } from "../../components/Modal";
import { Pill } from "../../components/Pill";
import { SearchField } from "../../components/SearchField";
import { Segmented } from "../../components/Segmented";
import { Switch } from "../../components/Switch";
import { color } from "../../lib/color";
import { pct } from "../../lib/format";
import { AUTONOMIES, AUTONOMY_COLOR, AUTONOMY_DETAIL, AUTONOMY_ICON, AUTONOMY_LABEL, RUN_COLOR, RUN_ICON, type Automation, type Autonomy } from "../../models";
import { relative } from "../../fmt";
import css from "./automation.module.css";
import { RiskBadge, guardrails, pendingOf, useAuto } from "./autoShared";
import { rowButtonClass } from "../../components/RowButton";

/** 自主程度與啟用的動作(畫面專用) */
export function useAutomationActions() {
  const { error, run } = useRun();
  return {
    error,
    setAutonomy: (id: string, autonomy: Autonomy) => run(() => action("automation_autonomy", { id, autonomy })),
    toggle: (id: string) => run(() => action("automation_toggle", { id })),
  };
}

export function AutoFlows({ openDetail }: { openDetail: (id: string) => void }) {
  const d = useAuto();
  const [filter, setFilter] = useState<Autonomy | "all">("all");
  const [query, setQuery] = useState("");
  const acts = useAutomationActions();
  const q = query.toLowerCase();
  const items = d.automations.filter((a) => (filter === "all" || a.autonomy === filter) && (!q || [a.name, a.summary, a.trigger].some((s) => s.toLowerCase().includes(q))));
  return (
    <div className="page-scroll">
      <div className="page-inner">
        <div className="grid g3">
          {AUTONOMIES.map((a) => (
            <div key={a} className={cardClass + " " + css.modeCard}>
              <span className={css.modeIcon} style={{ background: `color-mix(in srgb, ${color(AUTONOMY_COLOR[a])} 13%, transparent)` }}>
                <Icon name={AUTONOMY_ICON[a]} size={14} color={color(AUTONOMY_COLOR[a])} />
              </span>
              <div className="col gap4">
                <span className="row gap6"><span className={css.strong}>{AUTONOMY_LABEL[a]}</span><span className="caption muted num">{d.automations.filter((x) => x.autonomy === a).length}</span></span>
                <span className="caption muted">{AUTONOMY_DETAIL[a]}</span>
              </div>
            </div>
          ))}
        </div>
        <div className="row gap8">
          <div className={css.w320}>
            <Segmented value={filter} onChange={setFilter} options={[{ value: "all", label: "全部" }, ...AUTONOMIES.map((a) => ({ value: a, label: AUTONOMY_LABEL[a] }))]} />
          </div>
          <div className="grow" />
          <SearchField value={query} onChange={setQuery} placeholder="搜尋流程" width={220} />
        </div>
        <ErrorText>{acts.error}</ErrorText>
        {!d.loaded ? <div className="muted">載入中…</div> : d.automations.length === 0 ? (
          <Card><EmptyState title="還沒有自動化流程" icon="sparkles" message="設定好的 AI 自動化流程會列在這裡。" /></Card>
        ) : items.length === 0 ? (
          <Card><EmptyState title="沒有符合條件的流程" icon="magnifyingglass" /></Card>
        ) : (
          <div className={"grid " + css.flowGrid}>
            {items.map((a) => <FlowCard key={a.id} a={a} openDetail={() => openDetail(a.id)} acts={acts} />)}
          </div>
        )}
      </div>
    </div>
  );
}

function FlowCard({ a, openDetail, acts }: { a: Automation; openDetail: () => void; acts: ReturnType<typeof useAutomationActions> }) {
  const d = useAuto();
  const [hover, setHover] = useState(false);
  const menu = useContextMenu("simple");
  const pending = pendingOf(d).find((p) => p.automation === a.id);
  const stat = (title: string, value: string, tone?: string) => (
    <div className="col grow"><span className={"muted " + css.xs}>{title}</span><span className={"num " + css.strong + (tone ? " " + tone : "")}>{value}</span></div>
  );
  return (
    <div className={cardClass + " " + css.flowCard + (a.enabled ? "" : " " + css.off) + (hover ? " " + css.hovered : "")} onDoubleClick={openDetail} onMouseEnter={() => setHover(true)} onMouseLeave={() => setHover(false)}
      onContextMenu={menu.open([
        { label: "查看詳細", onClick: openDetail }, "-",
        ...AUTONOMIES.map((m) => ({ label: (m === a.autonomy ? "✓ " : "") + AUTONOMY_LABEL[m], onClick: () => acts.setAutonomy(a.id, m) })), "-",
        { label: a.enabled ? "停用" : "啟用", onClick: () => acts.toggle(a.id) },
      ])}>
      <div className={"row gap12 " + css.top}>
        <IconTile icon={a.icon} size={38} dimmed={!a.enabled} />
        <div className="col gap4 grow">
          <span className={"h3 ellipsis " + css.cardName}>{a.name}</span>
          <span className={"muted " + css.clamp2}>{a.summary}</span>
        </div>
        <Switch small on={a.enabled} title={a.enabled ? "停用這個流程" : "啟用這個流程"} onChange={() => acts.toggle(a.id)} />
      </div>
      <div className="row gap6 caption">
        <Icon name="bolt.horizontal.circle" size={12} color="var(--indigo)" />
        <span className="ellipsis grow">{a.trigger}</span>
        {pending && <span title={pending.title}><Pill text="待核准" color="orange" icon="hand.raised.fill" /></span>}
        {a.lastRun && <span className={"muted " + css.none}>上次 {relative(a.lastRun)}</span>}
      </div>
      <div className={"col gap4" + (a.enabled ? "" : " " + css.locked)}>
        <Segmented value={a.autonomy} onChange={(v) => acts.setAutonomy(a.id, v)} options={AUTONOMIES.map((m) => ({ value: m, label: AUTONOMY_LABEL[m] }))} />
        <span className="caption" style={{ color: color(AUTONOMY_COLOR[a.autonomy]) }}>{AUTONOMY_DETAIL[a.autonomy]}</span>
      </div>
      {a.allowedTools.length > 0 && <Flow gap={5}>{a.allowedTools.map((t) => <Chip key={t} text={t} icon="wrench.adjustable" />)}</Flow>}
      {a.scope.length > 0 && (
        <div className={"row gap6 caption " + css.baseline}>
          <span className={"muted " + css.strong + " " + css.none}>範圍</span>
          <span className={"muted " + css.clamp2}>{a.scope.join("、")}</span>
        </div>
      )}
      <div className="divider" />
      <div className="row">
        {stat("30 天執行", String(a.runs30d))}
        {stat("成功率", pct(a.successRate * 100), a.successRate >= 0.95 ? css.good : a.successRate >= 0.8 ? css.warn : css.danger)}
        {stat("省下時間", `${a.timeSavedHours.toFixed(1)} 小時`)}
        <button className="btn small" onClick={openDetail}>詳細</button>
      </div>
      {menu.node}
    </div>
  );
}

// MARK: - 詳細:流程圖與安全護欄

export function FlowDetailSheet({ id, onClose, navigate }: { id?: string; onClose: () => void; navigate: (target: string) => void }) {
  const d = useAuto();
  const acts = useAutomationActions();
  const a = d.automations.find((x) => x.id === id);
  if (!id) return null;
  return (
    <Modal open onClose={onClose} width={760}>
      {!a ? <EmptyState title="找不到這個流程" icon="questionmark.circle" /> : (
        <>
          <div className={"row gap12 " + css.top}>
            <IconTile icon={a.icon} size={46} dimmed={!a.enabled} />
            <div className="col gap4 grow">
              <span className="row gap8"><span className="h3">{a.name}</span><AIBadge /></span>
              <span className="muted">{a.summary}</span>
              <span className={"row gap4 caption " + css.indigo}><Icon name="bolt.horizontal.circle" size={11} />{a.trigger}</span>
            </div>
            <label className="row gap6">{a.enabled ? "啟用中" : "已停用"}<Switch on={a.enabled} onChange={() => acts.toggle(a.id)} /></label>
          </div>
          <div className="row gap12">
            <div className={css.w360}><Segmented value={a.autonomy} onChange={(v) => acts.setAutonomy(a.id, v)} options={AUTONOMIES.map((m) => ({ value: m, label: AUTONOMY_LABEL[m] }))} /></div>
            <span className="caption" style={{ color: color(AUTONOMY_COLOR[a.autonomy]) }}>{AUTONOMY_DETAIL[a.autonomy]}</span>
          </div>
          <ErrorText>{acts.error}</ErrorText>
          <div className="grid g3">
            <MetricTile title="30 天執行" value={a.runs30d} icon="play.circle" tint="indigo" />
            <MetricTile title="成功率" value={pct(a.successRate * 100)} icon="checkmark.circle" tint="green" />
            <MetricTile title="省下時間" value={`${a.timeSavedHours.toFixed(1)} h`} icon="clock.arrow.circlepath" tint="purple" />
          </div>
          <div className="divider" />
          <Card title="流程" subtitle="人工核准關卡會依自主程度移動"><Diagram autonomy={a.autonomy} /></Card>
          <Guardrails a={a} />
          <Card title="權限" subtitle="AI 只能用這些工具、只能動這些對象">
            <span className={"caption muted " + css.strong}>可用工具</span>
            {a.allowedTools.length ? <Flow>{a.allowedTools.map((t) => <Chip key={t} text={t} icon="wrench.adjustable" />)}</Flow> : <span className="muted">未設定</span>}
            <span className={"caption muted " + css.strong}>可操作範圍</span>
            {a.scope.length ? <Flow>{a.scope.map((t) => <Chip key={t} text={t} icon="cube" mono={false} />)}</Flow> : <span className="muted">未設定</span>}
          </Card>
          <RecentRuns a={a} navigate={navigate} />
          <div className={"row " + css.end}><button className="btn primary" onClick={onClose}>完成</button></div>
        </>
      )}
    </Modal>
  );
}

function Guardrails({ a }: { a: Automation }) {
  const d = useAuto();
  const g = guardrails(a, d.runs);
  const ratio = g.monthlyBudgetUSD > 0 ? Math.min(1, g.spentUSD / g.monthlyBudgetUSD) : 0;
  const row = (label: string, icon: string, body: React.ReactNode) => (
    <Fragment key={label}>
      <span className="row gap6 muted"><Icon name={icon} size={12} />{label}</span>
      <div>{body}</div>
    </Fragment>
  );
  return (
    <Card title="安全護欄" subtitle="AI 不論自主程度,都不能超出這些限制">
      <div className={css.guardGrid}>
        {row("環境限制", "globe", <Flow gap={5}>{g.environments.map((e) => <Pill key={e} text={e} color="indigo" />)}</Flow>)}
        {row("影響範圍上限", "scope", g.blastRadius)}
        {row("頻率上限", "speedometer", `每小時最多 ${g.maxActionsPerHour} 次動作`)}
        {row("成本預算", "dollarsign.circle", (
          <div className="col gap4">
            <span className="num">本月 US${g.spentUSD.toFixed(2)} / US${g.monthlyBudgetUSD.toFixed(0)}</span>
            <div className={css.budgetTrack}>
              <div className={css.budgetFill + (ratio > 0.8 ? " " + css.over : "")} style={{ width: `${ratio * 100}%` }} />
            </div>
          </div>
        ))}
        {row("執行前演練", "checklist", g.requiresDryRun ? "必須先 dry-run / plan,結果附在請求裡" : "不需要")}
        {row("自動回滾", "arrow.uturn.backward", g.autoRollback ? "驗證失敗時自動回滾並通知值班" : "由人手動回滾")}
        {row("禁止動作", "nosign", <Flow gap={5}>{g.forbidden.map((f) => <Pill key={f} text={f} color="red" icon="xmark" />)}</Flow>)}
      </div>
    </Card>
  );
}

function RecentRuns({ a, navigate }: { a: Automation; navigate: (id: string) => void }) {
  const d = useAuto();
  const runs = d.runs.filter((r) => r.automation === a.id).sort((x, y) => y.startedAt.localeCompare(x.startedAt));
  const pending = pendingOf(d).filter((p) => p.automation === a.id);
  return (
    <Card title="最近執行" subtitle={runs.length ? undefined : "還沒有執行紀錄"}>
      <div className="col">
        {pending.map((p) => (
          <button key={p.id} className={rowButtonClass} onClick={() => navigate(p.id)}>
            <Icon name="hand.raised.fill" size={13} color="var(--orange)" /><span className="ellipsis grow">{p.title}</span><RiskBadge risk={p.risk} /><Icon name="chevron.right" size={10} className="faint" />
          </button>
        ))}
        {runs.slice(0, 6).map((r) => (
          <button key={r.id} className={rowButtonClass} onClick={() => navigate(r.id)}>
            <Icon name={RUN_ICON[r.status]} size={13} color={color(RUN_COLOR[r.status])} />
            <span className={"ellipsis " + css.runTarget}>{r.target}</span>
            <span className="ellipsis muted grow">{r.summary}</span>
            <span className="caption muted">{relative(r.startedAt)}</span>
            <Icon name="chevron.right" size={10} className="faint" />
          </button>
        ))}
      </div>
    </Card>
  );
}

/** 觸發 → AI 分析 → (核准關卡) → 執行 → 驗證 → 通知/回滾 */
function Diagram({ autonomy }: { autonomy: Autonomy }) {
  type Kind = "normal" | "ai" | "human" | "skipped" | "off";
  const gate = autonomy === "suggest" ? { title: "人工決定", caption: "AI 只給建議,由人自己動手", icon: "person.fill", kind: "human" as Kind }
    : autonomy === "approve" ? { title: "核准關卡", caption: "人核准、拒絕或修改後才繼續", icon: "hand.raised.fill", kind: "human" as Kind }
    : { title: "核准關卡", caption: "已略過,事後通知", icon: "hand.raised.slash", kind: "skipped" as Kind };
  const off: Kind = autonomy === "suggest" ? "off" : "normal";
  const nodes = [
    { title: "觸發", caption: "事件或排程", icon: "bolt.fill", kind: "normal" as Kind },
    { title: "AI 分析", caption: "讀取日誌、指標、程式碼", icon: "sparkles", kind: "ai" as Kind },
    gate,
    { title: "執行", caption: autonomy === "suggest" ? "AI 不執行" : "在護欄內執行", icon: "play.fill", kind: off },
    { title: "驗證", caption: "健康檢查與指標", icon: "checkmark.shield", kind: (off === "off" ? "off" : "ai") as Kind },
    { title: "通知/回滾", caption: autonomy === "auto" ? "失敗自動回滾" : "通知負責人", icon: "bell.badge", kind: off },
  ];
  const kindClass = (k: Kind) => (k === "normal" ? css.diaNormal : k === "ai" ? css.diaAi : k === "human" ? css.diaHuman : css.diaGray);
  const explanation = autonomy === "suggest" ? "目前是「只建議」:AI 分析完就停,所有變更都由人執行。"
    : autonomy === "approve" ? "目前是「需核准」:AI 準備好動作,送到待核准收件匣等人決定。"
    : "目前是「自動執行」:AI 直接執行,驗證失敗會自動回滾,並通知值班人員。";
  return (
    <div className="col gap8">
      <div className={"row " + css.top + " " + css.scrollX}>
        {nodes.map((n, i) => {
          const faded = n.kind === "skipped" || n.kind === "off";
          return (
            <Fragment key={n.title + i}>
              {i > 0 && <span className={css.diaArrow}><Icon name="chevron.right" size={11} className="faint" /></span>}
              <div className={"col " + css.diaNode}>
                <span className={css.diaIcon}>
                  {n.kind === "human" && <span className={css.diaRing} />}
                  <span className={css.diaCircle + " " + (faded ? css.diaFaded : kindClass(n.kind))}>
                    <Icon name={n.icon} size={15} color={faded ? "var(--fg2)" : "white"} />
                  </span>
                </span>
                <span className={"caption " + css.strong + (faded ? " muted" : n.kind === "human" ? " " + css.warn : "") + (n.kind === "skipped" ? " " + css.strike : "")}>{n.title}</span>
                <span className={"muted " + css.xs + " " + css.center}>{n.caption}</span>
              </div>
            </Fragment>
          );
        })}
      </div>
      <span className="row gap6 caption muted"><Icon name="info.circle" size={12} />{explanation}</span>
    </div>
  );
}
