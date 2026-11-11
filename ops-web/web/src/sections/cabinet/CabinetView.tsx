// 文件櫃:依 evoleap 研發基地的區塊/頁籤放純文字文件;每個位置標出哪些角色可以讀寫(Swift 的 CabinetView)
import { useEffect, useState } from "react";
import { action, tool, useRun, useView } from "../../api";
import { AgentAvatar, AuthorIcon, Markdown, SearchField, useDirectory } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { DOC_ACCESS_LABEL, agentAccess, tabTitle, type Agent, type AgentsView, type CabinetDoc, type CabinetSection, type CabinetView as CabinetData, type DocAccess } from "../../components/models";
import { EmptyState, ErrorText, Modal, Pill } from "../../components/ui";
import { relative } from "../../fmt";
import type { SectionProps } from "../index";
import { CabinetLayoutEditor } from "./CabinetLayoutEditor";

export default function CabinetView({ focusId, clearFocus }: SectionProps) {
  const [data, err] = useView<CabinetData>("cabinet", ["orgs"]);
  const [agentsView] = useView<AgentsView>("agents", ["orgs"]);
  const layout = Array.isArray(data?.layout) ? data!.layout : [];
  const docs = Array.isArray(data?.docs) ? data!.docs : [];
  const agents = Array.isArray(agentsView?.agents) ? agentsView!.agents : [];
  const [node, setNode] = useState("overview");
  const [selectedDoc, setSelectedDoc] = useState<string>();
  const [query, setQuery] = useState("");
  const [creating, setCreating] = useState(false);
  const [editingLayout, setEditingLayout] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const section = layout.find((s) => s.id === node) ?? layout.find((s) => s.tabs.some((t) => t.path === node));
  const tab = node.startsWith("/") ? section?.tabs.find((t) => t.path === node) : undefined;
  useEffect(() => { if (layout.length && !section) { setNode(layout[0].id); setSelectedDoc(undefined); } }, [layout.map((s) => s.id).join(","), section]);
  // 從其他頁跳過來時(例如 DOC-3)選中那份文件
  useEffect(() => {
    if (!focusId || !data) return;
    const d = docs.find((x) => x.id.toLowerCase() === focusId.toLowerCase());
    if (d) { setNode(d.tab ?? d.section); setSelectedDoc(d.id); if (d.tab) setExpanded((e) => new Set([...e, d.section])); }
    clearFocus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusId, data ? 1 : 0]);

  const readers = (s: string, t?: string): [Agent, DocAccess][] =>
    agents.flatMap((a) => { const acc = agentAccess(a, s, t); return acc ? [[a, acc] as [Agent, DocAccess]] : []; });
  const q = query.trim().toLowerCase();
  const list = docs.filter((d) => (tab ? d.tab === node : d.section === section?.id) && (!q || d.title.toLowerCase().includes(q) || d.body.toLowerCase().includes(q)))
    .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
  const sel = docs.find((d) => d.id === selectedDoc);

  if (err) return <EmptyState title="讀不到文件櫃" icon="exclamationmark.triangle" message={err} />;
  if (!data) return <div className="muted" style={{ padding: 20 }}>載入中…</div>;

  const nodeRow = (title: string, icon: string, count: number, rs: [Agent, DocAccess][], id: string, indent: number, toggle?: () => void, open?: boolean) => (
    <div key={id} className={"bx-list-row" + (node === id ? " on" : "")} style={{ border: 0, padding: "5px 10px", paddingLeft: 10 + indent, alignItems: "center", gap: 6, borderRadius: 6, margin: "0 6px" }}
      onClick={() => { setNode(id); setSelectedDoc(undefined); }}>
      {toggle ? (
        <button className="bx-icon-btn" style={{ padding: 0 }} onClick={(e) => { e.stopPropagation(); toggle(); }}>
          <span style={{ display: "inline-flex", transform: open ? "rotate(90deg)" : undefined, transition: "transform .15s" }}><Icon name="chevron.right" size={10} /></span>
        </button>
      ) : <span style={{ width: 16 }} />}
      <Icon name={icon} size={13} className="muted" />
      <span className="ellipsis grow">{title}</span>
      <span className="row" style={{ gap: 0 }}>
        {rs.slice(0, 4).map(([a, acc], i) => (
          <span key={a.id} title={`${a.name}:${DOC_ACCESS_LABEL[acc]}`} style={{ marginLeft: i ? -4 : 0, opacity: acc === "write" ? 1 : 0.45, display: "inline-flex" }}><AgentAvatar agent={a} size={14} /></span>
        ))}
      </span>
      {count > 0 && <span className="caption muted num">{count}</span>}
    </div>
  );

  return (
    <div className="split">
      <div className="split-list" style={{ width: 250, background: "var(--side)" }}>
        <div className="grow" style={{ overflowY: "auto", paddingTop: 6 }}>
          {layout.map((s) => {
            const open = expanded.has(s.id);
            const toggle = () => setExpanded((e) => { const n = new Set(e); if (n.has(s.id)) n.delete(s.id); else n.add(s.id); return n; });
            return (
              <div key={s.id}>
                {nodeRow(s.label, "folder", docs.filter((d) => d.section === s.id).length, readers(s.id), s.id, 0, s.tabs.length ? toggle : undefined, open)}
                {open && s.tabs.map((t) => nodeRow(tabTitle(t), "doc.text", docs.filter((d) => d.tab === t.path).length, readers(s.id, t.path), t.path, 18))}
              </div>
            );
          })}
        </div>
        <div style={{ padding: 8, borderTop: "1px solid var(--line)" }}>
          <button className="btn link" onClick={() => setEditingLayout(true)}><Icon name="slider.horizontal.3" size={12} />編輯結構</button>
        </div>
      </div>
      <div className="split-list" style={{ width: 300 }}>
        <div className="col gap8" style={{ padding: 10, borderBottom: "1px solid var(--line)" }}>
          <div className="row"><span className="h3 ellipsis grow" style={{ fontSize: 14 }}>{tab ? tabTitle(tab) : section?.label}</span>
            <button className="bx-icon-btn" title="新增文件" onClick={() => setCreating(true)}><Icon name="plus" size={14} /></button></div>
          <span className="mono caption muted">研發基地 {tab?.path ?? section?.path ?? ""}</span>
          {(() => {
            const rs = readers(section?.id ?? "", tab?.path);
            return rs.length ? <span className="caption muted">{rs.map(([a, acc]) => `${a.name}(${acc === "write" ? "讀寫" : "讀"})`).join("、")}</span>
              : <span className="caption muted">沒有角色可以存取這裡</span>;
          })()}
          <SearchField value={query} onChange={setQuery} placeholder="搜尋標題、內文" />
        </div>
        {list.length === 0 ? <EmptyState title="沒有文件" icon="doc" message="按 + 新增,或讓有權限的角色用 MCP 的 ops_doc_save 寫進來。" /> : list.map((d) => (
          <div key={d.id} className={"list-item" + (selectedDoc === d.id ? " on" : "")} onClick={() => setSelectedDoc(d.id)}>
            <span>{d.title}</span>
            <span className="row gap6 caption">
              <span className="mono muted">{d.id}</span><AuthorIcon author={d.author} size={14} />
              {!tab && d.tab && <span className="muted ellipsis">{(() => { const t = section?.tabs.find((x) => x.path === d.tab); return t ? tabTitle(t) : ""; })()}</span>}
              <span className="grow" /><span className="muted" style={{ flex: "none" }}>{relative(d.updatedAt)}</span>
            </span>
          </div>
        ))}
      </div>
      <div className="split-detail">
        {sel ? <DocDetail key={sel.id} doc={sel} layout={layout} onDeleted={() => setSelectedDoc(undefined)} />
          : <EmptyState title="選一份文件" icon="doc.text" message="文件櫃依區塊與頁籤分類(左下「編輯結構」可調整)。每個角色可以在「AI 自動化 › 虛擬角色 › 編輯」設定能讀寫哪些區塊。" />}
      </div>
      {creating && section && <NewDocSheet section={section} tab={tab} onClose={() => setCreating(false)} onCreated={setSelectedDoc} />}
      {editingLayout && <CabinetLayoutEditor layout={layout} docs={docs} onClose={() => setEditingLayout(false)} />}
    </div>
  );
}

