// 文件櫃結構編輯:區塊與頁籤可新增、改名、刪除(裡面還有文件時不能刪)(Swift 的 CabinetLayoutEditor)
import { useState } from "react";
import { action, useRun, useView } from "../../api";
import { Icon } from "../../components/Icon";
import type { CabinetDoc, CabinetSection, CabinetTab, OrgIndex } from "../../components/models";
import { ErrorText, Modal } from "../../components/ui";

const T = (path: string, label: string, code?: string, group?: string): CabinetTab => ({ path, label, ...(code ? { code } : {}), ...(group ? { group } : {}) });
const G = "做出自己的軟體";

/** 新的空白組織用的結構 */
export const BLANK_LAYOUT: CabinetSection[] = [{ id: "overview", label: "總覽", path: "", tabs: [] }, { id: "docs", label: "文件", path: "", tabs: [] }];

/** evoleap 研發基地的結構(預設組織) */
export const EVOLEAP_STUDIO: CabinetSection[] = [
  { id: "overview", label: "總覽", path: "/studio", tabs: [] },
  { id: "todo", label: "待辦", path: "/studio/todo", tabs: [] },
  { id: "members", label: "會員", path: "/studio/members", tabs: [] },
  { id: "levels", label: "課程", path: "/studio/tools", tabs: [
    T("/studio/tools", "AI 工具介紹", "L0", G), T("/studio/ai-tools", "AI 工具入門", "L1", G), T("/studio/vibe-coding", "Vibe Coding 入門", "L2", G),
    T("/studio/ai-agent", "AI Agent 整合應用", "L3", G), T("/studio/advanced", "進階", "L4", G), T("/studio/ai-tips", "AI 工具技巧"),
    T("/studio/kids-web", "兒童網頁課"), T("/studio/web-basics", "網頁運作知識"),
  ] },
  { id: "dev", label: "開發", path: "/studio/visuals", tabs: [
    T("/studio/visuals", "即時教學影像"), T("/studio/actor-interaction", "演員互動"), T("/studio/portrait", "直式舞台"), T("/studio/scenes", "場景"),
  ] },
  { id: "ideas", label: "構想", path: "/studio/mcp", tabs: [
    T("/studio/mcp", "課程 MCP"), T("/studio/live-script-mcp", "即時腳本 MCP"), T("/studio/member", "會員空間"), T("/studio/kids-family", "兒童課家長模式"),
    T("/studio/resources", "資源系統"), T("/studio/news-feed", "科技新知動態牆"), T("/studio/extensions", "延伸課程"), T("/studio/ops-course", "從上線到維運"),
  ] },
  { id: "docs", label: "文件", path: "/studio/docs", tabs: [
    T("/studio/docs", "全部文件"), T("/docs/course-goals", "課程目標"), T("/docs/research-report", "研究報告"), T("/docs/research/methodology", "課程設計方法論"),
    T("/docs/research/recording", "影音錄製實務"), T("/docs/research/video-shotcraft", "video-shotcraft"), T("/docs/research/hyperframes", "HyperFrames"),
    T("/docs/research/marketing", "軟體與課程行銷手法"), T("/docs/research/voice-cost", "旁白語音的費用評估"), T("/docs/research/tts-landscape", "文字轉語音工具現況"),
    T("/docs/research/script-template", "腳本範本結構"), T("/docs/live", "即時教學畫面規格"), T("/docs/recorder", "宣傳影片錄製工具"),
  ] },
  { id: "tools", label: "工具", path: "/materials", tabs: [] },
];

/** 檢查新結構:id 不重複,而且被拿掉的區塊/頁籤裡不能還有文件 */
export function validateLayout(sections: CabinetSection[], docs: CabinetDoc[]): string | undefined {
  const ids = new Set<string>();
  for (const s of sections) {
    if (!s.label.trim()) return "區塊名稱不能是空的";
    if (ids.has(s.id)) return `區塊 id 重複:${s.id}`;
    ids.add(s.id);
    for (const t of s.tabs) {
      if (!t.label.trim()) return `「${s.label}」有頁籤沒有名稱`;
      if (!t.path.startsWith("/")) return `頁籤 id 要用 / 開頭:${t.path}`;
      if (ids.has(t.path)) return `頁籤 id 重複:${t.path}`;
      ids.add(t.path);
    }
  }
  for (const d of docs) {
    const s = sections.find((x) => x.id === d.section);
    if (!s) return `「${d.title}」還在被拿掉的區塊裡,先搬走或刪除`;
    if (d.tab && !s.tabs.some((t) => t.path === d.tab)) return `「${d.title}」還在被拿掉的頁籤裡,先搬走或刪除`;
  }
  return undefined;
}

