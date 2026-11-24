// Issue 的建立與編輯表單:所屬倉庫、所屬專案、版本衝突處理(編輯時記住讀到的 version,送出帶回去)。
import { useEffect, useState } from "react";
import { tool, useView } from "../../api";
import { Banner } from "../../components/Callout";
import { MdTextarea } from "../../components/MdTextarea";
import { SIcon } from "../../components/SIcon";
import type { Project } from "../../models";
import { repoKey } from "./devShared";
import ds from "./devShared.module.css";
import s from "./IssueForm.module.css";
import { updateIssue, useProjects } from "./issueApi";
import {
  conflictCurrent, CONFLICT_MESSAGE, PRIORITIES, PRIORITY, DRAFT_LABEL, draftChanges, draftDiff, draftOf, isVersionConflict, mergeLatest, splitLabels,
  type Draft, type Issue, type MergeConflict, type Priority,
} from "./issueModel";
import { ModalTitle } from "../../components/Modal";

// 所屬倉庫(repo):owner/name(GitHub)或 group/sub/project(GitLab),可加 github:、gitlab: 前綴;"" = 未標記。
// 格式同核心 model.CleanRepo(只允許英數與 - _ . / :,最長 200);存的時候核心會再檢查一次
export const REPO_RE = /^[A-Za-z0-9._/:-]{0,200}$/;
type IssueRepos = { github: string[]; gitlab: string[]; used: string[] };

/** 候選倉庫:GitHub 追蹤的 repos、GitLab 追蹤的專案(加 gitlab: 前綴)、其他 Issue 用過的(去重,不分大小寫與前綴) */
function useRepoOptions(): { label: string; options: string[] }[] {
  const [v] = useView<IssueRepos>("issue_repos", ["github", "gitlab", "issues"]);
  const seen = new Set<string>();
  const take = (l: string[] | undefined) => (l ?? []).filter((r) => {
    if (!r || seen.has(repoKey(r))) return false;
    seen.add(repoKey(r));
    return true;
  });
  return [
    { label: "GitHub", options: take(v?.github) },
    { label: "GitLab", options: take((v?.gitlab ?? []).map((p) => "gitlab:" + p)) },
    { label: "其他 Issue 用過的", options: take(v?.used) },
  ].filter((g) => g.options.length > 0);
}

/** 設定所屬倉庫:下拉選已設定的倉庫、「不標記」,或「其他…」自己輸入 */
export function RepoPicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const groups = useRepoOptions();
  const known = groups.some((g) => g.options.includes(value));
  const [custom, setCustom] = useState(false);
  const other = custom || (value !== "" && !known);
  const bad = !REPO_RE.test(value.trim());
  return (
    <div className="row gap6">
      <SIcon name="chevron.left.forwardslash.chevron.right" className="muted" />
      <select className="select" title="所屬倉庫" value={other ? "__other" : value} onChange={(e) => {
        if (e.target.value === "__other") { setCustom(true); return; }
        setCustom(false); onChange(e.target.value);
      }}>
        <option value="">不標記倉庫</option>
        {groups.map((g) => <optgroup key={g.label} label={g.label}>{g.options.map((r) => <option key={r} value={r}>{r}</option>)}</optgroup>)}
        <option value="__other">其他…</option>
      </select>
      {other && <input className="input grow" placeholder="owner/name 或 gitlab:group/project" value={value} onChange={(e) => onChange(e.target.value)} />}
      {bad && <span className={"caption " + ds.red}>只能用英數與 - _ . / :(最長 200)</span>}
    </div>
  );
}

/** 專案的顯示:名稱(代號) */
export const projectName = (p: Project) => (p.key ? `${p.name}(${p.key})` : p.name);