export function docLocation(d: CabinetDoc, layout: CabinetSection[]): string {
  const s = layout.find((x) => x.id === d.section);
  const t = s?.tabs.find((x) => x.path === d.tab) ?? layout.flatMap((x) => x.tabs).find((x) => x.path === d.tab);
  const sl = s?.label ?? d.section;
  return t ? `${sl} › ${tabTitle(t)}` : sl;
}

function DocDetail({ doc, layout, onDeleted }: { doc: CabinetDoc; layout: CabinetSection[]; onDeleted: () => void }) {
  const dir = useDirectory();
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [confirmDel, setConfirmDel] = useState(false);
  const { busy, error, run } = useRun();
  const save = () => run(async () => { await tool("ops_doc_save", { id: doc.id, title, body }); setEditing(false); });
  const del = () => run(async () => { await action("cabinet_doc_delete", { id: doc.id }); onDeleted(); });
  return (
    <div className="page-inner" style={{ maxWidth: 820, gap: 12 }}>
      <div className="row gap8">
        {editing ? <input className="input grow" style={{ fontSize: 16 }} value={title} onChange={(e) => setTitle(e.target.value)} />
          : <span className="page-title grow" style={{ userSelect: "text" }}>{doc.title}</span>}
        {editing ? (
          <>
            <button className="btn" onClick={() => setEditing(false)}>取消</button>
            <button className="btn primary" disabled={!title.trim() || busy} onClick={save}>儲存</button>
          </>
        ) : (
          <>
            <button className="btn" onClick={() => { setTitle(doc.title); setBody(doc.body); setEditing(true); }}>編輯</button>
            <button className="btn danger" onClick={() => setConfirmDel(true)}>刪除</button>
          </>
        )}
      </div>
      <div className="row gap8 wrap">
        <Pill text={docLocation(doc, layout)} icon="folder" />
        {doc.tags.map((t) => <Pill key={t} text={t} icon="tag" />)}
        <AuthorIcon author={doc.author} size={16} />
        <span className="caption muted">{dir.authorName(doc.author)}</span>
        <span className="caption muted">{relative(doc.updatedAt)}</span>
      </div>
      <ErrorText>{error}</ErrorText>
      <div className="divider" />
      {editing ? <textarea className="textarea" style={{ minHeight: 360 }} value={body} onChange={(e) => setBody(e.target.value)} />
        : doc.body ? <Markdown text={doc.body} /> : <span className="muted">沒有內文</span>}
      <Modal open={confirmDel} onClose={() => setConfirmDel(false)} width={380} title={`刪除「${doc.title}」?`}>
        <div className="row gap8" style={{ justifyContent: "flex-end" }}>
          <button className="btn" onClick={() => setConfirmDel(false)}>取消</button>
          <button className="btn red" disabled={busy} onClick={del}>刪除</button>
        </div>
      </Modal>
    </div>
  );
}

