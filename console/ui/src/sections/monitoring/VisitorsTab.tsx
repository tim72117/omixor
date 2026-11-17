// 監控 › 網站訪客:evoleap 的會員與訪客統計(後台「會員」頁同一份資料),透過連線以管理員身分讀取(Swift 的 MonVisitorsTab)
import { Fragment, useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Bar, BarChart, Cell, LabelList, ResponsiveContainer, XAxis, YAxis } from "recharts";
import { action, useView } from "../../api";
import { Spinner, hm, useStored } from "../../components/bits";
import { Icon } from "../../components/Icon";
import type { ConnectionsView } from "../../components/models";
import { Card, EmptyState, Pill, Segmented } from "../../components/ui";
import { relative } from "../../fmt";

// MARK: 模型(Visitors/SiteVisitors.swift;缺欄位一律用預設值)
type SiteCount = { name: string; count: number };
type Lesson = {
  lesson: string; learners: number; completed: number; avgPercent: number; guestViews: number; guestVisitors: number; guestWatchedMs: number;
  guestBots: number; guestTracked: number; guestReady: number; guestFailed: number; guestPlayed: number; guestIdleVisibleMs: number; guestReadyAvgMs: number;
};
type Visit = { lesson: string; device: string; source: string; readyMs: number; loadError: string; visibleMs: number; events: { t: number; e: string }[]; openedAt?: string | null };
type PathCount = { steps: { lesson: string; played: boolean }[]; count: number; played: number };
type HomeStay = { guest: string; device: string; source: string; visits: number; totalMs: number; longestMs: number; maxScroll: number; lastOpened?: string | null };
type Login = { visits: number; outcomes: SiteCount[]; next: SiteCount[]; fails: SiteCount[]; googleReady: number; googleReadyAvgMs: number; googleFailed: number; googleErrors: SiteCount[] };
type Guest = { devices: SiteCount[]; sources: SiteCount[]; errors: SiteCount[]; idleVisible: SiteCount[]; idleVisits: Visit[]; homePaths: PathCount[]; homeVisits: number; homeStays: HomeStay[]; login: Login };
type Overview = { users: number; activeUsers: number; guests: number; lessons: Lesson[]; guestStats: Guest };

const n = (v: unknown) => (typeof v === "number" ? v : 0);
const s = (v: unknown) => (typeof v === "string" ? v : "");
const a = <T,>(v: unknown, f: (x: any) => T): T[] => (Array.isArray(v) ? v.map(f) : []);
const count = (x: any): SiteCount => ({ name: s(x?.name), count: n(x?.count) });
function decode(o: any): Overview {
  const g = o?.guestStats ?? {};
  const l = g.login ?? {};
  return {
    users: n(o?.users), activeUsers: n(o?.activeUsers), guests: n(o?.guests),
    lessons: a(o?.lessons, (x) => ({
      lesson: s(x?.lesson), learners: n(x?.learners), completed: n(x?.completed), avgPercent: n(x?.avgPercent), guestViews: n(x?.guestViews),
      guestVisitors: n(x?.guestVisitors), guestWatchedMs: n(x?.guestWatchedMs), guestBots: n(x?.guestBots), guestTracked: n(x?.guestTracked),
      guestReady: n(x?.guestReady), guestFailed: n(x?.guestFailed), guestPlayed: n(x?.guestPlayed), guestIdleVisibleMs: n(x?.guestIdleVisibleMs), guestReadyAvgMs: n(x?.guestReadyAvgMs),
    })),
    guestStats: {
      devices: a(g.devices, count), sources: a(g.sources, count), errors: a(g.errors, count), idleVisible: a(g.idleVisible, count),
      idleVisits: a(g.idleVisits, (x) => ({ lesson: s(x?.lesson), device: s(x?.device), source: s(x?.source), readyMs: n(x?.readyMs), loadError: s(x?.loadError), visibleMs: n(x?.visibleMs), events: a(x?.events, (e) => ({ t: n(e?.t), e: s(e?.e) })), openedAt: x?.openedAt ?? null })),
      homePaths: a(g.homePaths, (x) => ({ steps: a(x?.steps, (st) => ({ lesson: s(st?.lesson), played: !!st?.played })), count: n(x?.count), played: n(x?.played) })),
      homeVisits: n(g.homeVisits),
      homeStays: a(g.homeStays, (x) => ({ guest: s(x?.guest), device: s(x?.device), source: s(x?.source), visits: n(x?.visits), totalMs: n(x?.totalMs), longestMs: n(x?.longestMs), maxScroll: n(x?.maxScroll), lastOpened: x?.lastOpened ?? null })),
      login: { visits: n(l.visits), outcomes: a(l.outcomes, count), next: a(l.next, count), fails: a(l.fails, count), googleReady: n(l.googleReady), googleReadyAvgMs: n(l.googleReadyAvgMs), googleFailed: n(l.googleFailed), googleErrors: a(l.googleErrors, count) },
    },
  };
}

