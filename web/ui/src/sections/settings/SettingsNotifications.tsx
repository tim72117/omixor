// 設定 › 通知:依嚴重度選擇通知管道、事件與 AI 通知、勿擾時段(Swift 的 SettingsNotificationsPane)
import { Fragment, useState } from "react";
import { tool, useView } from "../../api";
import { Switch, hm } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { SEVERITIES, SEV_COLOR, SEV_LABEL, arr, type Integration, type Severity } from "../../components/models";
import { color } from "../../components/ui";
import { Form, Row, Section } from "./form";
import type { Pane } from "./SettingsView";

type Channel = "slack" | "email" | "pagerduty" | "sms" | "desktop";
const CHANNELS: { id: Channel; label: string; icon: string; key?: string }[] = [
  { id: "slack", label: "Slack", icon: "number", key: "slack" }, { id: "email", label: "Email", icon: "envelope" },
  { id: "pagerduty", label: "PagerDuty", icon: "phone.arrow.up.right", key: "pagerduty" }, { id: "sms", label: "簡訊", icon: "message" },
  { id: "desktop", label: "macOS 通知", icon: "bell" },
];

export function SettingsNotifications({ setPane }: { setPane: (p: Pane) => void }) {
  const [intRaw] = useView<Integration[]>("integrations");
  const integrations = arr<Integration>(intRaw);
  const [matrix, setMatrix] = useState<Record<Severity, Set<Channel>>>({
    critical: new Set(["slack", "pagerduty", "sms", "desktop"]), warning: new Set(["slack", "desktop"]), info: new Set(["email"]),
  });
  const [slack, setSlack] = useState("#ops-alerts");
  const [t, setT] = useState({ newInc: true, incStatus: true, pmReminder: true, aiApproval: true, aiAuto: true, quiet: false, bypass: true });
  const [digest, setDigest] = useState("daily");
  const [quietStart, setQuietStart] = useState("22:00");
  const [quietEnd, setQuietEnd] = useState("08:00");
  const [lastTest, setLastTest] = useState<string>();
  const integ = (c: (typeof CHANNELS)[number]) => (c.key ? integrations.find((i) => i.name.toLowerCase().includes(c.key!)) : undefined);
  const missing = CHANNELS.map(integ).filter((i): i is Integration => !!i && !i.connected);
  const toggle = (s: Severity, c: Channel, on: boolean) => setMatrix((m) => { const n = new Set(m[s]); if (on) n.add(c); else n.delete(c); return { ...m, [s]: n }; });
  const sw = (k: keyof typeof t, label: string, disabled?: boolean) => (
    <Row label={label}><Switch on={t[k]} disabled={disabled} onChange={(v) => setT({ ...t, [k]: v })} /></Row>
  );
  const sendTest = () => {
    const chans = CHANNELS.filter((c) => matrix.critical.has(c.id)).map((c) => c.label).sort().join("、");
    tool("activity_post", { icon: "bell.and.waves.left.and.right", text: `傳送測試通知到 ${chans || "(未選擇管道)"}`, section: "settings" }).catch(() => undefined);
    setLastTest(new Date().toISOString());
  };
  return (
    <Form>
      <Section header="告警通知管道" icon="bell.badge" footer="每個嚴重度可以同時送到多個管道。Critical 建議至少開啟 PagerDuty 或簡訊,確保值班人員會被叫醒。">
        <Row>
          <div style={{ display: "grid", gridTemplateColumns: `120px repeat(${CHANNELS.length}, 1fr)`, rowGap: 10, width: "100%", alignItems: "center" }}>
            <span className="caption muted" style={{ fontWeight: 600 }}>嚴重度</span>
            {CHANNELS.map((c) => <span key={c.id} className="col caption muted" style={{ alignItems: "center", fontWeight: 600, gap: 2 }}><Icon name={c.icon} size={13} />{c.label}</span>)}
            <span style={{ gridColumn: "1 / -1", height: 1, background: "var(--line)" }} />
            {SEVERITIES.map((s) => (
              <Fragment key={s}>
                <span className="row gap6"><span className="dot" style={{ width: 8, height: 8, background: color(SEV_COLOR[s]) }} />{SEV_LABEL[s]}</span>
                {CHANNELS.map((c) => (
                  <span key={s + c.id} style={{ textAlign: "center" }}>
                    <input type="checkbox" checked={matrix[s].has(c.id)} disabled={integ(c)?.connected === false} onChange={(e) => toggle(s, c.id, e.target.checked)} />
                  </span>
                ))}
              </Fragment>
            ))}
          </div>
        </Row>
        {missing.length > 0 && (
          <Row>
            <Icon name="exclamationmark.triangle.fill" size={12} color="var(--orange)" />
            <span className="caption grow">{missing.map((i) => i.name).join("、")} 尚未連線,無法透過它通知。</span>
            <button className="btn link caption" onClick={() => setPane("integrations")}>前往整合</button>
          </Row>
        )}
        <Row label="Slack 頻道"><input className="input" value={slack} onChange={(e) => setSlack(e.target.value)} /></Row>
        <Row>
          {lastTest && <span className="row gap4 caption" style={{ color: "var(--green)" }}><Icon name="checkmark.circle.fill" size={12} />已在 {hm(lastTest)} 送出測試通知</span>}
          <div className="grow" />
          <button className="btn" onClick={sendTest}>傳送測試通知</button>
        </Row>
      </Section>
      <Section header="事件" icon="flame">
        {sw("newInc", "建立新事件時通知全體維運成員")}
        {sw("incStatus", "事件狀態變更時通知訂閱者")}
        {sw("pmReminder", "事件結案 48 小時後提醒撰寫檢討報告")}
      </Section>
      <Section header="AI 自動化" icon="sparkles">
        {sw("aiApproval", "AI 請求核准時通知我")}
        {sw("aiAuto", "AI 自動執行變更後立即通知")}
        <Row label="AI 工作摘要">
          <select className="select" value={digest} onChange={(e) => setDigest(e.target.value)}><option value="daily">每天 09:00</option><option value="weekly">每週一 09:00</option><option value="off">不寄送</option></select>
        </Row>
      </Section>
      <Section header="勿擾時段" icon="moon" footer="勿擾時段內的 Warning 與 Info 通知會累積,到結束時一次送出。">
        {sw("quiet", "啟用勿擾時段")}
        <Row label="開始"><input className="input" type="time" disabled={!t.quiet} value={quietStart} onChange={(e) => setQuietStart(e.target.value)} /></Row>
        <Row label="結束"><input className="input" type="time" disabled={!t.quiet} value={quietEnd} onChange={(e) => setQuietEnd(e.target.value)} /></Row>
        {sw("bypass", "Critical 告警與值班呼叫不受勿擾限制", !t.quiet)}
      </Section>
    </Form>
  );
}
