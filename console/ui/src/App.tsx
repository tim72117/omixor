import { Suspense, useEffect, useRef, useState } from "react";
import { action, useView } from "./api";
import { AssistantPanel } from "./assistant/AssistantPanel";
import { CommandPalette } from "./assistant/CommandPalette";
import { useAssistantVisible } from "./assistant/store";
import { AccountBox } from "./auth/AccountBox";
import s from "./App.module.css";
import { Icon } from "./components/Icon";
import { IconButton } from "./components/IconButton";
import { Dropdown, MenuList } from "./components/Menu";
import { Segmented } from "./components/Segmented";
import { color } from "./lib/color";
import { clearFocus, go, goBack, guardedNavigate, useNavSync } from "./lib/nav";
import { DirectoryProvider } from "./lib/directory";
import { useFeatures, useFeaturesError, useFeaturesSync, useLocalNotice, useTimedNotice } from "./features";
import { openOffice, useOfficeAvailable } from "./officeLink";
import { SECTION_VIEWS } from "./sections";
import { SECTIONS, type Nav, type SectionId } from "./types";
import { StatusDot } from "./components/StatusDot";

/** 只有這些區塊會讀工具列的環境(正式、預備、開發);其他區塊(Issue、文件櫃、自動化…)的資料屬於組織,和環境無關,不顯示切換 */
const ENV_SECTIONS: SectionId[] = ["overview", "pipelines", "infrastructure"];
/** 只有這些區塊會讀工具列的時間範圍 */
const RANGE_SECTIONS: SectionId[] = ["overview", "infrastructure", "monitoring"];

type OrgIndex = { current: string; orgs: { id: string; name: string; symbol: string; color: string; note: string }[] };

/** 側邊欄徽章:各功能可以登記 view「badges」回傳 {section: 數字} */
function useBadges(): Record<string, number> {
  const [b] = useView<Record<string, number>>("badges", ["alerts", "incidents", "approvals", "issues", "agents", "pipeline_runs", "voice_jobs"], { throttleMs: 5000 });
  return b ?? {};
}

export function App() {
  const [nav] = useView<Nav>("nav");
  const badges = useBadges();
  useFeaturesSync();
  const flags = useFeatures();
  // 這個分頁的所在頁以網址為準(lib/nav.ts);網址沒有路由時 useNavSync 會採用後端的目前頁
  const { route, canGoBack } = useNavSync(nav);
  // 系統設定關掉的頁面不顯示(useNavSync 會把網址換成總覽;這裡先照總覽畫,不閃一下)
  const asked = (route?.section ?? "overview") as SectionId;
  const section: SectionId = SECTION_VIEWS[asked] && flags.visible(asked) ? asked : "overview";
  const View = SECTION_VIEWS[section];
  const [showAssistantPref, setShowAssistant] = useAssistantVisible();
  // AI 助理是實驗功能(系統設定的 assistant):關掉時面板、右上角按鈕與 ⌘J 都不提供
  const assistantOn = flags.enabled("assistant");
  const showAssistant = assistantOn && showAssistantPref;
  const [palette, setPalette] = useState(false);
  const officeOn = useOfficeAvailable();

  // ⌘K 指令面板、⌘J 顯示/隱藏 AI 助理
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.shiftKey || e.altKey) return;
      const k = e.key.toLowerCase();
      if (k === "k") { e.preventDefault(); setPalette((p) => !p); }
      else if (k === "j" && assistantOn) { e.preventDefault(); setShowAssistant(!showAssistant); }
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [showAssistant, setShowAssistant, assistantOn]);

  return (
    <DirectoryProvider>
    <div className="app">
      <aside className="sidebar">
        <OrgSwitcher />
        {(["main", "auto", "end"] as const).map((g) => (
          <div key={g} className="side-group">
            {g === "main" && <div className="side-label">維運</div>}
            {g === "auto" && <div className="side-label">自動化</div>}
            {SECTIONS.filter((s) => s.group === g && flags.visible(s.id)).map((s) => (
              <button key={s.id} className={"side-item" + (s.id === section ? " on" : "")} onClick={() => go(s.id)}>
                <Icon name={s.icon} size={15} />
                <span>{s.title}</span>
                {badges[s.id] ? <span className="side-badge">{badges[s.id]}</span> : null}
              </button>
            ))}
          </div>
        ))}
        <div className={s.spacer} />
        <AccountBox />
        <McpStatus />
      </aside>
      <main className="main">
        <header className={"toolbar " + s.toolbar}>
          {canGoBack && <button className={"btn link " + s.back} title="回到上一個畫面" onClick={goBack}><Icon name="chevron.left" size={14} />返回</button>}
          {ENV_SECTIONS.includes(section) && <Segmented
            value={nav?.env ?? "production"}
            options={[{ value: "production", label: "Production" }, { value: "staging", label: "Staging" }, { value: "development", label: "Development" }]}
            onChange={(v) => action("navigate", { section, focusId: route?.focusId ?? "", env: v })}
          />}
          {RANGE_SECTIONS.includes(section) && <select className="select" value={nav?.timeRange ?? "24h"} onChange={(e) => action("navigate", { section, focusId: route?.focusId ?? "", timeRange: e.target.value })}>
            <option value="1h">1 小時</option>
            <option value="6h">6 小時</option>
            <option value="24h">24 小時</option>
            <option value="7d">7 天</option>
            <option value="30d">30 天</option>
          </select>}
          <div className={"row gap4 " + s.toolbarRight}>
            <button className="btn link" title="搜尋頁面、服務、伺服器、事件(⌘K)" onClick={() => setPalette(true)}><Icon name="magnifyingglass" size={14} /></button>
            {officeOn && <button className="btn link" title="3D 辦公室(新分頁)" onClick={openOffice}><Icon name="building.2" size={14} /></button>}
            {assistantOn && <button className="btn link" title={showAssistant ? "隱藏 AI 助理(⌘J)" : "顯示 AI 助理(⌘J)"} onClick={() => setShowAssistant(!showAssistant)}>
              <Icon name={showAssistant ? "sidebar.trailing" : "sparkles"} size={14} />
            </button>}
          </div>
        </header>
        <FeaturesLoadError />
        <FeatureNotice />
        <div className="content">
          <Suspense fallback={<div className={"muted " + s.loading}>載入中…</div>}>
            <View key={section} focusId={route?.focusId} clearFocus={clearFocus} />
          </Suspense>
        </div>
      </main>
      {showAssistant && <AssistantPanel />}
      {palette && <CommandPalette onClose={() => setPalette(false)} />}
    </div>
    </DirectoryProvider>
  );
}

