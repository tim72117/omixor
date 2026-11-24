// 開發 › 待決定:要人選一個(decision)與要人做事(help)的清單、詳情與動作。
// 資料與操作走 MCP 工具(decisionApi.ts);純邏輯在 decisionModel.ts。撤回與重新開啟是 console 自己的工具,features 有宣告才開放按鈕(沒宣告時停用並說明)。
// 所有資料都以 React 文字節點顯示(內文走 MarkdownText,和 Issue 內文一樣),沒有外部網址。
import { useMemo, useState } from "react";
import { useConfirm } from "../../components/Confirm";
import { EmptyState } from "../../components/EmptyState";
import { Banner } from "../../components/Callout";
import { FilterBar } from "../../components/FilterBar";
import { MarkdownText } from "../../components/Markdown";
import { Modal, ModalTitle } from "../../components/Modal";
import { Pill } from "../../components/Pill";
import { SIcon } from "../../components/SIcon";
import { useAct } from "../../components/Toast";
import { monthDayTime } from "../../lib/format";
import { layoutClass } from "../../components/Layout";
import type { Decision } from "../../models";
import { useDev } from "./devShared";
import ds from "./devShared.module.css";
import s from "./DevDecisions.module.css";
import { addDecision, answerDecision, decisionToIssue, reopenDecision, useDecisions, useViewer, withdrawDecision } from "./decisionApi";
import {
  answerLabel, answerProblem, canAnswer, canReopen, canToIssue, canWithdraw, countByStatus, DECISION_LIMIT, draftProblem,
  emptyDraft, emptyOption, errorMessage, FEATURE_DECISION_REOPEN, FEATURE_DECISION_WITHDRAW, filterDecisions, KIND_COLOR, KIND_LABEL, MAX_OPTIONS,
  optionSummary, relativeTimeZh, sortDecisions, STATUS_COLOR, statusLabel, supportsFeature, UNSUPPORTED_REASON, whoLabel,
  type AnswerInput, type KindFilter, type NewDraft, type Perm, type StatusFilter, type Viewer,
} from "./decisionModel";

