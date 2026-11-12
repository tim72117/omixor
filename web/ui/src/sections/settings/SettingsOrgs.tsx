// 設定 › 組織:新增、切換、改名、刪除組織,以及目前組織工作空間的各項設定入口(Swift 的 SettingsOrgsPane)
import { useEffect, useState } from "react";
import { action, useRun, useView } from "../../api";
import { go } from "../../components/bits";
import { Icon } from "../../components/Icon";
import type { AgentsView, CabinetView, ConnectionsView, Organization, OrgIndex } from "../../components/models";
import { ErrorText, Modal, Pill, color } from "../../components/ui";
import { CabinetLayoutEditor } from "../cabinet/CabinetLayoutEditor";
import { Form, Row, Section } from "./form";
import type { Pane } from "./SettingsView";

const SYMBOLS = ["building.2", "graduationcap", "briefcase", "storefront", "hammer", "flask", "globe", "heart", "leaf", "star"];
const COLORS = ["blue", "teal", "green", "orange", "pink", "purple", "indigo", "gray"];
const TEMPLATES = [
  { id: "blank", label: "全新空白", detail: "沒有角色、沒有連線;文件櫃只有「總覽」「文件」兩個區塊,之後再自己設定。" },
  { id: "copyCurrent", label: "複製目前組織的設定", detail: "複製角色(含角色設定、例行任務、文件存取範圍)、文件櫃結構、連線設定、追蹤的 GitHub repo 與 GCP 專案設定;不複製 Issue、任務、文件,連線要重新登入。" },
];

