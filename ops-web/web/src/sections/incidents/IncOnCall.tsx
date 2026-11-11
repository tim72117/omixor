// 值班:目前值班、一週排班、升級規則(Swift 的 IncOnCallView)
import { useEffect, useRef, useState } from "react";
import { AIBadge, ActorAvatar, StatusDot, useDirectory, useNow } from "../../components/bits";
import { Icon } from "../../components/Icon";
import type { OnCallShift } from "../../components/models";
import { Card, EmptyState, color } from "../../components/ui";
import type { Actor } from "../../types";
import { fmtDateTime, fmtDay, fmtDuration, t, useInc } from "./incSupport";

export function IncOnCall() {
  const { onCall } = useInc();
  const now = useNow(60000);
  const active = onCall.filter((s) => t(s.start) <= now && t(s.end) > now);
  const primary = active.find((s) => s.role === "Primary");
  const secondary = active.find((s) => s.role === "Secondary");
  const upcoming = onCall.filter((s) => t(s.start) > now && s.role === "Primary").sort((a, b) => a.start.localeCompare(b.start))[0];
  return (
    <div className="page-scroll">
      <div className="page-inner">
        <div className="grid g3" style={{ alignItems: "start" }}>
          <PersonCard title="主要值班" shift={primary} tint="blue" now={now} />
          <PersonCard title="次要值班" shift={secondary} tint="teal" now={now} />
          <PersonCard title="下一班" shift={upcoming} tint="gray" now={now} upcoming />
        </div>
        <WeekSchedule now={now} />
        <Escalation now={now} />
      </div>
    </div>
  );
}

function PersonCard({ title, shift, tint, now, upcoming }: { title: string; shift?: OnCallShift; tint: string; now: number; upcoming?: boolean }) {
  const dir = useDirectory();
  const p = dir.person(shift?.person);
  return (
    <div className="card" style={{ borderRadius: 12, position: "relative", overflow: "hidden" }}>
      <div style={{ position: "absolute", left: 0, top: 0, bottom: 0, width: 4, background: color(tint) }} />
      <span className="row gap6 muted">{!upcoming && shift && <StatusDot c="green" pulsing />}{title}</span>
      {shift ? (
        <>
          <div className="row gap8">
            <ActorAvatar actor={{ kind: "person", id: shift.person }} size={36} />
            <div className="col"><span style={{ fontSize: 16, fontWeight: 600 }}>{p?.name ?? shift.person}</span><span className="caption muted">{p?.role ?? ""}</span></div>
          </div>
          <span className="caption muted">
            {upcoming ? `${fmtDateTime(shift.start)} 開始(${fmtDuration((t(shift.start) - now) / 1000)} 後)` : `到 ${fmtDateTime(shift.end)},還有 ${fmtDuration((t(shift.end) - now) / 1000)}`}
          </span>
        </>
      ) : (
        <>
          <span className="faint" style={{ fontSize: 16 }}>沒有排班</span>
          <span className="caption muted">{upcoming ? "未來沒有排定的班表" : "這個時段沒有人值班"}</span>
        </>
      )}
    </div>
  );
}

function WeekSchedule({ now }: { now: number }) {
  const { onCall } = useInc();
  const dir = useDirectory();
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(800);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWidth(el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  }, [onCall.length > 0]);
  const today = new Date(now); today.setHours(0, 0, 0, 0);
  const rangeStart = new Date(today); rangeStart.setDate(rangeStart.getDate() - 1);
  const rangeEnd = new Date(rangeStart); rangeEnd.setDate(rangeEnd.getDate() + 7);
  const days = Array.from({ length: 7 }, (_, i) => { const d = new Date(rangeStart); d.setDate(d.getDate() + i); return d; });
  const shifts = onCall.filter((s) => t(s.end) > rangeStart.getTime() && t(s.start) < rangeEnd.getTime());
  const people: string[] = [];
  [...shifts].sort((a, b) => a.start.localeCompare(b.start)).forEach((s) => { if (!people.includes(s.person)) people.push(s.person); });
  const nameW = 110, rowH = 34;
  const w = Math.max(1, width - nameW);
  const total = rangeEnd.getTime() - rangeStart.getTime();
  const x = (ms: number) => nameW + (Math.min(Math.max(ms - rangeStart.getTime(), 0), total) / total) * w;
  const h = 22 + people.length * rowH + 4;
  const legend = (tt: string, c: string) => <span className="row gap4"><span style={{ width: 12, height: 6, borderRadius: 2, background: color(c) }} />{tt}</span>;
  return (
    <Card title="本週排班" subtitle={`${fmtDay(rangeStart)} – ${fmtDay(days[6])}`}>
      {shifts.length === 0 ? <EmptyState title="沒有排班資料" icon="calendar.badge.exclamationmark" message="這一週還沒有值班排程。" /> : (
        <div className="col gap6">
          <div className="row gap12 muted" style={{ fontSize: 10.5 }}>{legend("主要", "blue")}{legend("次要", "teal")}{legend("現在", "red")}</div>
          <div ref={ref} style={{ position: "relative", height: h }}>
            {days.map((d, i) => {
              const isToday = d.toDateString() === new Date(now).toDateString();
              return (
                <div key={i} style={{ position: "absolute", left: nameW + (i * w) / 7, width: w / 7, top: 0, bottom: 0,
                  background: isToday ? "color-mix(in srgb, var(--accent) 6%, transparent)" : i % 2 === 0 ? "color-mix(in srgb, var(--fg) 2.5%, transparent)" : undefined }}>
                  <div style={{ textAlign: "center", fontSize: 10.5, paddingTop: 2, fontWeight: isToday ? 600 : 400, color: isToday ? "var(--accent)" : "var(--fg2)" }}>{fmtDay(d)}</div>
                </div>
              );
            })}
            {people.map((pid, row) => {
              const y = 22 + row * rowH;
              return (
                <div key={pid}>
                  <div className="row gap6" style={{ position: "absolute", left: 0, top: y, width: nameW - 8, height: rowH }}>
                    <ActorAvatar actor={{ kind: "person", id: pid }} size={20} />
                    <span className="ellipsis">{dir.person(pid)?.name ?? pid}</span>
                  </div>
                  {shifts.filter((s) => s.person === pid).map((s) => {
                    const x0 = x(t(s.start)), x1 = x(t(s.end));
                    const primary = s.role === "Primary";
                    const live = t(s.start) <= now && t(s.end) > now;
                    return (
                      <div key={s.id} title={`${dir.person(pid)?.name ?? pid} · ${primary ? "主要" : "次要"}值班\n${fmtDateTime(s.start)} – ${fmtDateTime(s.end)}`}
                        style={{ position: "absolute", left: x0 + 1, top: y + 6, width: Math.max(3, x1 - x0 - 2), height: rowH - 12, borderRadius: 5,
                          background: `color-mix(in srgb, ${color(primary ? "blue" : "teal")} ${live ? 90 : 55}%, transparent)`,
                          color: "white", fontSize: 10.5, fontWeight: 600, paddingLeft: 5, display: "flex", alignItems: "center", overflow: "hidden" }}>
                        {x1 - x0 > 34 ? (primary ? "主要" : "次要") : ""}
                      </div>
                    );
                  })}
                </div>
              );
            })}
            {now > rangeStart.getTime() && now < rangeEnd.getTime() && (
              <>
                <div style={{ position: "absolute", left: x(now), top: 16, bottom: 0, width: 1.5, background: "var(--red)" }} />
                <div style={{ position: "absolute", left: x(now) - 3, top: 13, width: 7, height: 7, borderRadius: 7, background: "var(--red)" }} />
              </>
            )}
          </div>
        </div>
      )}
    </Card>
  );
}

