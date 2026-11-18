// 文件櫃的專案切換:未分專案(原本的組織文件櫃)+ 組織的各專案(PRJ-<n>)。專案只分文件櫃(結構與文件),權限沿用組織成員;
// 新增、改名、刪除專案要組織管理員(核心檢查;裡面還有文件時刪不掉,顯示伺服器的錯誤訊息)
import { useState } from "react";
import { action, useRun } from "../../api";
import { Icon } from "../../components/Icon";
import type { Project } from "../../components/models";
import { MenuButton } from "../../components/omixorKit";
import { ErrorText, Modal } from "../../components/ui";

/** 專案的顯示名稱:未分專案 / 名稱(找不到時顯示 id) */
export const projectLabel = (id: string | undefined, projects: Project[]) =>
  !id ? "未分專案" : projects.find((p) => p.id === id)?.name ?? id;

export function ProjectBar({ project, projects, projectsError, onChange }: { project: string; projects: Project[]; projectsError?: string; onChange: (id: string) => void }) {
  const [editing, setEditing] = useState<Project | "new">();
  const [deleting, setDeleting] = useState<Project>();
  const cur = projects.find((p) => p.id === project);
  return (
    <div className="col gap4" style={{ padding: "8px 8px 6px", borderBottom: "1px solid var(--line)" }}>
      <div className="row gap6">
        <Icon name="square.stack.3d.up" size={13} className="muted" />
        <select className="select grow" style={{ minWidth: 0 }} value={project} title={cur?.description || undefined} onChange={(e) => onChange(e.target.value)}>
          <option value="">未分專案</option>
          {projects.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
          {project && !cur && <option value={project}>{project}</option>}
        </select>
        <MenuButton className="bx-icon-btn" title="專案" align="right" label={<Icon name="ellipsis.circle" size={14} />} items={[
          { label: "新增專案…", onClick: () => setEditing("new") },
          { label: "改名或說明…", disabled: !cur, onClick: () => cur && setEditing(cur) },
          "sep",
          { label: "刪除專案…", danger: true, disabled: !cur, onClick: () => cur && setDeleting(cur) },
        ]} />
      </div>
      {cur?.description && <span className="caption muted ellipsis" title={cur.description}>{cur.description}</span>}
      {projectsError && <span className="caption" style={{ color: "var(--orange)" }} title={projectsError}>讀不到專案清單:{projectsError}</span>}
      {editing && <ProjectSheet project={editing === "new" ? undefined : editing} onClose={() => setEditing(undefined)} onSaved={(p) => onChange(p.id)} />}
      {deleting && <DeleteProjectSheet project={deleting} onClose={() => setDeleting(undefined)} onDeleted={() => onChange("")} />}
    </div>
  );
}

/** 新增或修改專案(名稱必填、組織內不重複,由伺服器檢查) */
function ProjectSheet({ project, onClose, onSaved }: { project?: Project; onClose: () => void; onSaved: (p: Project) => void }) {
  const [name, setName] = useState(project?.name ?? "");
  const [description, setDescription] = useState(project?.description ?? "");
  const { busy, error, run } = useRun();
  const save = () => run(async () => {
    const p = project
      ? await action<Project>("project_update", { id: project.id, name: name.trim(), description: description.trim() })
      : await action<Project>("project_create", { name: name.trim(), description: description.trim() });
    if (p?.id) onSaved(p);
    onClose();
  });
  return (
    <Modal open onClose={onClose} width={460} title={project ? `專案 ${project.id}` : "新增專案"}>
      <span className="caption muted">專案只分文件櫃(各自一份結構與文件);Issue、頻道、角色都還是屬於組織,組織成員看得到所有專案。</span>
      <input className="input" autoFocus placeholder="專案名稱" value={name} onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => { if (e.key === "Enter" && name.trim() && !busy) save(); }} />
      <textarea className="textarea" style={{ minHeight: 80 }} placeholder="說明(選填)" value={description} onChange={(e) => setDescription(e.target.value)} />
      <ErrorText>{error}</ErrorText>
      <div className="row gap8" style={{ justifyContent: "flex-end" }}>
        <button className="btn" onClick={onClose}>取消</button>
        <button className="btn primary" disabled={!name.trim() || busy} onClick={save}>{project ? "儲存" : "建立"}</button>
      </div>
    </Modal>
  );
}

/** 刪除專案:裡面還有文件時伺服器會拒絕,錯誤訊息照伺服器的顯示 */
function DeleteProjectSheet({ project, onClose, onDeleted }: { project: Project; onClose: () => void; onDeleted: () => void }) {
  const { busy, error, run } = useRun();
  const del = () => run(async () => { await action("project_delete", { id: project.id }); onDeleted(); onClose(); });
  return (
    <Modal open onClose={onClose} width={420} title={`刪除專案「${project.name}」?`}>
      <span className="muted">專案的文件櫃結構會一起刪除。裡面還有文件時不能刪,請先把文件搬到別的專案或刪除。</span>
      <ErrorText>{error}</ErrorText>
      <div className="row gap8" style={{ justifyContent: "flex-end" }}>
        <button className="btn" onClick={onClose}>取消</button>
        <button className="btn red" disabled={busy} onClick={del}>刪除</button>
      </div>
    </Modal>
  );
}