export function OrgBadge({ org, size = 24 }: { org: Pick<Organization, "symbol" | "color">; size?: number }) {
  return (
    <span style={{ width: size, height: size, borderRadius: size * 0.25, background: color(org.color), display: "inline-flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
      <Icon name={org.symbol} size={size * 0.5} color="white" />
    </span>
  );
}

export function SettingsOrgs({ setPane }: { setPane: (p: Pane) => void }) {
  const [idx, idxErr] = useView<OrgIndex>("orgs");
  const [agents] = useView<AgentsView>("agents", ["orgs"]);
  const [cab] = useView<CabinetView>("cabinet", ["orgs"]);
  const [conns] = useView<ConnectionsView>("connections", ["orgs"]);
  const [issues] = useView<{ status: string }[]>("issues", ["orgs"]);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<Organization>();
  const [deleting, setDeleting] = useState<Organization>();
  const [editingLayout, setEditingLayout] = useState(false);
  const { busy, error, run } = useRun();
  const orgs = idx?.orgs ?? [];
  const cur = orgs.find((o) => o.id === idx?.current);
  const layout = Array.isArray(cab?.layout) ? cab!.layout : [];
  const n = (v: unknown[] | undefined) => (Array.isArray(v) ? v.length : "—");
  const setup = (title: string, icon: string, detail: string, onClick: () => void) => (
    <Row label={<span className="row gap6"><Icon name={icon} size={13} />{title}</span>}>
      <span className="caption muted">{detail}</span>
      <button className="btn" onClick={onClick}>設定…</button>
    </Row>
  );
  return (
    <Form>
      <Section header="組織" footer="每個組織有自己的 Issue、虛擬角色與任務、文件櫃與結構、連線(登入也分開)。旁白批次與畫面上的範例資料是共用的。">
        {idxErr && <Row><ErrorText>{idxErr}</ErrorText></Row>}
        {orgs.map((o) => (
          <Row key={o.id}>
            <OrgBadge org={o} size={28} />
            <div className="col grow">
              <span className="row gap6"><span style={{ fontWeight: 600 }}>{o.name}</span>{o.id === idx?.current && <Pill text="使用中" color="green" />}</span>
              <span className="caption muted">{o.note || `id:${o.id}`}</span>
            </div>
            {o.id !== idx?.current && <button className="btn" disabled={busy} onClick={() => run(() => action("org_switch", { id: o.id }))}>切換</button>}
            <button className="btn" onClick={() => setEditing(o)}>編輯…</button>
            {o.id !== idx?.current && <button className="bx-icon-btn" title="刪除" onClick={() => setDeleting(o)}><Icon name="trash" size={13} color="var(--red)" /></button>}
          </Row>
        ))}
        <Row><button className="btn link" onClick={() => setCreating(true)}><Icon name="plus" size={12} />新增組織…</button></Row>
      </Section>
      {error && <ErrorText>{error}</ErrorText>}
      <Section header={`「${cur?.name ?? ""}」的工作空間`}>
        {setup("虛擬角色", "person.2", `${n(agents?.agents)} 個角色;角色設定、例行任務、文件存取範圍`, () => go("automation", "agents"))}
        {setup("文件櫃結構", "archivebox", cab ? `${layout.length} 個區塊、${layout.reduce((s, x) => s + x.tabs.length, 0)} 個頁籤` : "—", () => setEditingLayout(true))}
        {setup("連線", "key.horizontal", `${n(conns?.connections)} 個連線;外部服務的網址與登入`, () => setPane("connections"))}
        {setup("Issue", "exclamationmark.bubble", Array.isArray(issues) ? `${issues.filter((i) => i.status === "open").length} 件未解決` : "—", () => go("development", "issues"))}
      </Section>
      {creating && <NewOrgSheet onClose={() => setCreating(false)} />}
      {editing && <EditOrgSheet original={editing} onClose={() => setEditing(undefined)} />}
      {editingLayout && <CabinetLayoutEditor layout={layout} docs={Array.isArray(cab?.docs) ? cab!.docs : []} onClose={() => setEditingLayout(false)} />}
      <Modal open={!!deleting} onClose={() => setDeleting(undefined)} width={440} title={`刪除組織「${deleting?.name ?? ""}」?`}>
        <span className="muted">這個組織的 Issue、角色、任務、文件與連線設定會一起移到垃圾桶,鑰匙圈裡的登入會刪除。</span>
        <div className="row gap8" style={{ justifyContent: "flex-end" }}>
          <button className="btn" onClick={() => setDeleting(undefined)}>取消</button>
          <button className="btn red" disabled={busy} onClick={() => run(async () => { await action("org_delete", { id: deleting!.id }); setDeleting(undefined); })}>刪除(資料夾移到垃圾桶)</button>
        </div>
      </Modal>
    </Form>
  );
}

function SymbolColorPickers({ symbol, colorName, onSymbol, onColor }: { symbol: string; colorName: string; onSymbol: (s: string) => void; onColor: (c: string) => void }) {
  return (
    <>
      <div className="bx-field"><span>圖示</span>
        <div className="row gap4 wrap">{SYMBOLS.map((s) => (
          <button key={s} className="btn" style={{ padding: 5, borderColor: symbol === s ? "var(--accent)" : undefined, background: symbol === s ? "color-mix(in srgb, var(--accent) 14%, transparent)" : undefined }} onClick={() => onSymbol(s)}><Icon name={s} size={15} /></button>
        ))}</div>
      </div>
      <div className="bx-field"><span>顏色</span>
        <div className="row gap6">{COLORS.map((c) => (
          <button key={c} title={c} onClick={() => onColor(c)} style={{ width: 20, height: 20, borderRadius: 20, background: color(c), border: colorName === c ? "2px solid var(--fg)" : "2px solid transparent" }} />
        ))}</div>
      </div>
    </>
  );
}

function NewOrgSheet({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState("");
  const [symbol, setSymbol] = useState("building.2");
  const [col, setCol] = useState("indigo");
  const [template, setTemplate] = useState("blank");
  const [switchNow, setSwitchNow] = useState(true);
  const { busy, error, run } = useRun();
  const create = () => run(async () => {
    await action("org_create", { name: name.trim(), symbol, color: col, template, switch: switchNow });
    onClose();
  });
  return (
    <Modal open onClose={onClose} width={520}>
      <span className="modal-title">新增組織</span>
      <label className="bx-field"><span>名稱</span><input className="input" autoFocus placeholder="例如 新產品實驗室" value={name} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === "Enter" && name.trim() && create()} /></label>
      <SymbolColorPickers symbol={symbol} colorName={col} onSymbol={setSymbol} onColor={setCol} />
      <div className="bx-field"><span>起始設定</span>
        {TEMPLATES.map((t) => <label key={t.id} className="row gap6"><input type="radio" checked={template === t.id} onChange={() => setTemplate(t.id)} />{t.label}</label>)}
        <span className="caption muted">{TEMPLATES.find((t) => t.id === template)?.detail}</span>
      </div>
      <label className="row gap6"><input type="checkbox" checked={switchNow} onChange={(e) => setSwitchNow(e.target.checked)} />建立後切換過去</label>
      <ErrorText>{error}</ErrorText>
      <div className="row gap8" style={{ justifyContent: "flex-end" }}>
        <button className="btn" onClick={onClose}>取消</button>
        <button className="btn primary" disabled={!name.trim() || busy} onClick={create}>建立</button>
      </div>
    </Modal>
  );
}

function EditOrgSheet({ original, onClose }: { original: Organization; onClose: () => void }) {
  const [draft, setDraft] = useState(original);
  useEffect(() => setDraft(original), [original]);
  const { busy, error, run } = useRun();
  return (
    <Modal open onClose={onClose} width={480}>
      <span className="modal-title">編輯組織</span>
      <label className="bx-field"><span>名稱</span><input className="input" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} /></label>
      <label className="bx-field"><span>說明</span><input className="input" placeholder="選填" value={draft.note} onChange={(e) => setDraft({ ...draft, note: e.target.value })} /></label>
      <SymbolColorPickers symbol={draft.symbol} colorName={draft.color} onSymbol={(symbol) => setDraft({ ...draft, symbol })} onColor={(c) => setDraft({ ...draft, color: c })} />
      <div className="row gap8"><span className="caption muted">id</span><span className="mono">{draft.id}</span></div>
      <ErrorText>{error}</ErrorText>
      <div className="row gap8" style={{ justifyContent: "flex-end" }}>
        <button className="btn" onClick={onClose}>取消</button>
        <button className="btn primary" disabled={!draft.name.trim() || busy} onClick={() => run(async () => { await action("org_update", draft as unknown as Record<string, unknown>); onClose(); })}>儲存</button>
      </div>
    </Modal>
  );
}
