// ⌘K 指令面板:跳到頁面、服務、伺服器、事件、AI 自動化(Swift 的 CommandPalette)
import { useEffect, useRef, useState } from "react";
import { useView } from "../api";
import { go } from "../components/bits";
import { Icon } from "../components/Icon";
import { arr, type Automation, type Incident, type Server, type Service } from "../components/models";
import { useFeatures } from "../features";
import { SECTIONS, type SectionId } from "../types";

// feature:這個項目所在的分頁(系統設定關掉的不列;所屬頁面關了也不列)
type Entry = { id: string; title: string; detail: string; icon: string; section: SectionId; focus?: string; feature?: string };

export function CommandPalette({ onClose }: { onClose: () => void }) {
  const [services] = useView<Service[]>("services");
  const [servers] = useView<Server[]>("servers");
  const [incidents] = useView<Incident[]>("incidents");
  const [autos] = useView<Automation[]>("automations");
  const [query, setQuery] = useState("");
  const [sel, setSel] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  const flags = useFeatures();
  const all: Entry[] = ([
    ...SECTIONS.map((s) => ({ id: `s-${s.id}`, title: s.title, detail: "頁面", icon: s.icon, section: s.id })),
    ...arr<Service>(services).map((s) => ({ id: `svc-${s.id}`, title: s.name, detail: "服務", icon: "shippingbox", section: "infrastructure" as SectionId, focus: s.id, feature: "infrastructure.services" })),
    ...arr<Server>(servers).map((s) => ({ id: `srv-${s.id}`, title: s.name, detail: `伺服器 · ${s.region}`, icon: "server.rack", section: "infrastructure" as SectionId, focus: s.id, feature: "infrastructure.servers" })),
    ...arr<Incident>(incidents).map((i) => ({ id: `inc-${i.id}`, title: `${i.id} ${i.title}`, detail: "事件", icon: "flame", section: "incidents" as SectionId, focus: i.id, feature: "incidents.list" })),
    ...arr<Automation>(autos).map((a) => ({ id: `ai-${a.id}`, title: a.name, detail: "AI 自動化", icon: a.icon, section: "automation" as SectionId, focus: a.id, feature: "automation.flows" })),
  ] as Entry[]).filter((e) => flags.visible(e.section) && (!e.feature || flags.enabled(e.feature)));
  const q = query.trim().toLowerCase();
  const entries = (q ? all.filter((e) => e.title.toLowerCase().includes(q) || e.detail.toLowerCase().includes(q)) : all).slice(0, 40);
  useEffect(() => setSel(0), [query]);
  useEffect(() => { listRef.current?.querySelector(".palette-item.on")?.scrollIntoView({ block: "nearest" }); }, [sel]);
  const run = (e?: Entry) => { if (!e) return; go(e.section, e.focus); onClose(); };
  return (
    <div className="modal-bg" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal palette" onKeyDown={(e) => {
        if (e.key === "Escape") onClose();
        else if (e.key === "ArrowDown") { e.preventDefault(); setSel((s) => Math.min(entries.length - 1, s + 1)); }
        else if (e.key === "ArrowUp") { e.preventDefault(); setSel((s) => Math.max(0, s - 1)); }
        else if (e.key === "Enter" && !e.nativeEvent.isComposing) run(entries[sel]);
      }}>
        <div className="palette-input">
          <Icon name="magnifyingglass" size={16} className="muted" />
          <input autoFocus value={query} placeholder="搜尋頁面、服務、伺服器、事件…" onChange={(e) => setQuery(e.target.value)} />
        </div>
        <div className="palette-list" ref={listRef}>
          {entries.map((e, i) => (
            <button key={e.id} className={"palette-item" + (i === sel ? " on" : "")} onMouseEnter={() => setSel(i)} onClick={() => run(e)}>
              <span style={{ width: 20, display: "inline-flex", justifyContent: "center" }}><Icon name={e.icon} size={14} /></span>
              <span className="grow ellipsis">{e.title}</span>
              <span className="caption muted">{e.detail}</span>
            </button>
          ))}
          {entries.length === 0 && <div className="muted" style={{ padding: 16 }}>找不到符合的項目</div>}
        </div>
      </div>
    </div>
  );
}
