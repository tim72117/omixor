// 待核准收件匣:AI 準備好的動作,等人決定(Swift 的 AutoInboxView)
import { useEffect, useState } from "react";
import { action, useRun } from "../../api";
import { AutoTextarea } from "../../components/AutoTextarea";
import { Card, cardClass } from "../../components/Card";
import { CodeBlock } from "../../components/CodeBlock";
import { EmptyState } from "../../components/EmptyState";
import { ErrorText } from "../../components/ErrorText";
import { Icon } from "../../components/Icon";
import { IconTile } from "../../components/IconTile";
import { Modal } from "../../components/Modal";
import { Pill } from "../../components/Pill";
import { color } from "../../lib/color";
import { useDirectory } from "../../lib/directory";
import { RISK_COLOR, RISK_LABEL, type Approval, type Risk } from "../../models";
import { relative } from "../../fmt";
import css from "./automation.module.css";
import { Countdown, RiskBadge, autoName, pendingOf, useAuto } from "./autoShared";

const rank = (r: Risk) => (r === "high" ? 2 : r === "medium" ? 1 : 0);

export function AutoInbox({ focusId, clearFocus, edited, setEdited, openAutomation, openRun }: {
  focusId?: string; clearFocus: () => void; edited: Record<string, string>; setEdited: (f: (m: Record<string, string>) => Record<string, string>) => void;
  openAutomation: (id: string) => void; openRun: (id: string) => void;
}) {
  const d = useAuto();
  const [risk, setRisk] = useState<Risk | "">("");
  const [highlighted, setHighlighted] = useState<string>();
  const all = pendingOf(d);
  const pending = all.filter((a) => !risk || a.risk === risk).sort((a, b) => rank(b.risk) - rank(a.risk) || a.expiresAt.localeCompare(b.expiresAt));
  const decided = d.approvals.filter((a) => a.state !== "pending").sort((a, b) => b.createdAt.localeCompare(a.createdAt));

  useEffect(() => {
    if (!focusId) return;
    setHighlighted(focusId);
    window.setTimeout(() => document.getElementById("apv-" + focusId)?.scrollIntoView({ behavior: "smooth", block: "start" }), 150);
    const t = window.setTimeout(() => setHighlighted((h) => (h === focusId ? undefined : h)), 2500);
    clearFocus();
    return () => window.clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusId]);

  return (
    <div className="page-scroll">
      <div className="page-inner">
        <div className="row gap8">
          <Icon name="hand.raised.fill" size={15} color="var(--purple)" />
          <span className="h3">{all.length === 0 ? "目前沒有需要你決定的事" : `${all.length} 件請求等你決定`}</span>
          <div className="grow" />
          <select className={"select " + css.minW120} value={risk} onChange={(e) => setRisk(e.target.value as Risk | "")}>
            <option value="">全部風險</option>
            {(["high", "medium", "low"] as Risk[]).map((r) => <option key={r} value={r}>{RISK_LABEL[r]}</option>)}
          </select>
        </div>
        {!d.loaded ? <div className="muted">載入中…</div> : all.length === 0 ? (
          <Card><EmptyState title="沒有待核准的請求" icon="checkmark.seal" message="AI 需要人把關的動作會出現在這裡。" /></Card>
        ) : pending.length === 0 ? (
          <Card><EmptyState title="沒有符合篩選的請求" icon="line.3.horizontal.decrease.circle" /></Card>
        ) : pending.map((r) => (
          <ApprovalCard key={r.id} req={r} highlighted={highlighted === r.id} openAutomation={openAutomation} openRun={openRun}
            onEdited={(cmd) => setEdited((m) => ({ ...m, [r.id]: cmd }))} />
        ))}
        {decided.length > 0 && (
          <>
            <span className={"h3 " + css.mt8}>最近已處理</span>
            <div className={cardClass + " " + css.flush}>
              {decided.slice(0, 12).map((r, i) => (
                <div key={r.id} id={"apv-" + r.id} className={(i ? css.sepTop : "") + (highlighted === r.id ? " " + css.flash : "")}>
                  <DecidedRow req={r} edited={edited[r.id]} />
                </div>
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function ApprovalCard({ req, highlighted, openAutomation, openRun, onEdited }: { req: Approval; highlighted: boolean; openAutomation: (id: string) => void; openRun: (id: string) => void; onEdited: (cmd: string) => void }) {
  const d = useAuto();
  const auto = d.automations.find((a) => a.id === req.automation);
  const relatedRun = d.runs.find((r) => r.automation === req.automation && r.status === "waitingApproval");
  const [confirmApprove, setConfirmApprove] = useState(false);
  const [confirmReject, setConfirmReject] = useState(false);
  const [editing, setEditing] = useState(false);
  const { busy, error, run } = useRun();
  const decide = (approve: boolean, editedCmd?: string) => run(async () => {
    await action("approval_decide", { id: req.id, approve, ...(editedCmd != null ? { edited: editedCmd } : {}) });
    if (editedCmd != null && editedCmd !== req.action) onEdited(editedCmd);
    setConfirmApprove(false); setConfirmReject(false); setEditing(false);
  });
  return (
    <div id={"apv-" + req.id} className={cardClass + " " + css.approval + (highlighted ? " " + css.focused : "")}>
      <div className={css.riskStripe} style={{ background: color(RISK_COLOR[req.risk]) }} />
      <div className={"row gap12 " + css.top}>
        <IconTile icon={auto?.icon ?? "sparkles"} size={36} />
        <div className="col gap4 grow">
          <span className={"h3 " + css.cardName}>{req.title}</span>
          <span className="row gap6 caption">
            <button className={"btn link caption " + css.noPad} onClick={() => openAutomation(req.automation)}>{auto?.name ?? req.automation}</button>
            <span className="faint">·</span><span className="muted">提出於 {relative(req.createdAt)}</span>
          </span>
        </div>
        <div className={"col gap6 " + css.alignEnd}><RiskBadge risk={req.risk} /><Countdown expiresAt={req.expiresAt} /></div>
      </div>
      <div className={"row gap8 " + css.top + " " + css.aiBox}>
        <Icon name="sparkles" size={13} color="var(--purple)" />
        <div className="col gap4"><span className={css.strong + " " + css.purple}>AI 的判斷</span><span className={css.selectable + " " + css.lh15}>{req.rationale}</span></div>
      </div>
      {req.evidence.length > 0 && (
        <div className="col gap4">
          <span className={css.strong}>依據</span>
          {req.evidence.map((e, i) => <span key={i} className={"row gap6 " + css.baseline}><Icon name="checkmark.circle" size={11} className="muted" /><span className={css.selectable}>{e}</span></span>)}
        </div>
      )}
      <div className="col gap6">
        <span className={"row gap6 " + css.strong}><Icon name="terminal" size={13} />將執行的動作</span>
        <CodeBlock text={req.action} maxHeight={220} />
      </div>
      <div className={"row gap8 " + css.top}>
        <Icon name="arrow.uturn.backward.circle" size={14} color="var(--blue)" />
        <div className="col gap4"><span className={css.strong}>回滾計畫</span><span className={"muted " + css.selectable}>{req.rollbackPlan}</span></div>
      </div>
      <div className="divider" />
      <div className="row gap8">
        {relatedRun && <button className="btn link" onClick={() => openRun(relatedRun.id)}><Icon name="list.bullet.rectangle" size={12} />查看執行過程</button>}
        <div className="grow" />
        <ErrorText>{error}</ErrorText>
        <button className="btn" disabled={busy} onClick={() => setConfirmReject(true)}>拒絕</button>
        <button className="btn" disabled={busy} onClick={() => setEditing(true)}>修改後核准…</button>
        <button className={"btn " + (req.risk === "high" ? "red" : "purple")} disabled={busy} onClick={() => (req.risk === "high" ? setConfirmApprove(true) : decide(true))}>
          <Icon name="checkmark.seal.fill" size={12} />核准並執行
        </button>
      </div>
      <Modal open={confirmApprove} onClose={() => setConfirmApprove(false)} width={420} title="確定要核准高風險動作?">
        <span className="muted">「{req.title}」會立即執行。請確認回滾計畫可行:{req.rollbackPlan}</span>
        <div className={"row gap8 " + css.end}>
          <button className="btn" onClick={() => setConfirmApprove(false)}>取消</button>
          <button className="btn red" disabled={busy} onClick={() => decide(true)}>核准並執行</button>
        </div>
      </Modal>
      <Modal open={confirmReject} onClose={() => setConfirmReject(false)} width={420} title="拒絕這個請求?">
        <span className="muted">AI 不會執行這個動作,執行紀錄會標為已拒絕。</span>
        <div className={"row gap8 " + css.end}>
          <button className="btn" onClick={() => setConfirmReject(false)}>取消</button>
          <button className="btn red" disabled={busy} onClick={() => decide(false)}>拒絕</button>
        </div>
      </Modal>
      <EditSheet open={editing} req={req} onClose={() => setEditing(false)} onApprove={(cmd) => decide(true, cmd)} busy={busy} />
    </div>
  );
}

function EditSheet({ open, req, onClose, onApprove, busy }: { open: boolean; req: Approval; onClose: () => void; onApprove: (cmd: string) => void; busy: boolean }) {
  const [cmd, setCmd] = useState(req.action);
  const [ack, setAck] = useState(false);
  useEffect(() => { if (open) { setCmd(req.action); setAck(false); } }, [open, req.action]);
  const changed = cmd !== req.action;
  return (
    <Modal open={open} onClose={onClose} width={560}>
      <div className="row gap8">
        <IconTile icon="pencil" size={30} />
        <div className="col grow"><span className="h3">修改後核准</span><span className="caption muted">{req.title}</span></div>
        <RiskBadge risk={req.risk} />
      </div>
      <span className="muted">調整 AI 準備的指令。核准後會照你修改的版本執行,並記錄在動態牆。</span>
      <AutoTextarea monospace minRows={3} maxRows={10} value={cmd} onChange={setCmd} />
      <div className="row gap8">
        {changed ? <><Pill text="已修改" color="orange" icon="pencil" /><button className="btn link" onClick={() => setCmd(req.action)}>還原 AI 版本</button></> : <span className="caption muted">尚未修改</span>}
      </div>
      {req.risk === "high" && <label className="row gap6"><input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} />我已確認這是高風險動作,並了解回滾計畫</label>}
      <div className={"row gap8 " + css.end}>
        <button className="btn" onClick={onClose}>取消</button>
        <button className="btn purple" disabled={busy || !cmd.trim() || (req.risk === "high" && !ack)} onClick={() => onApprove(cmd)}>核准修改後的指令</button>
      </div>
    </Modal>
  );
}

function DecidedRow({ req, edited }: { req: Approval; edited?: string }) {
  const d = useAuto();
  const dir = useDirectory();
  const info: Record<string, [string, string, string]> = {
    approved: [edited == null ? "已核准" : "修改後核准", "green", "checkmark.seal.fill"],
    rejected: ["已拒絕", "red", "xmark.seal.fill"],
    expired: ["已逾時", "secondary", "hourglass.bottomhalf.filled"],
    pending: ["待核准", "orange", "hand.raised"],
  };
  const [label, c, icon] = info[req.state] ?? info.pending;
  return (
    <div className={"row gap8 " + css.decidedRow} title={edited != null ? `修改後的指令:\n${edited}` : req.action}>
      <span className={css.stateIcon}><Icon name={icon} size={15} color={color(c)} /></span>
      <div className="col grow">
        <span className="ellipsis">{req.title}</span>
        <span className="caption muted ellipsis">{autoName(d, req.automation)}{req.decidedBy ? ` · ${dir.person(req.decidedBy)?.name ?? req.decidedBy} 決定` : ""}</span>
      </div>
      <RiskBadge risk={req.risk} />
      <Pill text={label} color={c} />
    </div>
  );
}
