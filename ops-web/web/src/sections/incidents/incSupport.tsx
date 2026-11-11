// 事件畫面共用的小工具:SEV 樣式、時間軸事件樣式、時間格式、AI 摘要與檢討報告(Swift 的 IncSupport)
import { createContext, useContext } from "react";
import { go, useDirectory } from "../../components/bits";
import { Icon } from "../../components/Icon";
import {
  HEALTH_COLOR, INC_STATUSES, INC_STATUS_COLOR, INC_STATUS_ICON, INC_STATUS_LABEL, WORK_STATUS_LABEL,
  type Alert, type Incident, type IncidentEventKind, type IncidentStatus, type OnCallShift, type Service, type WorkItem,
} from "../../components/models";
import { Pill, color } from "../../components/ui";
import type { Actor } from "../../types";

export type IncData = { incidents: Incident[]; alerts: Alert[]; services: Service[]; workItems: WorkItem[]; onCall: OnCallShift[]; loaded: boolean; error?: string };
export const IncCtx = createContext<IncData>({ incidents: [], alerts: [], services: [], workItems: [], onCall: [], loaded: false });
export const useInc = () => useContext(IncCtx);

export const SEVS = [1, 2, 3, 4];
export const sevColor = (s: number) => (s === 1 ? "red" : s === 2 ? "orange" : s === 3 ? "yellow" : "gray");
export const sevLabel = (s: number) => `SEV${s}`;
export const sevMeaning = (s: number) =>
  s === 1 ? "核心服務中斷,多數使用者受影響" : s === 2 ? "主要功能異常,部分使用者受影響" : s === 3 ? "次要功能異常,有替代方案" : "輕微問題,幾乎不影響使用";

export function SevBadge({ sev, large }: { sev: number; large?: boolean }) {
  return (
    <span title={sevMeaning(sev)} className="mono" style={{
      fontSize: large ? 12 : 10, fontWeight: 700, padding: large ? "3px 8px" : "2px 5px", borderRadius: 4, flex: "none",
      color: sev === 3 ? "rgba(0,0,0,.75)" : "white", background: color(sevColor(sev)),
    }}>{sevLabel(sev)}</span>
  );
}

export const StatusPill = ({ status }: { status: IncidentStatus }) => <Pill text={INC_STATUS_LABEL[status]} color={INC_STATUS_COLOR[status]} icon={INC_STATUS_ICON[status]} />;

export const nextStatus = (s: IncidentStatus): IncidentStatus | undefined => INC_STATUSES[INC_STATUSES.indexOf(s) + 1];
export const stepOf = (s: IncidentStatus) => INC_STATUSES.indexOf(s);

export const KIND_ICON: Record<IncidentEventKind, string> = { status: "flag.fill", note: "text.bubble.fill", action: "hammer.fill", alert: "bell.fill", deploy: "shippingbox.fill", aiFinding: "sparkles" };
export const KIND_LABEL: Record<IncidentEventKind, string> = { status: "狀態", note: "備註", action: "處置", alert: "告警", deploy: "部署", aiFinding: "AI 發現" };
export const KIND_COLOR: Record<IncidentEventKind, string> = { status: "blue", note: "gray", action: "teal", alert: "red", deploy: "indigo", aiFinding: "purple" };
export const isKey = (k: IncidentEventKind) => k !== "note";
export const isAI = (a: Actor) => a.kind === "ai";
export const isPerson = (a: Actor) => a.kind === "person";

export const isActive = (i: Incident) => i.status !== "resolved";
export const t = (iso?: string | null) => (iso ? new Date(iso).getTime() : Date.now());
export const durationOf = (i: Incident, now = Date.now()) => Math.max(0, ((i.resolvedAt ? t(i.resolvedAt) : now) - t(i.startedAt)) / 1000);
/** 從事件開始到第一次有人回應的秒數(MTTA 用) */
export function ackDelay(i: Incident): number | undefined {
  const start = t(i.startedAt);
  const ds = i.timeline.filter((e) => isPerson(e.actor) && t(e.date) >= start).map((e) => t(e.date));
  return ds.length ? (Math.min(...ds) - start) / 1000 : undefined;
}
export const sortedTimeline = (i: Incident) => [...i.timeline].sort((a, b) => a.date.localeCompare(b.date));
export const incNumber = (id: string) => parseInt(id.replace(/\D/g, ""), 10) || 0;