/** 選所屬專案:「組織」= 組織層級(ISS-n);還沒設定代號的專案不能放 Issue。搬專案後會發新的編號,舊編號仍然查得到 */
export function ProjectPicker({ value, onChange, projects, moving }: { value: string; onChange: (v: string) => void; projects: Project[]; moving?: boolean }) {
  const cur = projects.find((p) => p.id === value);
  return (
    <div className="row gap6">
      <SIcon name="square.stack.3d.up" className="muted" />
      <select className="select" title="所屬專案" value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">組織(ISS-編號)</option>
        {projects.map((p) => <option key={p.id} value={p.id} disabled={!p.key && p.id !== value}>{projectName(p)}{p.key ? "" : "(尚未設定代號)"}</option>)}
        {value && !cur && <option value={value}>{value}</option>}
      </select>
      {moving && <span className="caption muted">搬移後會換成新的編號,舊編號仍然查得到</span>}
    </div>
  );
}

/**
 * 編輯 Issue。開啟時記住讀到的 version 與內容(base),送出帶回去;
 * 版本不符(別人先改了)時不蓋掉:顯示「這張 Issue 已被別人更新」,按「重新載入最新內容」做三方合併(base、我的草稿、最新內容):
 * 我沒改過的欄位自動採用最新值,只有我和別人都改、而且不同的欄位才列出差異,由使用者決定用哪邊。
 * 表單開著時畫面上的 Issue 版本變了(自己在表單外按優先度、指派、關閉,或別人改了):同樣合併、換成新的基準,避免自己讓自己 409。
 */
