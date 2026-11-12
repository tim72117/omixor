// 設定 › 組織成員:接 omixor 核心的成員(角色 admin / member);本機與遠端都支援
import { useState } from "react";
import { action, useView } from "../../api";
import { Icon } from "../../components/Icon";
import { Pill } from "../../components/ui";
import { Row, Section } from "./form";

type Member = { org: string; userId: string; email: string; name: string; role: string; addedAt: string };
type MembersView = { org: string; remote: boolean; canEdit: boolean; members: Member[]; error?: string };
const ROLES = [{ id: "admin", label: "管理員" }, { id: "member", label: "成員" }];

export function SettingsMembers() {
  const [v, , reload] = useView<MembersView>("members", ["members", "orgs"]);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("member");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const run = async (f: () => Promise<unknown>) => {
    setBusy(true); setErr("");
    try { await f(); reload(); } catch (e: any) { setErr(String(e?.message ?? e)); } finally { setBusy(false); }
  };
  if (!v) return <Section header="組織成員"><Row><span className="muted">載入中…</span></Row></Section>;
  const members = v.members ?? [];
  const canEdit = v.canEdit;
  const add = () => email.trim() && run(async () => { await action("member_save", { email: email.trim(), role }); setEmail(""); });
  const remove = (m: Member) => {
    if (!window.confirm(`把 ${m.email} 從這個組織移除?`)) return;
    run(() => action("member_remove", { email: m.email }));
  };
  return (
    <Section header={`組織成員(${members.length})`}
      footer={v.remote && !canEdit ? "你不是這個組織的管理員,只能檢視成員;要新增或移除請找組織管理員。" : "管理員可以新增、移除成員與改角色;成員只能存取組織的內容。"}>
      {v.error && <Row><span className="row gap6" style={{ color: "var(--red, #d33)" }}><Icon name="exclamationmark.triangle" size={13} />{v.error}</span></Row>}
      {!v.error && members.length === 0 && <Row><span className="muted">尚未加入任何成員</span></Row>}
      {members.map((m) => (
        <Row key={m.email}>
          <Icon name="person.crop.circle" size={22} />
          <div className="col grow">
            <span style={{ fontWeight: 500 }}>{m.name || m.email}</span>
            <span className="caption muted">{m.name ? m.email : ""}</span>
          </div>
          {canEdit ? (
            <>
              <select className="select" style={{ minWidth: 100 }} disabled={busy} value={m.role} onChange={(e) => run(() => action("member_save", { email: m.email, role: e.target.value }))}>
                {ROLES.map((r) => <option key={r.id} value={r.id}>{r.label}</option>)}
              </select>
              <button className="bx-icon-btn" title="移除" disabled={busy} onClick={() => remove(m)}><Icon name="trash" size={14} /></button>
            </>
          ) : <Pill text={ROLES.find((r) => r.id === m.role)?.label ?? m.role} color={m.role === "admin" ? "var(--accent)" : "secondary"} />}
        </Row>
      ))}
      {canEdit && !v.error && (
        <Row>
          <input className="input grow" type="email" placeholder="新成員的 email" value={email} disabled={busy} onChange={(e) => setEmail(e.target.value)} onKeyDown={(e) => e.key === "Enter" && add()} />
          <select className="select" style={{ minWidth: 100 }} value={role} onChange={(e) => setRole(e.target.value)}>
            {ROLES.map((r) => <option key={r.id} value={r.id}>{r.label}</option>)}
          </select>
          <button className="btn" disabled={busy || !email.trim()} onClick={add}>新增</button>
        </Row>
      )}
      {err && <Row><span style={{ color: "var(--red, #d33)" }}>{err}</span></Row>}
    </Section>
  );
}
