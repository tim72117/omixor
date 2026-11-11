import { Suspense, useEffect, useState } from "react";
import { action, useView } from "./api";
import { AssistantPanel } from "./assistant/AssistantPanel";
import { CommandPalette } from "./assistant/CommandPalette";
import { useAssistantVisible } from "./assistant/store";
import { UserMenu } from "./auth/UserMenu";
import { DirectoryProvider } from "./components/bits";
import { Icon } from "./components/Icon";
import { color, Segmented } from "./components/ui";
import { SECTION_VIEWS } from "./sections";
import { SECTIONS, type Nav, type SectionId } from "./types";

type OrgIndex = { current: string; orgs: { id: string; name: string; symbol: string; color: string; note: string }[] };

/** 側邊欄徽章:各功能可以登記 view「badges」回傳 {section: 數字} */
function useBadges(): Record<string, number> {
  const [b] = useView<Record<string, number>>("badges", ["alerts", "incidents", "approvals", "issues", "agents", "pipeline_runs", "voice_jobs"], { throttleMs: 5000 });
  return b ?? {};
}

export function App() {
  const [nav] = useView<Nav>("nav");
  const badges = useBadges();
  const section: SectionId = nav?.section ?? "overview";
  const View = SECTION_VIEWS[section];
  const go = (s: SectionId) => action("navigate", { section: s });
  const [showAssistant, setShowAssistant] = useAssistantVisible();
  const [palette, setPalette] = useState(false);

  // ⌘K 指令面板、⌘J 顯示/隱藏 AI 助理
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.shiftKey || e.altKey) return;
      const k = e.key.toLowerCase();
      if (k === "k") { e.preventDefault(); setPalette((p) => !p); }
      else if (k === "j") { e.preventDefault(); setShowAssistant(!showAssistant); }
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [showAssistant, setShowAssistant]);

  return (
    <DirectoryProvider>
    <div className="app">
      <aside className="sidebar">
        <OrgSwitcher />
        {(["main", "auto", "end"] as const).map((g) => (
          <div key={g} className="side-group">
            {g === "main" && <div className="side-label">維運</div>}
            {g === "auto" && <div className="side-label">自動化</div>}
            {SECTIONS.filter((s) => s.group === g).map((s) => (
              <button key={s.id} className={"side-item" + (s.id === section ? " on" : "")} onClick={() => go(s.id)}>
                <Icon name={s.icon} size={15} />
                <span>{s.title}</span>
                {badges[s.id] ? <span className="side-badge">{badges[s.id]}</span> : null}
              </button>
            ))}
          </div>
        ))}
        <div style={{ flex: 1 }} />
        <UserMenu />
        <McpStatus />
      </aside>
      <main className="main">
        <header className="toolbar" style={{ position: "relative" }}>
          <Segmented
            value={nav?.env ?? "production"}
            options={[{ value: "production", label: "Production" }, { value: "staging", label: "Staging" }, { value: "development", label: "Development" }]}
            onChange={(v) => action("navigate", { section, focusId: nav?.focusId ?? "", env: v })}
          />
          <select className="select" value={nav?.timeRange ?? "24h"} onChange={(e) => action("navigate", { section, focusId: nav?.focusId ?? "", timeRange: e.target.value })}>
            <option value="1h">1 小時</option>
            <option value="6h">6 小時</option>
            <option value="24h">24 小時</option>
            <option value="7d">7 天</option>
            <option value="30d">30 天</option>
          </select>
          <div className="row gap4" style={{ position: "absolute", right: 12 }}>
            <button className="btn link" title="搜尋頁面、服務、伺服器、事件(⌘K)" onClick={() => setPalette(true)}><Icon name="magnifyingglass" size={14} /></button>
            <button className="btn link" title={showAssistant ? "隱藏 AI 助理(⌘J)" : "顯示 AI 助理(⌘J)"} onClick={() => setShowAssistant(!showAssistant)}>
              <Icon name={showAssistant ? "sidebar.trailing" : "sparkles"} size={14} />
            </button>
          </div>
        </header>
        <div className="content">
          <Suspense fallback={<div className="muted" style={{ padding: 20 }}>載入中…</div>}>
            <View key={section} focusId={nav?.focusId || undefined} clearFocus={() => action("navigate", { section, focusId: "" })} />
          </Suspense>
        </div>
      </main>
      {showAssistant && <AssistantPanel />}
      {palette && <CommandPalette onClose={() => setPalette(false)} />}
    </div>
    </DirectoryProvider>
  );
}

function OrgSwitcher() {
  const [idx] = useView<OrgIndex>("orgs");
  const [open, setOpen] = useState(false);
  const cur = idx?.orgs.find((o) => o.id === idx.current);
  return (
    <div className="org-switcher">
      <button className="org-current" onClick={() => setOpen(!open)}>
        <Icon name={cur?.symbol ?? "building.2"} size={15} color={color(cur?.color)} />
        <span>{cur?.name ?? "…"}</span>
        <Icon name="chevron.down" size={12} className="muted" />
      </button>
      {open && (
        <div className="menu" onMouseLeave={() => setOpen(false)}>
          {idx?.orgs.map((o) => (
            <button key={o.id} className="menu-item" onClick={() => { setOpen(false); if (o.id !== idx.current) action("org_switch", { id: o.id }); }}>
              <Icon name={o.symbol} size={14} color={color(o.color)} /> {o.name} {o.id === idx.current && <span className="muted">✓</span>}
            </button>
          ))}
          <div className="menu-sep" />
          <button className="menu-item" onClick={() => { setOpen(false); action("navigate", { section: "settings", focusId: "orgs" }); }}>
            <Icon name="gearshape" size={14} /> 管理組織…
          </button>
        </div>
      )}
    </div>
  );
}

function McpStatus() {
  const [m] = useView<{ status: { state: string; error?: string; requests: number }; url: string }>("mcp");
  if (!m) return null;
  const ok = m.status.state === "listening";
  return (
    <div className="mcp-status" title={m.status.error ?? m.url}>
      <span className="dot" style={{ background: ok ? "var(--green)" : "var(--orange)" }} />
      <span className="caption">MCP {ok ? m.url.replace("http://", "") : m.status.error ?? m.status.state}</span>
    </div>
  );
}
