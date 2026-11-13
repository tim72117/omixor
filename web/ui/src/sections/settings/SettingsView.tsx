// 設定:組織、認證、整合、程式碼倉庫、連線、團隊與值班、AI 治理、通知、一般(Swift 的 SettingsView)
// 打開哪個分頁記在 localStorage(PANE_KEY);其他頁用 navigate 帶 focusId = 分頁 id 跳過來,例如開發進度的「前往設定」帶 "repos"。
import { useEffect } from "react";
import { action, useView } from "../../api";
import { ActorAvatar, Switch, useDirectory, useStored } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { EmptyState, Segmented } from "../../components/ui";
import { useAssistantVisible } from "../../assistant/store";
import { useAuth } from "../../auth/auth";
import { AuthSettings } from "../../auth/AuthSettings";
import type { Nav } from "../../types";
import type { SectionProps } from "../index";
import { Form, Row, Section } from "./form";
import { SettingsAI } from "./SettingsAI";
import { SettingsConnections } from "./SettingsConnections";
import { SettingsIntegrations } from "./SettingsIntegrations";
import { SettingsNotifications } from "./SettingsNotifications";
import { SettingsOrgs } from "./SettingsOrgs";
import { SettingsRepos } from "./SettingsRepos";
import { SettingsTeam } from "./SettingsTeam";

export type Pane = "orgs" | "auth" | "integrations" | "repos" | "connections" | "team" | "ai" | "notifications" | "general";
const PANES: { id: Pane; title: string; icon: string }[] = [
  { id: "orgs", title: "組織", icon: "building.2" }, { id: "auth", title: "認證", icon: "lock" }, { id: "integrations", title: "整合", icon: "puzzlepiece.extension" },
  { id: "repos", title: "程式碼倉庫", icon: "chevron.left.forwardslash.chevron.right" },
  { id: "connections", title: "連線", icon: "key.horizontal" }, { id: "team", title: "團隊與值班", icon: "person.2" },
  { id: "ai", title: "AI 治理", icon: "sparkles" }, { id: "notifications", title: "通知", icon: "bell.badge" }, { id: "general", title: "一般", icon: "gearshape" },
];
export const PANE_KEY = "ops.settings.pane";

export default function SettingsView({ focusId, clearFocus }: SectionProps) {
  const [pane, setPane] = useStored<Pane>(PANE_KEY, "integrations");
  useEffect(() => {
    if (!focusId) return;
    if (PANES.some((p) => p.id === focusId)) setPane(focusId as Pane);
    clearFocus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusId]);
  return (
    <div className="col" style={{ flex: 1, minHeight: 0 }}>
      <div className="row" style={{ justifyContent: "center", padding: "12px 20px", borderBottom: "1px solid var(--line)" }}>
        <div style={{ maxWidth: 860, width: "100%", textAlign: "center" }}>
          <Segmented value={pane} onChange={setPane} options={PANES.map((p) => ({ value: p.id, label: <span className="row gap4" style={{ justifyContent: "center" }}><Icon name={p.icon} size={12} />{p.title}</span> }))} />
        </div>
      </div>
      <div className="col" style={{ flex: 1, minHeight: 0 }}>
        {pane === "orgs" && <SettingsOrgs setPane={setPane} />}
        {pane === "auth" && <AuthPane />}
        {pane === "integrations" && <SettingsIntegrations />}
        {pane === "repos" && <SettingsRepos />}
        {pane === "connections" && <SettingsConnections />}
        {pane === "team" && <SettingsTeam />}
        {pane === "ai" && <SettingsAI />}
        {pane === "notifications" && <SettingsNotifications setPane={setPane} />}
        {pane === "general" && <General />}
      </div>
    </div>
  );
}

/** 設定 › 認證:只有管理員能看 */
function AuthPane() {
  const auth = useAuth();
  if (!auth) return <div className="muted" style={{ padding: 20 }}>載入中…</div>;
  if (!auth.user?.admin) return <EmptyState title="只有管理員可以改認證設定" icon="lock" />;
  return (
    <div className="page-scroll">
      <div style={{ maxWidth: 760, margin: "0 auto", padding: 20 }}><AuthSettings /></div>
    </div>
  );
}