/** 讀不到功能開關(view「system」)時一直顯示:讀到過就照最後一份,從來沒讀到就用保守預設(實驗功能關) */
function FeaturesLoadError() {
  const err = useFeaturesError();
  const flags = useFeatures();
  if (!err) return null;
  return (
    <div className={"feature-notice " + s.warn}>
      <Icon name="exclamationmark.triangle" size={14} />
      <span>讀不到功能開關:{err}。{flags.view ? "目前照最後一次讀到的設定顯示。" : "目前用保守預設(實驗功能關)。"}</span>
    </div>
  );
}

/** 跳到關掉的功能、所在頁被關掉時,畫面上方的提示(5 秒後自動消失;同 App 的 FeatureNoticeBanner) */
function FeatureNotice() {
  const flags = useFeatures();
  const server = flags.view?.notice;
  // 第一次讀到時已經有的提示是之前的,不顯示
  const base = useRef<number>();
  if (server && base.current === undefined) base.current = server.seq;
  const fresh = server && base.current !== undefined && server.seq > base.current ? server : undefined;
  const [s, closeS] = useTimedNotice(fresh?.seq ?? 0, fresh?.text ?? "");
  const local = useLocalNotice();
  const [l, closeL] = useTimedNotice(local.seq, local.text);
  const text = l ?? s;
  if (!text) return null;
  const close = () => { closeS(); closeL(); };
  return (
    <div className="feature-notice">
      <Icon name="switch.2" size={14} />
      <span>{text}</span>
      <button className="btn link" onClick={() => { close(); void guardedNavigate("settings", "features"); }}>前往 設定 › 功能</button>
      <IconButton icon="xmark" size={12} title="關閉" onClick={close} />
    </div>
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
        <Dropdown className={s.orgMenu} onMouseLeave={() => setOpen(false)}>
          <MenuList checkColumn={false} close={() => setOpen(false)} items={[
            ...(idx?.orgs ?? []).map((o) => ({
              label: <><Icon name={o.symbol} size={14} color={color(o.color)} /> {o.name} {o.id === idx?.current && <span className="muted">✓</span>}</>,
              onClick: () => { if (o.id !== idx?.current) action("org_switch", { id: o.id }); },
            })),
            "sep",
            { label: <><Icon name="gearshape" size={14} /> 管理組織…</>, onClick: () => go("settings", "orgs") },
          ]} />
        </Dropdown>
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
      <StatusDot c={ok ? "green" : "orange"} size={7} />
      <span className="caption">MCP {ok ? m.url.replace("http://", "") : m.status.error ?? m.status.state}</span>
    </div>
  );
}
