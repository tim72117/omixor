// Issue 詳情的「子項」區塊:清單、前置與卡住資訊、可展開的 files / 驗收標準 / 完成證據,以及領取、回報完成 / 卡住、放回、取消。
// 資料與操作都走 MCP 工具(issueItemsApi.ts);權限與可見性由後端判斷,失敗時直接顯示後端的錯誤訊息。
import { useState } from "react";
import { useConfirm } from "../../components/Confirm";
import { Disclosure } from "../../components/Disclosure";
import { ErrorText } from "../../components/ErrorText";
import { MenuButton } from "../../components/Menu";
import { Pill } from "../../components/Pill";
import { SIcon } from "../../components/SIcon";
import { useAct } from "../../components/Toast";
import { relative } from "../../fmt";
import { ITEM_STATUS_LABEL, ITEM_WAIT_LABEL, type IssueItem, type ItemWaitKind } from "../../models";
import { cancelItem, claimItem, releaseItem, reportBlocked, reportDone, useIssueItems } from "./issueItemsApi";
import { blockedByDependency, dependencyOf, evidenceRows, ITEM_STATUS_COLOR, ITEM_STATUS_ICON, itemTerminal, whoName } from "./itemModel";
import s from "./IssueItems.module.css";

type Run = ReturnType<typeof useAct>["run"];
type Ask = ReturnType<typeof useConfirm>[1];

/** 子項區塊;沒有子項就不顯示(讀不到才顯示錯誤)。
 * 一律掛載(不看 Issue 的 itemsTotal):先接受、再拆子項時第一個子項才看得到,資料版本一變就重讀。
 * quietError:Issue 還沒有子項時讀不到(例如舊伺服器沒有 issue_items 工具)不顯示錯誤,避免每張 Issue 都跳錯 */
export function IssueItems({ issueKey, quietError = false }: { issueKey: string; quietError?: boolean }) {
  const { items, error, reload } = useIssueItems(issueKey);
  const [showDone, setShowDone] = useState(false);
  const { run, toastEl } = useAct();
  const [dlg, ask] = useConfirm();
  if (error && !items) return quietError ? null : <ErrorText>讀不到子項:{error}</ErrorText>;
  if (!items || items.length === 0) return null;
  const open = items.filter((i) => !itemTerminal(i));
  const closed = items.length - open.length;
  const shown = showDone ? items : open;
  return (
    <div className={"col " + s.root}>
      <div className={s.head}>
        <span className="h3">子項</span>
        <span className="muted caption">未完成 {open.length} / 共 {items.length}</span>
        {closed > 0 && <button className={"btn link " + s.toggle} onClick={() => setShowDone(!showDone)}>{showDone ? "隱藏已完成與取消" : `顯示已完成與取消(${closed})`}</button>}
      </div>
      {error && <ErrorText>{error}</ErrorText>}
      <div className={"col " + s.list}>
        {shown.length === 0 && <div className="muted caption">子項都已完成或取消。</div>}
        {shown.map((it) => <ItemRow key={it.id} item={it} all={items} run={run} ask={ask} reload={reload} />)}
      </div>
      {dlg}{toastEl}
    </div>
  );
}

type Form = "done" | "blocked" | undefined;