function NewDocSheet({ section, tab, onClose, onCreated }: { section: CabinetSection; tab?: { path: string; label: string; code?: string | null }; onClose: () => void; onCreated: (id: string) => void }) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const { busy, error, run } = useRun();
  const create = () => run(async () => {
    const r = await tool<{ id?: string; doc?: { id?: string } }>("ops_doc_save", { section: section.id, ...(tab ? { tab: tab.path } : {}), title: title.trim(), body });
    const id = r?.id ?? r?.doc?.id;
    if (id) onCreated(id);
    onClose();
  });
  return (
    <Modal open onClose={onClose} width={600}>
      <span className="modal-title">新增文件到「{section.label}{tab ? ` › ${tabTitle(tab)}` : ""}」</span>
      <input className="input" autoFocus placeholder="標題" value={title} onChange={(e) => setTitle(e.target.value)} />
      <textarea className="textarea" style={{ minHeight: 260 }} value={body} onChange={(e) => setBody(e.target.value)} />
      <ErrorText>{error}</ErrorText>
      <div className="row gap8">
        <span className="caption muted grow">純文字,支援 Markdown</span>
        <button className="btn" onClick={onClose}>取消</button>
        <button className="btn primary" disabled={!title.trim() || busy} onClick={create}>建立</button>
      </div>
    </Modal>
  );
}