export function DevDecisionsTab() {
  const dev = useDev();
  const { list, total, error, reload } = useDecisions();
  const { viewer, features } = useViewer();
  const f = dev.decisionFilters, set = dev.setDecisionFilters;
  const [creating, setCreating] = useState(false);
  const all = list ?? [];
  const shown = useMemo(() => sortDecisions(filterDecisions(all, f, viewer)), [all, f, viewer.me, viewer.isHuman]);
  const counts = countByStatus(all);
  const issueKeys = useMemo(() => [...new Set(all.map((d) => d.sourceIssue).filter(Boolean))].sort(), [all]);
  const sel = all.find((d) => d.id === dev.selectedDecision);
  const statusOpt = (st: StatusFilter, text: string) => ({ value: st, text, label: <>{text}{counts[st] > 0 && <span className={"muted num " + s.optCount}>{counts[st]}</span>}</> });
  return (
    <div className={layoutClass.page}>
      <FilterBar
        filters={[
          { key: "kind", label: "種類", title: "依種類篩選", allLabel: "全部", value: f.kind, onChange: (v: string) => set({ kind: (v || "all") as KindFilter }), defaultValue: "all",
            options: [{ value: "all", label: "全部" }, { value: "decision", label: KIND_LABEL.decision }, { value: "help", label: KIND_LABEL.help }] },
          { key: "status", label: "狀態", title: "依狀態篩選", showDefault: true, value: f.status, defaultValue: "open", onChange: (v: string) => set({ status: (v || "open") as StatusFilter }),
            options: [statusOpt("open", "待處理"), statusOpt("decided", "已處理"), statusOpt("withdrawn", "已撤回"), statusOpt("all", "全部")] },
          { key: "mine", label: "範圍", title: "只看等我處理的", value: f.mine ? "mine" : "", onChange: (v: string) => set({ mine: v === "mine" }), allLabel: "全部",
            options: [{ value: "mine", label: "待我處理" }] },
          { key: "issue", label: "來源 Issue", title: "依來源 Issue 篩選", allLabel: "全部", value: f.issue, onChange: (v: string) => set({ issue: v }),
            options: [...issueKeys, ...(f.issue && !issueKeys.includes(f.issue) ? [f.issue] : [])].map((k) => ({ value: k, label: k })) },
        ]}
        end={(compact) => <button className="btn primary" title="提出新的待決定或需要協助" onClick={() => setCreating(true)}><SIcon name="plus" />{!compact && "提出新的待決定"}</button>} />
      <div className={layoutClass.fill}>
        <div className={ds.sideList + " " + s.side}>
          {error && !list ? <EmptyState title="讀不到待決定" icon="exclamationmark.triangle" message={error} />
            : !list ? null : shown.length === 0 ? (
              <EmptyState title={all.length === 0 ? "還沒有待決定" : "沒有符合的項目"} icon="questionmark.bubble"
                message={all.length === 0 ? "AI 或你可以用「提出新的待決定」(或 MCP 的 issue_decision_add)把要人選擇、要人做的事放在這裡。" : "換一個篩選條件看看。"} />
            ) : (
              <div className="list">
                {total > DECISION_LIMIT && <div className="caption muted" style={{ padding: "6px 12px" }}>只顯示最近 {DECISION_LIMIT} 筆(共 {total} 筆)</div>}
                {shown.map((d) => <DecisionRow key={d.id} d={d} on={d.id === dev.selectedDecision} onClick={() => dev.setSelectedDecision(d.id)} />)}
              </div>
            )}
        </div>
        <div className={layoutClass.scroll}>
          {sel ? <DecisionDetail key={sel.id} d={sel} viewer={viewer} features={features} reload={reload} />
            : <EmptyState title="選一筆待決定" icon="questionmark.bubble" message="左邊選一筆看內文、選項與處理紀錄,並回答、撤回或由它開 Issue。" />}
        </div>
      </div>
      <Modal open={creating} onClose={() => setCreating(false)} width={600}>
        <NewDecision defaultIssue={f.issue} onDone={(id) => { setCreating(false); reload(); if (id) { dev.setSelectedDecision(id); set({ status: "open" }); } }} />
      </Modal>
    </div>
  );
}

function KindPill({ d }: { d: Pick<Decision, "kind"> }) { return <Pill text={KIND_LABEL[d.kind]} color={KIND_COLOR[d.kind]} />; }
function StatusPill({ d }: { d: Pick<Decision, "kind" | "status"> }) { return <Pill text={statusLabel(d)} color={STATUS_COLOR[d.status]} />; }

function DecisionRow({ d, on, onClick }: { d: Decision; on: boolean; onClick: () => void }) {
  const opts = optionSummary(d.options);
  return (
    <div className={"list-row col gap4" + (on ? " on" : "")} onClick={onClick}>
      <div className={ds.rowTop + " gap6"}>
        <SIcon name={d.status === "open" ? "circle.dotted" : d.status === "decided" ? "checkmark.circle.fill" : "xmark.circle"} color={d.status === "open" ? "orange" : "secondary"} className={ds.rowIcon} />
        <span className={ds.clamp2 + (d.status === "open" ? "" : " " + ds.secondary)}>{d.title}</span>
      </div>
      <div className="meta gap6 wrap">
        <span className="caption mono muted">{d.id}</span>
        {d.sourceIssue && <span className="caption mono muted" title="來源 Issue">{d.sourceIssue}</span>}
        <KindPill d={d} />
        <StatusPill d={d} />
        <span className="caption muted">{whoLabel(d.createdByActor, d.createdBy)} · {relativeTimeZh(d.createdAt)}</span>
      </div>
      {opts && <span className={"caption muted " + s.summary} title={opts}>{opts}</span>}
    </div>
  );
}