function General() {
  const [nav] = useView<Nav>("nav");
  const dir = useDirectory();
  const [showAssistant, setShowAssistant] = useAssistantVisible();
  const [refresh, setRefresh] = useStored("ops.settings.refreshSeconds", 15);
  const [pause, setPause] = useStored("ops.settings.pauseInBackground", true);
  const [tz, setTz] = useStored("ops.settings.timezone", "Asia/Taipei");
  const [h24, setH24] = useStored("ops.settings.use24h", true);
  const me = dir.person(dir.me);
  const reset = () => {
    if (!window.confirm("還原所有本機設定?\n主題、更新頻率、AI 治理上限與預算等設定會回到預設值。")) return;
    Object.keys(localStorage).filter((k) => k.startsWith("ops.settings.")).forEach((k) => localStorage.removeItem(k));
    location.reload();
  };
  return (
    <Form>
      <Section header="外觀">
        <Row label="主題"><span className="caption muted">跟隨系統</span></Row>
        <Row label="顯示 AI 助理面板"><Switch on={showAssistant} onChange={setShowAssistant} /></Row>
      </Section>
      <Section header="資料更新" footer="更新越頻繁,圖表越即時,但會向監控服務送出較多查詢。">
        <Row label="自動更新頻率">
          <select className="select" value={refresh} onChange={(e) => setRefresh(+e.target.value)}>
            {[[5, "每 5 秒"], [15, "每 15 秒"], [30, "每 30 秒"], [60, "每分鐘"], [0, "手動"]].map(([v, l]) => <option key={v} value={v}>{l}</option>)}
          </select>
        </Row>
        <Row label="視窗不在前景時暫停更新"><Switch on={pause} onChange={setPause} /></Row>
      </Section>
      <Section header="預設檢視">
        <Row label="環境">
          <select className="select" value={nav?.env ?? "production"} onChange={(e) => action("navigate", { section: "settings", env: e.target.value })}>
            <option value="production">Production</option><option value="staging">Staging</option><option value="development">Development</option>
          </select>
        </Row>
        <Row label="時間範圍">
          <select className="select" value={nav?.timeRange ?? "24h"} onChange={(e) => action("navigate", { section: "settings", timeRange: e.target.value })}>
            {[["1h", "1 小時"], ["6h", "6 小時"], ["24h", "24 小時"], ["7d", "7 天"], ["30d", "30 天"]].map(([v, l]) => <option key={v} value={v}>{l}</option>)}
          </select>
        </Row>
      </Section>
      <Section header="地區與格式">
        <Row label="時區">
          <select className="select" value={tz} onChange={(e) => setTz(e.target.value)}>
            {[["Asia/Taipei", "台北(UTC+8)"], ["UTC", "UTC"], ["Asia/Tokyo", "東京(UTC+9)"], ["America/Los_Angeles", "洛杉磯"]].map(([v, l]) => <option key={v} value={v}>{l}</option>)}
          </select>
        </Row>
        <Row label="使用 24 小時制"><Switch on={h24} onChange={setH24} /></Row>
        <Row label="語言"><span className="muted">繁體中文(台灣)</span></Row>
      </Section>
      <Section header="帳號">
        <Row label="登入身分"><span className="row gap6"><ActorAvatar actor={{ kind: "person", id: dir.me }} size={18} />{me?.name ?? dir.me}</span></Row>
        <Row label="角色"><span className="muted">{me?.role ?? "—"}</span></Row>
        <Row label="資料來源"><span className="muted">模擬資料(尚未連接正式 API)</span></Row>
        <Row label="版本"><span className="muted">開發版(Go + React)</span></Row>
      </Section>
      <Section footer="只會清除這個瀏覽器上的偏好設定,不影響整合、團隊與自動化設定。">
        <Row><button className="btn danger" onClick={reset}>還原所有本機設定…</button></Row>
      </Section>
    </Form>
  );
}
