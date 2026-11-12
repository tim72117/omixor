// 設定 › 團隊與值班:組織成員(接核心)、輪值表與升級規則(Swift 的 SettingsTeamPane)
import { useState } from "react";
import { tool, useView } from "../../api";
import { ActorAvatar, Switch, useContextMenu, useDirectory } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { arr, type OnCallShift } from "../../components/models";
import { Pill } from "../../components/ui";
import { Form, Row, Section } from "./form";
import { SettingsMembers } from "./SettingsMembers";

const p2 = (n: number) => String(n).padStart(2, "0");
const shiftDate = (iso: string) => { const d = new Date(iso); return `${d.getMonth() + 1}/${d.getDate()} ${p2(d.getHours())}:${p2(d.getMinutes())}`; };

export function SettingsTeam() {
  const dir = useDirectory();
  const [ocRaw] = useView<OnCallShift[]>("on_call");
  const onCall = arr<OnCallShift>(ocRaw);
  const [rotation, setRotation] = useState("weekly");
  const [handoff, setHandoff] = useState("10:00");
  const [escalate, setEscalate] = useState(10);
  const [auto, setAuto] = useState(true);
  const [followSun, setFollowSun] = useState(false);
  const menu = useContextMenu();
  const now = Date.now();
  const t = (s: string) => new Date(s).getTime();
  const shifts = onCall.filter((s) => t(s.end) > now).sort((a, b) => a.start.localeCompare(b.start) || a.role.localeCompare(b.role));
  return (
    <Form>
      <SettingsMembers />
      <Section header="值班輪值表">
        {shifts.length === 0 && <Row><span className="muted">目前沒有排定的值班</span></Row>}
        {shifts.map((s) => {
          const active = t(s.start) <= now && t(s.end) > now;
          const name = dir.person(s.person)?.name ?? s.person;
          return (
            <Row key={s.id}>
              <ActorAvatar actor={{ kind: "person", id: s.person }} size={22} />
              <span style={{ minWidth: 80 }}>{name}</span>
              <Pill text={s.role} color={s.role === "Primary" ? "var(--accent)" : "secondary"} />
              {active && <Pill text="進行中" color="green" />}
              <div className="grow" />
              <span className="caption num muted">{shiftDate(s.start)} – {shiftDate(s.end)}</span>
              <button className="bx-icon-btn" title="換班" onClick={menu.open(dir.people.filter((p) => p.id !== s.person).map((p) => ({
                label: `與 ${p.name} 換班`,
                onClick: () => { tool("ops_activity_post", { icon: "arrow.left.arrow.right", text: `提出換班:${name} 與 ${p.name}(${shiftDate(s.start)})`, section: "settings" }).catch(() => undefined); },
              })))}><Icon name="ellipsis.circle" size={14} /></button>
            </Row>
          );
        })}
      </Section>
      <Section header="輪值與升級規則" footer="Critical 告警會先通知 Primary;超過等待時間沒有確認,就通知 Secondary 與事件指揮官。">
        <Row label="輪值週期"><select className="select" value={rotation} onChange={(e) => setRotation(e.target.value)}><option value="daily">每天</option><option value="weekly">每週</option><option value="biweekly">每兩週</option></select></Row>
        <Row label="交接時間"><input className="input" type="time" value={handoff} onChange={(e) => setHandoff(e.target.value)} /></Row>
        <Row label="Primary 未回應時自動升級給 Secondary"><Switch on={auto} onChange={setAuto} /></Row>
        <Row label="升級等待時間">
          <span className="muted">{escalate} 分鐘</span>
          <input type="range" min={5} max={60} step={5} disabled={!auto} value={escalate} onChange={(e) => setEscalate(+e.target.value)} />
        </Row>
        <Row label="跨時區接力(Follow the sun)"><Switch on={followSun} onChange={setFollowSun} /></Row>
      </Section>
      {menu.node}
    </Form>
  );
}