function DecisionDetail({ d, viewer, features, reload }: { d: Decision; viewer: Viewer; features?: string[]; reload: () => void }) {
  const dev = useDev();
  const { run, toast, toastEl } = useAct();
  const [dlg, ask] = useConfirm();
  const [answering, setAnswering] = useState(false);
  // 執行動作:成功提示、失敗顯示整理過的訊息(403 / 404 / 409);不管成敗都重讀(409 代表狀態已變)
  const act = async <T,>(f: () => Promise<T>, ok: string): Promise<T | undefined> => {
    const r = await run(async () => { try { return await f(); } catch (e) { throw new Error(errorMessage(e)); } }, ok);
    reload();
    return r;
  };
  const supported = (feat: string): Perm => (supportsFeature(features, feat) ? { ok: true } : { ok: false, reason: UNSUPPORTED_REASON });
  const both = (a: Perm, b: Perm): Perm => (a.ok ? b : a);
  const pAnswer = canAnswer(d, viewer);
  const pWithdraw = both(canWithdraw(d, viewer), supported(FEATURE_DECISION_WITHDRAW));
  const pReopen = both(canReopen(d, viewer), supported(FEATURE_DECISION_REOPEN));
  const pIssue = canToIssue(d, viewer);
  const btn = (p: Perm, label: React.ReactNode, onClick: () => void, cls = "btn") => (
    <button className={cls} disabled={!p.ok} title={p.ok ? undefined : p.reason} onClick={onClick}>{label}</button>
  );
  const picked = d.status === "decided" ? d.answer : "";
  return (
    <div className={"col " + s.detail}>
      <div className="col gap8">
        <div className={ds.rowTop + " gap8 wrap"}>
          <div className={s.titleRow}>
            <span className={"h2 " + s.title}>{d.title}</span>
            <span className={"muted " + s.id}>{d.id}</span>
          </div>
        </div>
        <div className="row gap6 wrap">
          <KindPill d={d} /><StatusPill d={d} />
          {d.sourceIssue
            ? <button className={"btn link " + s.srcLink} title="打開來源 Issue" onClick={() => dev.showIssue(d.sourceIssue)}><SIcon name="exclamationmark.bubble" size={12} /> {d.sourceIssue}</button>
            : <span className="caption muted">沒有來源 Issue</span>}
          {d.blocks.map((b) => <Pill key={b} text={"擋住:" + b} icon="hand.raised" />)}
        </div>
        <div className="row gap12 caption muted wrap">
          <span>{whoLabel(d.createdByActor, d.createdBy)} 提出於 {monthDayTime(d.createdAt)}({relativeTimeZh(d.createdAt)})</span>
        </div>
        <div className={s.ops}>
          {btn(pAnswer, <><SIcon name="checkmark.circle" /> 回答</>, () => setAnswering(!answering), "btn primary")}
          {btn(pWithdraw, "撤回", () => ask({ title: `撤回 ${d.id}?`, message: "撤回後它不再等人處理;之後可以由有權限的人重新開啟的前提是伺服器支援。", buttons: [{ label: "撤回", danger: true, onClick: async () => { await act(() => withdrawDecision(d.id), "已撤回 " + d.id); } }] }), "btn")}
          {btn(pReopen, "重新開啟", () => ask({ title: `重新開啟 ${d.id}?`, message: "它會回到待處理,之前的回答會保留在紀錄裡。", buttons: [{ label: "重新開啟", onClick: async () => { await act(() => reopenDecision(d.id), "已重新開啟 " + d.id); } }] }))}
          {btn(pIssue, <><SIcon name="plus" /> 由它開 Issue</>, () => ask({ title: `由 ${d.id} 開一張 Issue?`, message: "會開一張問題 Issue(標題是這個問題,內文含背景與選項),並讓它的來源 Issue 指向新的 Issue。", buttons: [{ label: "開 Issue", onClick: async () => {
            const r = await act(() => decisionToIssue(d.id), "");
            if (r) toast(r.message || "已開出 " + (r.issue?.key ?? "Issue"));
          } }] }))}
        </div>
      </div>
      <div className="divider" />
      {answering && pAnswer.ok && <AnswerForm d={d} run={act} onDone={() => setAnswering(false)} />}
      {d.body ? <MarkdownText source={d.body} /> : <div className="muted">沒有內文</div>}
      {d.kind === "decision" && d.options.length > 0 && (
        <div className="col gap8">
          <span className="h3">選項</span>
          {d.options.map((o) => (
            <div key={o.id} className={"col " + s.option + (picked === o.id ? " " + s.optionPicked : "")}>
              <div className="row gap6 wrap">
                <span className={"bold " + s.optionLabel}>{o.label}</span>
                {o.recommended && <Pill text="建議" color="green" icon="star.fill" />}
                {picked === o.id && <Pill text="已選" color="blue" icon="checkmark" />}
              </div>
              {o.description && <span className="caption muted">{o.description}</span>}
            </div>
          ))}
        </div>
      )}
      <div className="col gap8">
        <span className="h3">處理紀錄</span>
        {d.status === "open" ? <div className="muted caption">還沒有人處理。</div> : d.status === "withdrawn" ? <div className="muted caption">已撤回,沒有處理紀錄。</div> : (
          <div className={"col " + s.answer}>
            <div className="row gap6 wrap">
              <span className="bold">{answerLabel(d)}</span>
              <span className="caption muted">{whoLabel(d.decidedByActor, d.decidedBy)} · {d.decidedAt ? monthDayTime(d.decidedAt) + "(" + relativeTimeZh(d.decidedAt) + ")" : ""}</span>
            </div>
            {d.answerNote && <div className={"col gap4"}><span className="caption muted">說明</span><span className={s.answerNote}>{d.answerNote}</span></div>}
            {d.evidence && <div className={"col gap4"}><span className="caption muted">證據</span><span className={s.answerNote}>{d.evidence}</span></div>}
          </div>
        )}
        <span className="caption muted">結論會自動寫成來源 Issue 的留言。</span>
      </div>
      {dlg}{toastEl}
    </div>
  );
}