export function CabinetLayoutEditor({ layout, docs, onClose }: { layout: CabinetSection[]; docs: CabinetDoc[]; onClose: () => void }) {
  const [orgs] = useView<OrgIndex>("orgs");
  const [draft, setDraft] = useState<CabinetSection[]>(() => JSON.parse(JSON.stringify(layout)));
  const [err, setErr] = useState<string>();
  const [tplOpen, setTplOpen] = useState(false);
  const { busy, error, run } = useRun();
  const cur = orgs?.orgs.find((o) => o.id === orgs.current);
  const upd = (id: string, f: (s: CabinetSection) => CabinetSection) => setDraft((d) => d.map((s) => (s.id === id ? f(s) : s)));
  const newSectionId = () => { let n = draft.length + 1; while (draft.some((s) => s.id === `s${n}`)) n++; return `s${n}`; };
  const newTabId = (s: CabinetSection) => { let n = s.tabs.length + 1; const all = new Set(draft.flatMap((x) => x.tabs.map((t) => t.path))); while (all.has(`/${s.id}/${n}`)) n++; return `/${s.id}/${n}`; };
  const move = (id: string, dd: number) => setDraft((d) => {
    const i = d.findIndex((s) => s.id === id), j = i + dd;
    if (i < 0 || j < 0 || j >= d.length) return d;
    const n = [...d]; [n[i], n[j]] = [n[j], n[i]]; return n;
  });
  const save = () => {
    const e = validateLayout(draft, docs);
    if (e) { setErr(e); return; }
    setErr(undefined);
    run(async () => { await action("cabinet_layout_save", { layout: draft }); onClose(); });
  };
  return (
    <Modal open onClose={onClose} width={640}>
      <span className="modal-title">文件櫃結構({cur?.name ?? ""})</span>
      <span className="caption muted">區塊是第一層,頁籤是區塊底下的分類。角色的文件存取範圍以區塊或頁籤為單位。</span>
      <div className="col gap8" style={{ minHeight: 380, maxHeight: "50vh", overflowY: "auto", border: "1px solid var(--line)", borderRadius: 8, padding: 8 }}>
        {draft.map((s) => (
          <div key={s.id} className="col gap4" style={{ paddingBottom: 8, borderBottom: "1px solid var(--line)" }}>
            <div className="row gap6">
              <Icon name="folder" size={13} />
              <input className="input grow" style={{ fontWeight: 600 }} placeholder="區塊名稱" value={s.label} onChange={(e) => upd(s.id, (x) => ({ ...x, label: e.target.value }))} />
              <span className="mono caption muted">{s.id}</span>
              <button className="bx-icon-btn" onClick={() => move(s.id, -1)}><Icon name="arrow.up" size={12} /></button>
              <button className="bx-icon-btn" onClick={() => move(s.id, 1)}><Icon name="arrow.down" size={12} /></button>
              <button className="bx-icon-btn" onClick={() => setDraft((d) => d.filter((x) => x.id !== s.id))}><Icon name="trash" size={12} color="var(--red)" /></button>
            </div>
            {s.tabs.map((t) => (
              <div key={t.path} className="row gap6" style={{ paddingLeft: 20 }}>
                <Icon name="doc.text" size={12} className="muted" />
                <input className="input grow" placeholder="頁籤名稱" value={t.label} onChange={(e) => upd(s.id, (x) => ({ ...x, tabs: x.tabs.map((y) => (y.path === t.path ? { ...y, label: e.target.value } : y)) }))} />
                <span className="mono caption muted ellipsis" style={{ maxWidth: 180 }}>{t.path}</span>
                <button className="bx-icon-btn" onClick={() => upd(s.id, (x) => ({ ...x, tabs: x.tabs.filter((y) => y.path !== t.path) }))}><Icon name="minus.circle" size={12} color="var(--red)" /></button>
              </div>
            ))}
            <button className="btn link" style={{ alignSelf: "flex-start", marginLeft: 16 }} onClick={() => upd(s.id, (x) => ({ ...x, tabs: [...x.tabs, { path: newTabId(x), label: "新頁籤" }] }))}><Icon name="plus" size={11} />新增頁籤</button>
          </div>
        ))}
      </div>
      {err && <span style={{ color: "var(--orange)" }}>{err}</span>}
      <ErrorText>{error}</ErrorText>
      <div className="row gap8" style={{ position: "relative" }}>
        <button className="btn" onClick={() => setDraft((d) => [...d, { id: newSectionId(), label: "新區塊", path: "", tabs: [] }])}><Icon name="folder.badge.plus" size={12} />新增區塊</button>
        <button className="btn" onClick={() => setTplOpen(!tplOpen)}>套用範本 <Icon name="chevron.down" size={10} /></button>
        {tplOpen && (
          <div className="bx-menu" style={{ left: 110, bottom: 32 }} onMouseLeave={() => setTplOpen(false)}>
            <button className="menu-item" onClick={() => { setDraft(JSON.parse(JSON.stringify(EVOLEAP_STUDIO))); setTplOpen(false); }}>evoleap 研發基地</button>
            <button className="menu-item" onClick={() => { setDraft(JSON.parse(JSON.stringify(BLANK_LAYOUT))); setTplOpen(false); }}>空白(總覽、文件)</button>
          </div>
        )}
        <div className="grow" />
        <button className="btn" onClick={onClose}>取消</button>
        <button className="btn primary" disabled={busy} onClick={save}>儲存</button>
      </div>
    </Modal>
  );
}
