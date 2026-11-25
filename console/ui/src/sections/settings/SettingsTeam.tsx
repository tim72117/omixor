// 設定 › 團隊:組織成員(接核心)與值班表(只讀;只有範例資料時才有內容)(Swift 的 SettingsTeamPane)
// 輪值與升級規則、換班還沒有後端實作,不放只存在畫面上的設定。
import { useView } from "../../api";
import { ActorAvatar } from "../../components/Avatar";
import { Pill } from "../../components/Pill";
import { useDirectory } from "../../lib/directory";
import { arr, type OnCallShift } from "../../models";
import { Form, Row, Section } from "./form";
import css from "./settings.module.css";
import { SettingsMembers } from "./SettingsMembers";

const p2 = (n: number) => String(n).padStart(2, "0");
const shiftDate = (iso: string) => { const d = new Date(iso); return `${d.getMonth() + 1}/${d.getDate()} ${p2(d.getHours())}:${p2(d.getMinutes())}`; };

export function SettingsTeam() {
  const dir = useDirectory();
  const [ocRaw] = useView<OnCallShift[]>("on_call");
  const onCall = arr<OnCallShift>(ocRaw);
  const now = Date.now();
  const t = (s: string) => new Date(s).getTime();
  const shifts = onCall.filter((s) => t(s.end) > now).sort((a, b) => a.start.localeCompare(b.start) || a.role.localeCompare(b.role));
  return (
    <Form>
      <SettingsMembers />
      {shifts.length > 0 && (
        <Section header="值班輪值表">
          {shifts.map((s) => {
            const active = t(s.start) <= now && t(s.end) > now;
            const name = dir.person(s.person)?.name ?? s.person;
            return (
              <Row key={s.id}>
                <ActorAvatar actor={{ kind: "person", id: s.person }} size={22} />
                <span className={css.minW80}>{name}</span>
                <Pill text={s.role} color={s.role === "Primary" ? "var(--accent)" : "secondary"} />
                {active && <Pill text="進行中" color="green" />}
                <div className="grow" />
                <span className="caption num muted">{shiftDate(s.start)} – {shiftDate(s.end)}</span>
              </Row>
            );
          })}
        </Section>
      )}
    </Form>
  );
}