function AnswerForm({ d, run, onDone }: { d: Decision; run: <T>(f: () => Promise<T>, ok: string) => Promise<T | undefined>; onDone: () => void }) {
  const [a, setA] = useState<AnswerInput>({ choice: "", note: "", evidence: "" });
  const [busy, setBusy] = useState(false);
  const problem = answerProblem(d, a);
  const choices = d.kind === "help"
    ? [{ id: "done", label: "已完成" }, { id: "declined", label: "婉拒" }]
    : [...d.options.map((o) => ({ id: o.id, label: o.label + (o.recommended ? "(建議)" : "") })), { id: "other", label: "其他(要寫說明)" }];
  const needNote = a.choice === "other" || a.choice === "declined";
  return (
    <div className={"col " + s.form}>
      <span className="h3">{d.kind === "help" ? "回報處理結果" : "選擇答案"}</span>
      {choices.map((c) => (
        <label key={c.id} className={s.optionRow}>
          <input type="radio" name={"ans-" + d.id} checked={a.choice === c.id} onChange={() => setA({ ...a, choice: c.id })} />
          <span className={s.optionLabel}>{c.label}</span>
        </label>
      ))}
      <textarea className={"textarea " + s.note} placeholder={needNote ? "說明(必填)" : "補充說明(選填)"} value={a.note} onChange={(e) => setA({ ...a, note: e.target.value })} />
      {d.kind === "help" && a.choice === "done" && <input className="input" placeholder="證據(連結、輸出、版本號;選填)" value={a.evidence} onChange={(e) => setA({ ...a, evidence: e.target.value })} />}
      <div className="row gap8">
        <span className="caption muted">送出後結論會留言到來源 Issue</span>
        <span className="grow" />
        <button className="btn" onClick={onDone}>取消</button>
        <button className="btn primary" disabled={!!problem || busy} title={problem || undefined} onClick={async () => {
          setBusy(true);
          const r = await run(() => answerDecision(d, a), "已回答 " + d.id);
          setBusy(false);
          if (r) onDone();
        }}>送出</button>
      </div>
    </div>
  );
}