function Escalation({ now }: { now: number }) {
  const { onCall } = useInc();
  const dir = useDirectory();
  const active = onCall.filter((s) => t(s.start) <= now && t(s.end) > now);
  const primary = active.find((s) => s.role === "Primary")?.person;
  const secondary = active.find((s) => s.role === "Secondary")?.person;
  const lead = dir.people.find((p) => p.role.includes("主管") || p.role.toLowerCase().includes("lead") || p.role.includes("經理"));
  const steps: { delay: string; who: string; detail: string; icon: string; tint: string; actor?: Actor }[] = [
    { delay: "立即", who: "AI 分診", detail: "彙整告警、近期部署與日誌,附上初步判讀", icon: "sparkles", tint: "purple" },
    { delay: "0 分鐘", who: dir.person(primary)?.name ?? "主要值班", detail: "推播 + 電話通知", icon: "iphone.radiowaves.left.and.right", tint: "blue", actor: primary ? { kind: "person", id: primary } : undefined },
    { delay: "5 分鐘未確認", who: dir.person(secondary)?.name ?? "次要值班", detail: "推播 + 電話通知,並在 #incident 頻道標記", icon: "phone.arrow.up.right", tint: "teal", actor: secondary ? { kind: "person", id: secondary } : undefined },
    { delay: "15 分鐘未確認", who: lead?.name ?? "工程主管", detail: "SEV1/SEV2 直接通知,並開啟戰情會議", icon: "person.badge.shield.checkmark", tint: "orange", actor: lead ? { kind: "person", id: lead.id } : undefined },
    { delay: "30 分鐘未解決", who: "全體工程團隊", detail: "SEV1 發布狀態頁公告,通知客服", icon: "megaphone", tint: "red" },
  ];
  return (
    <Card title="升級規則" subtitle="告警觸發後依序通知,任一人確認即停止升級">
      <div className="col">
        {steps.map((s, i) => (
          <div key={i} className="row" style={{ alignItems: "stretch", gap: 12 }}>
            <div className="col" style={{ alignItems: "center", flex: "none" }}>
              <span style={{ width: 26, height: 26, borderRadius: 26, background: color(s.tint), display: "inline-flex", alignItems: "center", justifyContent: "center" }}>
                <Icon name={s.icon} size={12} color="white" />
              </span>
              {i < steps.length - 1 && <div style={{ width: 2, flex: 1, background: "color-mix(in srgb, var(--fg) 12%, transparent)" }} />}
            </div>
            <div className="col gap4" style={{ paddingBottom: 14 }}>
              <div className="row gap8">
                <span className="caption" style={{ fontWeight: 600, color: color(s.tint), padding: "1px 6px", borderRadius: 99, background: `color-mix(in srgb, ${color(s.tint)} 12%, transparent)` }}>{s.delay}</span>
                {s.actor && <ActorAvatar actor={s.actor} size={16} />}
                <span style={{ fontWeight: 500 }}>{s.who}</span>
                {i === 0 && <AIBadge />}
              </div>
              <span className="caption muted">{s.detail}</span>
            </div>
          </div>
        ))}
      </div>
    </Card>
  );
}