const RANGES = [
  { id: "all", label: "全部" }, { id: "30m", label: "30 分鐘" }, { id: "12h", label: "12 小時" }, { id: "1d", label: "1 天" }, { id: "7d", label: "7 天" },
] as const;
type RangeId = (typeof RANGES)[number]["id"];

const pct = (v: number, total: number) => (total > 0 ? `${((v / total) * 100).toFixed(0)}%` : "—");
function ms(v: number): string {
  const sec = v / 1000;
  if (sec < 60) return sec < 10 ? `${sec.toFixed(1)} 秒` : `${sec.toFixed(0)} 秒`;
  const m = Math.floor(sec / 60);
  return m < 60 ? `${m} 分 ${Math.floor(sec) % 60} 秒` : `${Math.floor(m / 60)} 小時 ${m % 60} 分`;
}
function label(name: string): string {
  return ({ "": "(直接進站)", under10s: "不到 10 秒", "10to60s": "10–60 秒", over60s: "超過 1 分鐘", bot: "爬蟲", mobile: "手機", tablet: "平板", desktop: "電腦" } as Record<string, string>)[name] ?? name;
}

export function VisitorsTab() {
  const [cv, cvErr] = useView<ConnectionsView>("connections");
  const [range, setRange] = useState<RangeId>("1d");
  const [connId, setConnId] = useStored("visitors.connection", "evoleap");
  const [data, setData] = useState<Overview>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [updatedAt, setUpdatedAt] = useState<string>();
  const [loginErr, setLoginErr] = useState<string>();
  const conns = Array.isArray(cv?.connections) ? cv!.connections : [];
  const conn = conns.find((c) => c.id === connId);
  const st = conn ? cv?.states?.[conn.id] : undefined;
  const loggedIn = !!st?.account;
  const busyRef = useRef(false);

  const load = useCallback(async () => {
    if (busyRef.current) return;
    busyRef.current = true;
    setLoading(true);
    try {
      const r = await action("site_visitors", { range, connection: connId });
      setData(decode(r));
      setError(undefined);
      setUpdatedAt(new Date().toISOString());
    } catch (e: any) {
      setError(`讀取失敗:${e?.message ?? e}`);
    } finally {
      busyRef.current = false;
      setLoading(false);
    }
  }, [range, connId]);

  // 換連線、時間範圍或登入狀態時重讀;開著這一頁時每 60 秒更新
  useEffect(() => { if (loggedIn) load(); }, [load, loggedIn]);
  useEffect(() => {
    const t = window.setInterval(() => { if (loggedIn) load(); }, 60000);
    return () => window.clearInterval(t);
  }, [load, loggedIn]);

  const login = async () => {
    if (!conn) return;
    setLoginErr(undefined);
    try { await action("connection_login", { id: conn.id }); } catch (e: any) { setLoginErr(String(e?.message ?? e)); }
  };

  return (
    <div className="page-scroll">
      <div className="page-inner">
        <div className="row gap12">
          <select className="select" value={connId} onChange={(e) => { setConnId(e.target.value); setData(undefined); }}>
            {!conn && <option value={connId}>{connId}</option>}
            {conns.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
          {conn && (st?.account ? <Pill text={st.account} color="green" icon="person.crop.circle.badge.checkmark" />
            : st?.busy ? <><Spinner size={12} /><span className="muted">請在瀏覽器完成登入</span></>
            : <button className="btn" onClick={login}>登入「{conn.name}」…</button>)}
          <div className="grow" />
          <Segmented value={range} onChange={setRange} options={RANGES.map((r) => ({ value: r.id, label: r.label }))} />
          <button className="btn" title="重新整理" disabled={loading || !loggedIn} onClick={load}>{loading ? <Spinner size={12} /> : <Icon name="arrow.clockwise" size={13} />}</button>
          {updatedAt && <span className="caption muted">{hm(updatedAt)}</span>}
        </div>
        {(cvErr || loginErr) && <Card><span className="row gap6" style={{ color: "var(--orange)" }}><Icon name="exclamationmark.triangle.fill" size={13} />{loginErr ?? `讀不到連線:${cvErr}`}</span></Card>}
        {error && <Card><span className="row gap6" style={{ color: "var(--orange)", userSelect: "text" }}><Icon name="exclamationmark.triangle.fill" size={13} />{error}</span></Card>}
        {data ? <Body d={data} range={range} /> : !loading && !error && (
          <Card><EmptyState title="還沒有資料" icon="person.3" message={loggedIn ? "按「重新整理」讀取。" : "先登入連線,才能以管理員身分讀取訪客統計。"} /></Card>
        )}
      </div>
    </div>
  );
}

function Body({ d, range }: { d: Overview; range: RangeId }) {
  const player = d.lessons.filter((l) => !l.lesson.startsWith("gate:") && !l.lesson.startsWith("page:"));
  const gates = d.lessons.filter((l) => l.lesson.startsWith("gate:") && l.guestTracked > 0).map((l) => ({ name: l.lesson.slice(5), count: l.guestTracked })).sort((x, y) => y.count - x.count);
  const f = player.reduce((acc, l) => ({ tracked: acc.tracked + l.guestTracked, ready: acc.ready + l.guestReady, played: acc.played + l.guestPlayed, failed: acc.failed + l.guestFailed, bots: acc.bots + l.guestBots }), { tracked: 0, ready: 0, played: 0, failed: 0, bots: 0 });
  const g = d.guestStats;
  const steps = [
    { name: "打開", v: f.tracked, c: "var(--blue)" }, { name: "載入完成", v: f.ready, c: "var(--teal)" },
    { name: "有播放", v: f.played, c: "var(--green)" }, { name: "載入失敗", v: f.failed, c: "var(--red)" },
  ].map((x) => ({ ...x, label: `${x.v}  ${pct(x.v, f.tracked)}` }));
  const rows = player.filter((l) => l.guestTracked + l.learners + l.guestBots > 0).sort((x, y) => y.guestTracked - x.guestTracked || y.learners - x.learners);

  return (
    <>
      <div className="grid" style={{ gridTemplateColumns: "repeat(5, minmax(0, 1fr))" }}>
        <Kpi title={range === "all" ? "會員" : "新會員"} value={d.users} sub={`有進度 ${d.activeUsers}`} />
        <Kpi title="訪客" value={d.guests} sub="不同的訪客代號" />
        <Kpi title="課程頁打開" value={f.tracked} sub={`真人;爬蟲另有 ${f.bots}`} />
        <Kpi title="有播放" value={pct(f.played, f.tracked)} sub={`${f.played} 次`} />
        <Kpi title="首頁造訪" value={g.homeVisits} sub={`登入頁 ${g.login.visits} 次`} />
      </div>
      <Card title="訪客漏斗" subtitle="未登入的真人打開課程頁 → 播放器載入完成 → 有播放(有播放器的課加總;不含爬蟲與登入門檻)">
        <div style={{ height: 130 }}>
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={steps} layout="vertical" margin={{ left: 0, right: 90, top: 0, bottom: 0 }}>
              <XAxis type="number" hide />
              <YAxis type="category" dataKey="name" width={64} tick={{ fontSize: 11, fill: "var(--fg2)" }} axisLine={false} tickLine={false} />
              <Bar dataKey="v" isAnimationActive={false} barSize={18}>
                {steps.map((x) => <Cell key={x.name} fill={x.c} />)}
                <LabelList dataKey="label" position="right" style={{ fontSize: 11, fill: "var(--fg2)" }} />
              </Bar>
            </BarChart>
          </ResponsiveContainer>
        </div>
      </Card>
      <Card title="各課概況" subtitle="訪客的漏斗與會員的進度;只列有紀錄的課">
        {rows.length === 0 ? <span className="muted">這段時間沒有紀錄</span> : (
          <div className="bx-grid-table num" style={{ gridTemplateColumns: "minmax(120px, 2fr) repeat(10, auto)", justifyItems: "end" }}>
            {["課", "訪客", "打開", "載入", "播放", "失敗", "平均載入", "訪客播放", "會員", "看完", "平均進度"].map((h, i) => (
              <span key={h} className="h" style={i === 0 ? { justifySelf: "start" } : undefined}>{h}</span>
            ))}
            <span className="sep" />
            {rows.map((l) => (
              <Fragment key={l.lesson}>
                <span className="mono" style={{ justifySelf: "start" }}>{l.lesson}</span>
                <span>{l.guestVisitors}</span>
                <span>{l.guestTracked}</span>
                <span>{pct(l.guestReady, l.guestTracked)}</span>
                <span style={{ color: l.guestTracked > 0 && l.guestPlayed * 2 < l.guestTracked ? "var(--orange)" : undefined }}>{pct(l.guestPlayed, l.guestTracked)}</span>
                <span style={{ color: l.guestFailed > 0 ? "var(--red)" : "var(--fg2)" }}>{l.guestFailed > 0 ? l.guestFailed : "—"}</span>
                <span>{l.guestReadyAvgMs > 0 ? ms(l.guestReadyAvgMs) : "—"}</span>
                <span>{l.guestWatchedMs > 0 ? ms(l.guestWatchedMs) : "—"}</span>
                <span>{l.learners}</span>
                <span>{l.completed}</span>
                <span>{l.learners > 0 ? `${l.avgPercent.toFixed(0)}%` : "—"}</span>
              </Fragment>
            ))}
          </div>
        )}
      </Card>
      <div className="grid" style={{ gridTemplateColumns: "repeat(5, minmax(0, 1fr))", alignItems: "start" }}>
        <CountCard title="裝置" subtitle="每種裝置打開幾次(含爬蟲)" items={g.devices} />
        <CountCard title="進站來源" subtitle="真人從哪裡進來" items={g.sources} />
        <CountCard title="沒播放的停留" subtitle="打開後沒播放,頁面在前景多久" items={g.idleVisible} />
        <CountCard title="載入錯誤" subtitle="真人遇到的播放器錯誤" items={g.errors} />
        <CountCard title="登入門檻" subtitle="訪客打開要登入的課" items={gates} />
      </div>
      <Card title="從首頁進來的路徑" subtitle="第一站是首頁的造訪(相隔 30 分鐘以上算另一次),最常見的在前;▶ 表示那一站有播放">
        {g.homePaths.length === 0 ? <span className="caption muted">沒有資料</span> : (
          <div className="col gap6">
            {g.homePaths.map((p, i) => (
              <div key={i} className="row gap8" style={{ alignItems: "baseline" }}>
                <span className="num" style={{ width: 52, textAlign: "right", fontWeight: 600, flex: "none" }}>{p.count} 次</span>
                <span className="mono grow" style={{ userSelect: "text" }}>{p.steps.map((st) => (st.played ? "▶ " : "") + st.lesson).join("  →  ")}</span>
                {p.played > 0 && <Pill text={`有播放 ${p.played}`} color="green" />}
              </div>
            ))}
          </div>
        )}
      </Card>
      <Card title="首頁停留" subtitle="每個真人訪客在首頁停留多久(頁面在前景的時間),最近來過的在前">
        {g.homeStays.length === 0 ? <span className="caption muted">沒有資料</span> : (
          <>
            <div className="bx-grid-table num" style={{ gridTemplateColumns: "repeat(4, auto) repeat(4, auto)", justifyContent: "start" }}>
              {["最後造訪", "訪客", "裝置", "來源", "次數", "合計", "最長", "捲動"].map((h) => <span key={h} className="h">{h}</span>)}
              <span className="sep" />
              {g.homeStays.slice(0, 20).map((h, i) => (
                <Fragment key={i}>
                  <span>{h.lastOpened ? relative(h.lastOpened) : "—"}</span>
                  <span className="mono">{h.guest}</span>
                  <span>{h.device}</span>
                  <span className="ellipsis">{h.source || "直接"}</span>
                  <span style={{ justifySelf: "end" }}>{h.visits}</span>
                  <span style={{ justifySelf: "end" }}>{ms(h.totalMs)}</span>
                  <span style={{ justifySelf: "end" }}>{ms(h.longestMs)}</span>
                  <span style={{ justifySelf: "end" }}>{h.maxScroll}%</span>
                </Fragment>
              ))}
            </div>
            {g.homeStays.length > 20 && <span className="caption muted">還有 {g.homeStays.length - 20} 人</span>}
          </>
        )}
      </Card>
      <Card title="登入／註冊頁" subtitle={`訪客打開登入頁 ${g.login.visits} 次;Google 按鈕載入 ${g.login.googleReady} 次(平均 ${g.login.googleReadyAvgMs > 0 ? ms(g.login.googleReadyAvgMs) : "—"}),失敗 ${g.login.googleFailed} 次`}>
        <div className="grid g4" style={{ alignItems: "start" }}>
          <MiniList title="結果" items={g.login.outcomes} />
          <MiniList title="登入後要去" items={g.login.next} />
          <MiniList title="失敗訊息" items={g.login.fails} />
          <MiniList title="Google 按鈕錯誤" items={g.login.googleErrors} />
        </div>
      </Card>
      <Card title="最近沒播放的造訪" subtitle="打開了卻沒有播放的真人造訪(含登入門檻),新的在前;展開看足跡">
        {g.idleVisits.length === 0 ? <span className="caption muted">沒有資料</span> : (
          <div className="col gap4">
            {g.idleVisits.map((v, i) => (
              <details key={i} className="bx-disclosure">
                <summary>
                  <span className="caption muted" style={{ width: 70 }}>{v.openedAt ? relative(v.openedAt) : "—"}</span>
                  <span className="mono">{v.lesson}</span>
                  <span className="caption muted">{v.device} · {v.source || "直接"}</span>
                  <span className="caption muted">前景 {ms(v.visibleMs)}</span>
                  {v.loadError ? <Pill text={v.loadError} color="red" /> : v.readyMs > 0 ? <span className="caption muted">載入 {ms(v.readyMs)}</span> : null}
                </summary>
                <div className="col" style={{ paddingLeft: 24, gap: 2 }}>
                  {v.events.length === 0 ? <span className="caption muted">沒有足跡</span> : v.events.map((e, j) => (
                    <span key={j} className="row gap8 mono caption">
                      <span className="muted" style={{ width: 70, textAlign: "right" }}>{ms(e.t)}</span>
                      <span style={{ userSelect: "text" }}>{e.e}</span>
                    </span>
                  ))}
                </div>
              </details>
            ))}
          </div>
        )}
      </Card>
    </>
  );
}

function Kpi({ title, value, sub }: { title: string; value: ReactNode; sub: string }) {
  return (
    <div className="card" style={{ gap: 4 }}>
      <span className="caption muted">{title}</span>
      <span className="num" style={{ fontSize: 22, fontWeight: 600 }}>{value}</span>
      <span className="caption muted ellipsis">{sub}</span>
    </div>
  );
}

function CountCard({ title, subtitle, items }: { title: string; subtitle: string; items: SiteCount[] }) {
  const max = Math.max(1, ...items.map((c) => c.count));
  return (
    <Card title={title} subtitle={subtitle}>
      {items.length === 0 ? <span className="caption muted">沒有資料</span> : (
        <div className="col gap6">
          {items.map((c, i) => (
            <div key={i} className="col" style={{ gap: 2 }}>
              <div className="row caption"><span className="ellipsis grow">{label(c.name)}</span><span className="muted num">{c.count}</span></div>
              <div className="bx-bar" style={{ width: `${(c.count / max) * 100}%` }} />
            </div>
          ))}
        </div>
      )}
    </Card>
  );
}

function MiniList({ title, items }: { title: string; items: SiteCount[] }) {
  return (
    <div className="col gap4">
      <span className="caption muted" style={{ fontWeight: 600 }}>{title}</span>
      {items.length === 0 && <span className="caption muted">—</span>}
      {items.map((c, i) => (
        <div key={i} className="row caption"><span className="ellipsis grow" style={{ userSelect: "text" }}>{c.name}</span><span className="muted num">{c.count}</span></div>
      ))}
    </div>
  );
}