function NewDecision({ defaultIssue, onDone }: { defaultIssue: string; onDone: (id?: string) => void }) {
  const [draft, setDraft] = useState<NewDraft>(emptyDraft(defaultIssue));
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const set = (p: Partial<NewDraft>) => setDraft((d) => ({ ...d, ...p }));
  const setOpt = (i: number, p: Partial<NewDraft["options"][number]>) => set({ options: draft.options.map((o, k) => (k === i ? { ...o, ...p } : o)) });
  const problem = draftProblem(draft);
  const submit = async () => {
    setBusy(true); setError(undefined);
    try { const r = await addDecision(draft); onDone(r?.id); }
    catch (e) { setError(errorMessage(e)); }
    finally { setBusy(false); }
  };
  return (
    <div className="col gap12">
      <ModalTitle>提出新的待決定</ModalTitle>
      <div className="row gap8 wrap">
        <label className="row gap6">種類
          <select className="select" value={draft.kind} onChange={(e) => set({ kind: e.target.value as NewDraft["kind"] })}>
            <option value="decision">{KIND_LABEL.decision}(要人選一個)</option>
            <option value="help">{KIND_LABEL.help}(要人做事)</option>
          </select>
        </label>
        <input className="input grow" placeholder="來源 Issue(選填,例如 ISS-37)" value={draft.sourceIssue} onChange={(e) => set({ sourceIssue: e.target.value })} />
      </div>
      <input className="input" placeholder="標題(一句話說明問題或請求)" value={draft.title} autoFocus onChange={(e) => set({ title: e.target.value })} />
      <textarea className={"textarea " + s.body} placeholder="內文(背景,支援 Markdown;選填)" value={draft.body} onChange={(e) => set({ body: e.target.value })} />
      {draft.kind === "decision" && (
        <div className="col gap6">
          <span className="caption muted">選項(至少 2 個,可標一個建議;最多 {MAX_OPTIONS} 個)</span>
          {draft.options.map((o, i) => (
            <div key={i} className={s.optEdit}>
              <input className="input grow" placeholder={"選項 " + (i + 1)} value={o.label} onChange={(e) => setOpt(i, { label: e.target.value })} />
              <input className="input grow" placeholder="說明(選填)" value={o.description} onChange={(e) => setOpt(i, { description: e.target.value })} />
              <label className="row gap4 caption" title="標成建議的選項"><input type="checkbox" checked={o.recommended} onChange={(e) => set({ options: draft.options.map((x, k) => ({ ...x, recommended: k === i ? e.target.checked : false })) })} />建議</label>
              <button className="btn small" title="移除這個選項" disabled={draft.options.length <= 2} onClick={() => set({ options: draft.options.filter((_, k) => k !== i) })}><SIcon name="xmark" size={11} /></button>
            </div>
          ))}
          <span><button className="btn small" disabled={draft.options.length >= MAX_OPTIONS} onClick={() => set({ options: [...draft.options, emptyOption()] })}><SIcon name="plus" size={11} /> 新增選項</button></span>
        </div>
      )}
      {error && <Banner>{error}</Banner>}
      <div className="row gap8">
        <span className="grow" />
        <button className="btn" onClick={() => onDone()}>取消</button>
        <button className="btn primary" disabled={!!problem || busy} title={problem || undefined} onClick={submit}>提出</button>
      </div>
    </div>
  );
}