function ItemRow({ item, all, run, ask, reload }: { item: IssueItem; all: IssueItem[]; run: Run; ask: Ask; reload: () => void }) {
  const [form, setForm] = useState<Form>();
  const [busy, setBusy] = useState(false);
  const dep = dependencyOf(item, all);
  const depBlocks = blockedByDependency(item, all);
  const terminal = itemTerminal(item);
  const owner = whoName(item.assignee, item.assigneeActor);
  const claimer = whoName(item.claimedBy, item.claimedByActor);
  const ev = evidenceRows(item.evidence);
  const hasDetail = (item.files?.length ?? 0) > 0 || !!item.acceptance || ev.length > 0 || !!item.promotedIssueId;
  const act = async <T,>(f: () => Promise<T>, ok: string) => {
    setBusy(true);
    const r = await run(f, ok);
    setBusy(false);
    if (r !== undefined) { setForm(undefined); reload(); }
  };
  return (
    <div className={"col " + s.item + (terminal ? " " + s.dim : "")}>
      <div className={s.top}>
        <span className={s.key}>{item.key}</span>
        <span className={s.title + (item.status === "canceled" ? " " + s.struck : "")}>{item.title}</span>
        <Pill text={ITEM_STATUS_LABEL[item.status]} color={ITEM_STATUS_COLOR[item.status]} icon={ITEM_STATUS_ICON[item.status]} />
        {!terminal && (
          <span className={s.ops}>
            {item.status === "todo" && <button className="btn" disabled={busy || depBlocks} title={depBlocks ? `前置 ${item.dependsOn} 還沒完成,不能領取` : "領取(開始做)"} onClick={() => act(() => claimItem(item.key), "已領取 " + item.key)}>領取</button>}
            {item.status === "doing" && <button className="btn" disabled={busy || depBlocks} title={depBlocks ? `前置 ${item.dependsOn} 還沒完成,不能完成` : undefined} onClick={() => setForm(form === "done" ? undefined : "done")}>回報完成</button>}
            {item.status === "doing" && <button className="btn" disabled={busy} onClick={() => setForm(form === "blocked" ? undefined : "blocked")}>回報卡住</button>}
            <MenuButton className="btn" title="改狀態" label={<SIcon name="ellipsis.circle" />} align="right" items={[
              { header: "改狀態" },
              ...(item.status !== "todo" ? [{ label: item.status === "blocked" ? "解除卡住(放回待領取)" : "放回待領取", onClick: () => { void act(() => releaseItem(item.key), item.key + " 已放回待領取"); } }] : []),
              ...(item.status === "blocked" ? [{ label: "領取(進行中)", onClick: () => { void act(() => claimItem(item.key), "已領取 " + item.key); } }] : []),
              "sep" as const,
              { label: "取消這個子項…", danger: true, onClick: () => ask({ title: `取消 ${item.key}?`, message: "取消表示放棄這項工作(綁定的角色任務一併取消),取消後不能再改。", buttons: [{ label: "取消子項", danger: true, onClick: () => { void act(() => cancelItem(item.key), item.key + " 已取消"); } }] }) },
            ]} />
          </span>
        )}
      </div>
      <div className={s.meta}>
        {owner && <span title="負責人">負責:{owner}</span>}
        {claimer && item.status === "doing" && <span title="領取者">領取:{claimer}</span>}
        {item.dependsOn && (
          <span className={depBlocks ? s.warn : ""} title={dep ? dep.title : undefined}>
            前置:{item.dependsOn}{dep ? `(${ITEM_STATUS_LABEL[dep.status]})` : ""}{depBlocks ? " · 被擋住" : ""}
          </span>
        )}
        {item.completedAt && <span>完成於 {relative(item.completedAt)}</span>}
      </div>
      {item.status === "blocked" && (item.blockReason || item.waitFor) && (
        <div className={s.note + " " + s.warn}>
          {item.blockReason && <div>卡住:{item.blockReason}</div>}
          {item.waitFor && <div>等待{item.waitForKind ? ITEM_WAIT_LABEL[item.waitForKind] : ""}:{item.waitFor}</div>}
        </div>
      )}
      {form === "done" && <DoneForm busy={busy} onCancel={() => setForm(undefined)} onSubmit={(evidence, note) => act(() => reportDone(item.key, evidence, note), item.key + " 已完成")} />}
      {form === "blocked" && <BlockedForm busy={busy} onCancel={() => setForm(undefined)} onSubmit={(r, k, w) => act(() => reportBlocked(item.key, r, k, w), item.key + " 已標為卡住")} />}
      {hasDetail && (
        <Disclosure variant="native" summary="files、驗收標準、完成證據">
          <div className={"col " + s.detail}>
            {item.promotedIssueId && <div>已提升為 {item.promotedIssueId}</div>}
            {item.acceptance && <div><div className={s.label}>驗收標準</div>{item.acceptance}</div>}
            {(item.files?.length ?? 0) > 0 && <div><div className={s.label}>files</div><ul className={s.files}>{item.files!.map((f) => <li key={f}>{f}</li>)}</ul></div>}
            {ev.length > 0 && (
              <div>
                <div className={s.label}>完成證據</div>
                {ev.map((r, i) => <div key={i}>{r.key && <span className={s.label}>{r.key}:</span>}<pre className={s.ev}>{r.text}</pre></div>)}
              </div>
            )}
          </div>
        </Disclosure>
      )}
    </div>
  );
}

function DoneForm({ busy, onSubmit, onCancel }: { busy: boolean; onSubmit: (evidence: string, note: string) => void; onCancel: () => void }) {
  const [evidence, setEvidence] = useState("");
  const [note, setNote] = useState("");
  return (
    <div className={"col " + s.form}>
      <textarea className="textarea" placeholder="完成證據(必填):做了什麼、commit、測試結果、沒驗證到的部分" value={evidence} onChange={(e) => setEvidence(e.target.value)} />
      <input className="input" placeholder="備註(選填,會留在 Issue 的系統留言)" value={note} onChange={(e) => setNote(e.target.value)} />
      <div className={s.formBtns}>
        <button className="btn primary" disabled={busy || !evidence.trim()} onClick={() => onSubmit(evidence.trim(), note.trim())}>回報完成</button>
        <button className="btn" onClick={onCancel}>取消</button>
      </div>
    </div>
  );
}

function BlockedForm({ busy, onSubmit, onCancel }: { busy: boolean; onSubmit: (reason: string, kind?: ItemWaitKind, waitFor?: string) => void; onCancel: () => void }) {
  const [reason, setReason] = useState("");
  const [kind, setKind] = useState<ItemWaitKind | "">("");
  const [waitFor, setWaitFor] = useState("");
  return (
    <div className={"col " + s.form}>
      <textarea className="textarea" placeholder="卡住的原因(必填)" value={reason} onChange={(e) => setReason(e.target.value)} />
      <select className="select" value={kind} onChange={(e) => setKind(e.target.value as ItemWaitKind | "")}>
        <option value="">等待對象:不指定</option>
        {(Object.keys(ITEM_WAIT_LABEL) as ItemWaitKind[]).map((k) => <option key={k} value={k}>{ITEM_WAIT_LABEL[k]}</option>)}
      </select>
      {kind && <input className="input" placeholder={kind === "user_decision" ? "要決定的問題或 DEC-n" : kind === "item" ? "同 Issue 的另一個子項(例如 ISS-9.1)" : "外部條件說明"} value={waitFor} onChange={(e) => setWaitFor(e.target.value)} />}
      <div className={s.formBtns}>
        <button className="btn primary" disabled={busy || !reason.trim()} onClick={() => onSubmit(reason.trim(), kind || undefined, waitFor.trim() || undefined)}>回報卡住</button>
        <button className="btn" onClick={onCancel}>取消</button>
      </div>
    </div>
  );
}
