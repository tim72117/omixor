// Issue 垃圾桶(只有組織管理員):列出已刪除的 Issue,可以還原或永久刪除(永久刪除要二次確認,無法復原)。
import { useCallback, useEffect, useState } from "react";
import { useView } from "../../api";
import { useConfirm } from "../../components/Confirm";
import { EmptyState } from "../../components/EmptyState";
import { useAct } from "../../components/Toast";
import { useOmixor } from "../../lib/directory";
import { monthDayTime } from "../../lib/format";
import { loadTrash, purgeIssue, restoreIssue } from "./issueApi";
import { issueKey, type Issue } from "./issueModel";
import s from "./IssueTrash.module.css";
import { ModalTitle } from "../../components/Modal";

export function IssueTrash({ onClose }: { onClose: () => void }) {
  const o = useOmixor();
  const [list, setList] = useState<Issue[]>();
  const [err, setErr] = useState<string>();
  const [dlg, ask] = useConfirm();
  const { run, toastEl } = useAct();
  const reload = useCallback(() => {
    loadTrash().then((l) => { setList(Array.isArray(l) ? l : []); setErr(undefined); }).catch((e) => setErr(String(e?.message ?? e)));
  }, []);
  // 別處還原、刪除(收到 issues 通知)也重讀
  const [tick] = useView<unknown>("issues");
  useEffect(reload, [reload, tick]);
  const who = (a?: string) => (!a ? "—" : a === "mcp" ? "MCP 用戶端" : a.startsWith("agent:") ? o.name({ kind: "ai", id: a.slice(6) }) : o.name({ kind: "person", id: a }));
  return (
    <div className="col gap12">
      <ModalTitle>垃圾桶</ModalTitle>
      <span className="caption muted">已刪除的 Issue 在這裡。還原後回到原本的位置與編號;永久刪除就找不回來了。只有組織管理員看得到。</span>
      {err ? <EmptyState title="讀不到垃圾桶" icon="exclamationmark.triangle" message={err} />
        : !list ? <div className="muted">載入中…</div>
        : list.length === 0 ? <EmptyState title="垃圾桶是空的" icon="trash" />
        : (
          <div className={"col " + s.list}>
            {list.map((i) => (
              <div key={i.id} className={"row gap8 " + s.item}>
                <span className={"mono caption muted " + s.key}>{issueKey(i)}</span>
                <div className="col grow">
                  <span className="ellipsis">{i.title}</span>
                  <span className="caption muted">{who(i.deletedBy)} 刪除於 {i.deletedAt ? monthDayTime(i.deletedAt) : "—"}</span>
                </div>
                <button className="btn" onClick={async () => { await run(() => restoreIssue(i.id), `已還原 ${issueKey(i)}`); reload(); }}>還原</button>
                <button className="btn danger" onClick={() => ask({
                  title: `永久刪除 ${issueKey(i)}?`,
                  message: `「${i.title}」連同留言會被永久刪除,無法復原。`,
                  buttons: [{ label: "永久刪除(無法復原)", danger: true, onClick: async () => { await run(() => purgeIssue(i.id), `已永久刪除 ${issueKey(i)}`); reload(); } }],
                })}>永久刪除</button>
              </div>
            ))}
          </div>
        )}
      <div className="row"><span className="grow" /><button className="btn" onClick={onClose}>關閉</button></div>
      {dlg}{toastEl}
    </div>
  );
}