/** 中文的持續時間,例如「2 小時 5 分」 */
export function fmtDuration(sec: number): string {
  const m = Math.floor(sec / 60);
  if (m < 1) return "不到 1 分";
  if (m < 60) return `${m} 分`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 === 0 ? `${h} 小時` : `${h} 小時 ${m % 60} 分`;
  return h % 24 === 0 ? `${Math.floor(h / 24)} 天` : `${Math.floor(h / 24)} 天 ${h % 24} 小時`;
}
export function fmtShort(sec?: number): string {
  if (sec == null) return "—";
  const m = Math.floor(sec / 60);
  if (m < 60) return `${Math.max(m, 0)}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}
const p2 = (n: number) => String(n).padStart(2, "0");
export function fmtDateTime(iso: string): string {
  const d = new Date(iso);
  return `${p2(d.getMonth() + 1)}/${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}`;
}
export function fmtDay(d: Date): string {
  return `${d.getMonth() + 1}/${d.getDate()} 週${"日一二三四五六"[d.getDay()]}`;
}

/** 可點的服務標籤 */
export function ServiceChip({ id }: { id: string }) {
  const { services } = useInc();
  const s = services.find((x) => x.id === id);
  return (
    <button className="bx-chip" title={`到「伺服器與服務」查看 ${s?.name ?? id}`} onClick={() => go("infrastructure", id)}
      style={{ background: "color-mix(in srgb, var(--fg) 6%, transparent)" }}>
      <span className="dot" style={{ background: color(HEALTH_COLOR[s?.health ?? "unknown"]) }} />
      {s?.name ?? id}
      <Icon name="arrow.up.right" size={8} className="faint" />
    </button>
  );
}

type Dir = ReturnType<typeof useDirectory>;

/** 本機「AI」整理:依資料產生摘要(Swift 的 IncAI.summary) */
export function aiSummary(inc: Incident, services: Service[], dir: Dir): string {
  const names = inc.services.map((id) => services.find((s) => s.id === id)?.name ?? id).join("、");
  const ev = sortedTimeline(inc);
  const parts = [`${sevLabel(inc.sev)} 事件,影響 ${names || "未指定服務"},已持續 ${fmtDuration(durationOf(inc))}。`];
  const finding = [...ev].reverse().find((e) => e.kind === "aiFinding");
  if (finding) parts.push(`最新發現:${finding.text}`);
  const act = [...ev].reverse().find((e) => e.kind === "action" || e.kind === "deploy");
  if (act) parts.push(`最近處置:${act.text}(${dir.name(act.actor)})。`);
  parts.push(`目前狀態為「${INC_STATUS_LABEL[inc.status]}」,由 ${dir.person(inc.commander)?.name ?? inc.commander} 指揮。`);
  const next = nextStatus(inc.status);
  if (inc.status !== "resolved" && next) parts.push(`建議下一步:確認條件後推進到「${INC_STATUS_LABEL[next]}」。`);
  return parts.join("\n");
}

export type Postmortem = {
  summary: string; impact: string; timeline: [string, string, string][]; rootCause: string; wentWell: string[]; improve: string[];
  actions: [string, string, boolean][];
};

export function postmortem(inc: Incident, services: Service[], workItems: WorkItem[], dir: Dir): Postmortem {
  const ev = sortedTimeline(inc);
  const names = inc.services.map((id) => services.find((s) => s.id === id)?.name ?? id).join("、");
  const commander = dir.person(inc.commander)?.name ?? inc.commander;
  const summary = `${fmtDateTime(inc.startedAt)} 起,${names || "服務"} 發生「${inc.title}」,定為 ${sevLabel(inc.sev)},共持續 ${fmtDuration(durationOf(inc))} 後解決。由 ${commander} 擔任指揮官。`;
  const key = ev.filter((e) => isKey(e.kind));
  const tl = (key.length ? key : ev).map((e) => [e.date, dir.name(e.actor), e.text] as [string, string, string]);
  const identified = ev.find((e) => e.kind === "status" && e.text.includes(INC_STATUS_LABEL.identified));
  const finding = [...ev].reverse().find((e) => e.kind === "aiFinding");
  const rootCause = finding ? finding.text + (identified ? `\n指揮官確認:${identified.text}` : "") : identified ? identified.text : "時間軸中沒有明確的根本原因紀錄,請指揮官補充。";
  const ack = ackDelay(inc);
  const well: string[] = [];
  if (ack != null) well.push(ack <= 300 ? `${fmtDuration(ack)} 內就有人回應,反應迅速` : `第一次回應花了 ${fmtDuration(ack)}`);
  const aiCount = ev.filter((e) => isAI(e.actor)).length;
  if (aiCount > 0) well.push(`AI 在時間軸上提供了 ${aiCount} 則分析,協助縮小範圍`);
  if (ev.some((e) => e.kind === "deploy")) well.push("能透過部署/回滾快速止血");
  const people = new Set(ev.filter((e) => e.actor.kind === "person").map((e) => e.actor.id));
  if (people.size > 1) well.push(`共有 ${people.size} 位成員協作處理`);
  if (!well.length) well.push("請團隊補充");
  const improve: string[] = [];
  if (ack != null && ack > 300) improve.push(`縮短告警到有人回應的時間(目前 ${fmtDuration(ack)})`);
  if (durationOf(inc) > 3600) improve.push("處理時間超過 1 小時,檢查是否缺少 runbook 或自動化止血");
  if (!ev.some((e) => e.kind === "alert")) improve.push("此事件沒有對應的告警紀錄,可能是使用者先發現,需補監控");
  if (!improve.length) improve.push("目前沒有明顯缺口,持續觀察");
  let actions: [string, string, boolean][] = inc.followUps.map((id) => {
    const w = workItems.find((x) => x.id === id);
    return w ? [`${w.id} ${w.title}`, WORK_STATUS_LABEL[w.status] ?? w.status, true] : [id, "未知", true];
  });
  if (!actions.length) actions = [[`為「${inc.title}」補上告警與 runbook`, "建議", false], ["在測試環境重現並加入回歸測試", "建議", false]];
  return { summary, impact: inc.impact || "尚未填寫影響範圍。", timeline: tl, rootCause, wentWell: well, improve, actions };
}

export function postmortemMarkdown(pm: Postmortem, title: string): string {
  let s = `# 事件檢討:${title}\n\n## 摘要\n${pm.summary}\n\n## 影響\n${pm.impact}\n\n## 時間軸\n`;
  for (const [d, who, text] of pm.timeline) s += `- ${fmtDateTime(d)} ${who}:${text}\n`;
  s += `\n## 根本原因\n${pm.rootCause}\n\n## 做得好的地方\n`;
  for (const w of pm.wentWell) s += `- ${w}\n`;
  s += "\n## 可以改進的地方\n";
  for (const w of pm.improve) s += `- ${w}\n`;
  s += "\n## 行動項目\n";
  for (const [tt, st] of pm.actions) s += `- [ ] ${tt}(${st})\n`;
  return s;
}