export function IssueEditForm({ issue, onClose, onSaved }: { issue: Issue; onClose: () => void; onSaved?: (i: Issue) => void }) {
  const projects = useProjects();
  const [base, setBase] = useState<Issue>(issue);
  const [draft, setDraft] = useState<Draft>(() => draftOf(issue));
  const [conflict, setConflict] = useState<{ latest?: Issue }>();
  /** 合併後的提示:載入的版本、自動換成最新值的欄位、兩邊都改而要使用者決定的欄位 */
  const [merged, setMerged] = useState<{ version?: number; adopted: (keyof Draft)[]; conflicts: MergeConflict[] }>();
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setDraft((d) => ({ ...d, [k]: v }));
  const changes = draftChanges(draft, base);
  const valid = !!draft.title.trim() && REPO_RE.test(draft.repo.trim());

  const save = async () => {
    setBusy(true); setError(undefined);
    try {
      const r = await updateIssue(base, changes);
      onSaved?.(r);
      onClose();
    } catch (e: any) {
      if (isVersionConflict(e)) { setConflict({ latest: conflictCurrent(e) }); setMerged(undefined); }
      else setError(String(e?.message ?? e));
    } finally { setBusy(false); }
  };
  // 合併最新內容:以目前的基準(開啟時的原始內容)做三方合併,之後基準換成最新內容
  const mergeIn = (latest: Issue, announce: boolean) => {
    const r = mergeLatest(base, draft, latest);
    setDraft(r.draft);
    setBase(latest);
    setConflict(undefined);
    if (announce || r.conflicts.length > 0 || r.adopted.length > 0) setMerged({ version: latest.version, adopted: r.adopted, conflicts: r.conflicts });
  };
  // 重新載入:最新內容有附就用,沒附(舊伺服器)用畫面目前的 Issue
  const reload = () => mergeIn(conflict?.latest ?? issue, true);
  // 畫面上的 Issue 版本往前(快速動作、別人的修改):自動合併、換基準
  useEffect(() => {
    if (issue.version !== undefined && base.version !== undefined && issue.version > base.version) mergeIn(issue, false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [issue.version]);
  // 還沒處理的衝突欄位(使用者按「改用最新的」或自己改成一樣就消失)
  const diff = merged ? draftDiff(draft, base).filter((d) => merged.conflicts.some((c) => c.field === d.field)) : [];
  return (
    <div className="col gap8">
      {conflict && (
        <div className="col gap6">
          <Banner>{CONFLICT_MESSAGE}。你還沒送出的編輯不會被蓋掉。</Banner>
          <div className="row gap8">
            <button className="btn" onClick={reload}>重新載入最新內容</button>
            <button className="btn" onClick={() => setConflict(undefined)}>先不處理</button>
          </div>
        </div>
      )}
      {merged && !conflict && (
        <div className={"col gap6 " + s.merged}>
          <span className="caption">
            已載入最新內容(第 {merged.version ?? base.version ?? "?"} 版),你的編輯還在。
            {merged.adopted.length > 0 && `你沒改過的欄位(${merged.adopted.map((f) => DRAFT_LABEL[f]).join("、")})已換成最新內容。`}
            {diff.length > 0 ? "下列欄位你和別人都改了,請決定要保留哪一邊再儲存(目前保留你的):" : "沒有需要你決定的衝突。"}
          </span>
          {diff.map((d) => (
            <div key={d.field} className="col gap4">
              <span className={"caption " + ds.bold}>{DRAFT_LABEL[d.field]}</span>
              <span className={"caption muted " + s.theirs}>最新:{d.theirs || "(空)"}</span>
              <span><button className={"btn " + s.small} onClick={() => set(d.field, d.theirs)}>改用最新的</button></span>
            </div>
          ))}
        </div>
      )}
      <input className={"input " + s.title} placeholder="標題" value={draft.title} onChange={(e) => set("title", e.target.value)} />
      <input className="input" placeholder="標籤(逗號分隔)" value={draft.labels} onChange={(e) => set("labels", e.target.value)} />
      <RepoPicker value={draft.repo} onChange={(v) => set("repo", v)} />
      <ProjectPicker value={draft.project} onChange={(v) => set("project", v)} projects={projects} moving={draft.project !== (base.project ?? "")} />
      <MdTextarea monospace minRows={11} maxRows={24} value={draft.body} onChange={(v) => set("body", v)} />
      {error && <Banner>{error}</Banner>}
      <div className="row gap8">
        <span className="caption muted">內文支援 Markdown</span>
        <span className="grow" />
        <button className="btn" onClick={onClose}>取消</button>
        <button className="btn primary" disabled={!valid || busy || Object.keys(changes).length === 0} onClick={save}>儲存</button>
      </div>
    </div>
  );
}

export function NewIssue({ onDone, defaultProject = "" }: { onDone: (id?: string) => void; defaultProject?: string }) {
  const projects = useProjects();
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [labels, setLabels] = useState("");
  const [priority, setPriority] = useState<Priority>("normal");
  const [repo, setRepo] = useState("");
  const [project, setProject] = useState(defaultProject);
  const [error, setError] = useState<string>();
  const create = async () => {
    try {
      const r: any = await tool("issue_create", { return: "full", title: title.trim(), body, priority, labels: splitLabels(labels), ...(repo.trim() ? { repo: repo.trim() } : {}), ...(project ? { project } : {}) });
      const o = typeof r === "string" ? (() => { try { return JSON.parse(r); } catch { return undefined; } })() : r;
      onDone(o?.id);
    } catch (e: any) { setError(String(e?.message ?? e)); }
  };
  return (
    <div className="col gap12">
      <ModalTitle>新增 Issue</ModalTitle>
      <input className="input" placeholder="標題" value={title} autoFocus onChange={(e) => setTitle(e.target.value)} />
      <div className="row gap8">
        <label className="row gap6">優先度
          <select className="select" value={priority} onChange={(e) => setPriority(e.target.value as Priority)}>{PRIORITIES.map((p) => <option key={p} value={p}>{PRIORITY[p].label}</option>)}</select>
        </label>
        <input className="input grow" placeholder="標籤(逗號分隔)" value={labels} onChange={(e) => setLabels(e.target.value)} />
      </div>
      <ProjectPicker value={project} onChange={setProject} projects={projects} />
      <RepoPicker value={repo} onChange={setRepo} />
      <MdTextarea monospace minRows={9} maxRows={24} value={body} onChange={setBody} />
      {error && <Banner>{error}</Banner>}
      <div className="row gap8">
        <span className="caption muted">內文支援 Markdown</span>
        <span className="grow" />
        <button className="btn" onClick={() => onDone()}>取消</button>
        <button className="btn primary" disabled={!title.trim() || !REPO_RE.test(repo.trim())} onClick={create}>建立</button>
      </div>
    </div>
  );
}
