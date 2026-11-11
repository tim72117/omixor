// 伺服器與服務 › GCP:真實的 Cloud Run 服務、Job、錯誤日誌與指標。只讀。Swift 的 InfraGCPTab.swift。
// 資料:view gcp;動作:gcp_* 與 connection_*(見 docs/PORTING.md)。
import { useEffect, useState } from "react";
import { Bar as RBar, BarChart, CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { action, useRun, useView } from "../../api";
import { Card, color, EmptyState, Modal, Pill } from "../../components/ui";
import { axisStyle, Banner, hhmm, monthDayTime, Seg, SIcon, useAct } from "../../components/opsKit";
import { relative } from "../../fmt";
import type { Connection, ConnState } from "../development/DevGitHubTab";

type GCPSettings = { project: string; region: string; services: string[]; jobs: string[]; connection: string };
type GCPService = { id: string; url: string; ready: boolean; state: string; message: string; latestReady: string; latestCreated: string; image: string; maxInstances: number; updatedAt?: string | null; lastModifier: string; traffic: { revision: string; percent: number; tag: string }[] };
type GCPRevision = { id: string; createdAt?: string | null; image: string; ready: boolean; message: string };
type GCPExecution = { id: string; createdAt?: string | null; startedAt?: string | null; completedAt?: string | null; succeeded: number; failed: number; running: number; cancelled: number; tasks: number; message: string };
type GCPJob = { id: string; latest?: GCPExecution | null; executions: GCPExecution[] };
type GCPLogEntry = { id: string; timestamp?: string | null; severity: string; text: string; resource: string; revision: string; httpStatus?: number | null; method: string; requestUrl: string; latency: string };
type GCPPoint = { time: string; series: string; value: number };
type GCPMetrics = { hours: number; requests: GCPPoint[]; latencyP95: GCPPoint[]; instances: GCPPoint[] };
type GCPView = {
  settings: GCPSettings; snapshot: { services: GCPService[]; jobs: GCPJob[]; fetchedAt?: string | null; busy: boolean; error?: string | null };
  connection: Connection | null; loggedIn: boolean; configured: boolean; state: ConnState | null;
};

const rolloutPending = (s: GCPService) => !!s.latestCreated && s.latestCreated !== s.latestReady;
const stateLabel = (s: GCPService) => (s.state === "CONDITION_RECONCILING" || s.state === "CONDITION_PENDING" ? "部署中" : !s.ready ? "異常" : rolloutPending(s) ? "新版本未就緒" : "正常");
const serviceColor = (s: GCPService) => { const l = stateLabel(s); return l === "正常" ? "green" : l === "部署中" ? "orange" : "red"; };
function execStatus(e: GCPExecution) {
  if (!e.completedAt) return e.running > 0 || e.startedAt ? "running" : "pending";
  if (e.cancelled > 0 && e.failed === 0 && e.succeeded < e.tasks) return "cancelled";
  return e.failed > 0 || e.succeeded < e.tasks ? "failed" : "succeeded";
}
const execLabel = (e: GCPExecution) => ({ running: "執行中", pending: "等待中", cancelled: "已取消", failed: "失敗" } as Record<string, string>)[execStatus(e)] ?? "成功";
const execColor = (e?: GCPExecution | null) => {
  if (!e) return "color-mix(in srgb, var(--gray) 40%, transparent)";
  const s = execStatus(e);
  return s === "succeeded" ? "green" : s === "failed" ? "red" : s === "running" || s === "pending" ? "orange" : "secondary";
};
const execDuration = (e: GCPExecution) => {
  const s = e.startedAt ?? e.createdAt;
  return s && e.completedAt ? (Date.parse(e.completedAt) - Date.parse(s)) / 1000 : undefined;
};
const sevColor = (s: string) => (["CRITICAL", "ALERT", "EMERGENCY", "ERROR"].includes(s) ? "red" : s === "WARNING" ? "orange" : "secondary");
const SERIES_COLOR: Record<string, string> = { "2xx": "green", "3xx": "blue", "4xx": "orange", "5xx": "red", 其他: "gray" };
const ghDuration = (sec?: number) => (sec == null ? undefined : sec < 60 ? `${Math.trunc(sec)} 秒` : `${Math.trunc(sec / 60)} 分 ${Math.trunc(sec % 60)} 秒`);
const lastPart = (s: string) => s.split("/").filter(Boolean).pop() ?? s;
function imageTag(image: string) {
  const at = image.indexOf("@");
  if (at >= 0) return image.slice(at + 1, at + 20);
  const colon = image.lastIndexOf(":");
  if (colon >= 0 && !image.slice(colon).includes("/")) return image.slice(colon + 1, colon + 13);
  return lastPart(image);
}
const consoleURL = (st: GCPSettings, service?: string, job?: string) =>
  service ? `https://console.cloud.google.com/run/detail/${st.region}/${service}/metrics?project=${st.project}`
    : job ? `https://console.cloud.google.com/run/jobs/details/${st.region}/${job}/executions?project=${st.project}`
      : `https://console.cloud.google.com/run?project=${st.project}`;
const openURL = (u: string) => window.open(u, "_blank", "noopener");
const errMsg = (e: any) => String(e?.message ?? e);
const splitList = (s: string) => s.split(",").map((x) => x.trim()).filter(Boolean);

export function InfraGCPTab() {
  const [gcp, err] = useView<GCPView>("gcp", ["connections"]);
  const ready = !!gcp?.connection && gcp.loggedIn && gcp.configured;
  useEffect(() => {
    if (ready && !gcp!.snapshot?.fetchedAt && !gcp!.snapshot?.busy) action("gcp_refresh").catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, gcp?.state?.account]);
  if (err && !gcp) return <EmptyState title="讀不到 GCP 資料" icon="cloud" message={err} />;
  if (!gcp) return <EmptyState title="讀取中…" icon="cloud" />;
  if (!ready) return <div className="page-scroll"><div style={{ padding: 24, maxWidth: 760 }}><GCPSetup gcp={gcp} /></div></div>;
  return <GCPMain gcp={gcp} />;
}

function GCPMain({ gcp }: { gcp: GCPView }) {
  const [selection, setSelection] = useState<string>();
  const [editing, setEditing] = useState(false);
  const { run, toastEl } = useAct();
  const snap = gcp.snapshot ?? { services: [], jobs: [], busy: false };
  const st = gcp.settings;
  useEffect(() => { if (!selection && snap.services?.[0]) setSelection("svc:" + snap.services[0].id); }, [snap.fetchedAt]); // eslint-disable-line react-hooks/exhaustive-deps
  const scope = `${st.project}/${st.region}`;
  const [lastScope, setLastScope] = useState(scope);
  if (scope !== lastScope) { setLastScope(scope); setSelection(undefined); }
  const svc = selection?.startsWith("svc:") ? snap.services?.find((s) => "svc:" + s.id === selection) : undefined;
  const job = selection?.startsWith("job:") ? snap.jobs?.find((j) => "job:" + j.id === selection) : undefined;
  return (
    <div className="ops-page">
      <div className="row" style={{ gap: 10, padding: "8px 16px" }}>
        <SIcon name="person.crop.circle.badge.checkmark" color="green" />
        <span>{gcp.state?.account ?? "已登入"}</span>
        <span style={{ width: 1, height: 16, background: "var(--line)" }} />
        <span className="row gap4"><SIcon name="cloud" size={12} />{st.project}</span>
        <span className="caption mono muted">{st.region}</span>
        <button className="btn link" onClick={() => setEditing(true)}>設定…</button>
        <button className="btn link" onClick={() => openURL(consoleURL(st))}>Console</button>
        <span className="grow" />
        {snap.fetchedAt && <span className="caption muted">更新於 {relative(snap.fetchedAt)}</span>}
        {snap.busy && <SIcon name="arrow.triangle.2.circlepath" spin className="muted" />}
        <button className="btn" title="重新整理" disabled={snap.busy} onClick={() => run(() => action("gcp_refresh"))}><SIcon name="arrow.clockwise" /></button>
      </div>
      {snap.error && <Banner>{snap.error}</Banner>}
      <div className="divider" />
      <div className="ops-fill">
        <div className="ops-list" style={{ width: 300, flex: "none", borderRight: "1px solid var(--line)", background: "color-mix(in srgb, var(--side) 50%, transparent)" }}>
          <div className="ops-section-label">Cloud Run 服務</div>
          {(snap.services ?? []).map((s) => (
            <div key={s.id} className={"ops-list-row row gap8" + (selection === "svc:" + s.id ? " on" : "")} style={{ border: 0 }} onClick={() => setSelection("svc:" + s.id)}>
              <span className="dot" style={{ width: 8, height: 8, background: color(serviceColor(s)) }} />
              <div className="col" style={{ gap: 2, minWidth: 0 }}><span className="ellipsis">{s.id}</span><span className="caption mono muted ellipsis">{s.latestReady || stateLabel(s)}</span></div>
            </div>
          ))}
          {!snap.services?.length && snap.fetchedAt && <div className="muted" style={{ padding: "4px 12px" }}>沒有服務</div>}
          <div className="ops-section-label">Cloud Run Job</div>
          {(snap.jobs ?? []).map((j) => (
            <div key={j.id} className={"ops-list-row row gap8" + (selection === "job:" + j.id ? " on" : "")} style={{ border: 0 }} onClick={() => setSelection("job:" + j.id)}>
              <span className="dot" style={{ width: 8, height: 8, background: color(execColor(j.latest)) }} />
              <div className="col" style={{ gap: 2, minWidth: 0 }}><span className="ellipsis">{j.id}</span><span className="caption muted ellipsis">{j.latest ? `${execLabel(j.latest)} · ${j.latest.createdAt ? relative(j.latest.createdAt) : ""}` : "還沒執行過"}</span></div>
            </div>
          ))}
          {!snap.jobs?.length && snap.fetchedAt && <div className="muted" style={{ padding: "4px 12px" }}>沒有 Job</div>}
        </div>
        <div className="ops-scroll">
          {svc ? <ServiceDetail key={svc.id} s={svc} st={st} /> : job ? <JobDetail key={job.id} j={job} st={st} />
            : <EmptyState title={snap.fetchedAt ? "選一個服務或 Job" : "讀取中…"} icon="cloud" message="狀態每 5 分鐘自動更新;錯誤日誌與指標在打開時讀。" />}
        </div>
      </div>
      <Modal open={editing} onClose={() => setEditing(false)} width={520}>
        <div className="h3">GCP 設定</div>
        <SettingsForm st={st} onSaved={() => { setEditing(false); action("gcp_refresh").catch(() => undefined); }} onCancel={() => setEditing(false)} />
      </Modal>
      {toastEl}
    </div>
  );
}

function SettingsForm({ st, onSaved, onCancel }: { st: GCPSettings; onSaved: () => void; onCancel?: () => void }) {
  const [project, setProject] = useState(st?.project ?? "");
  const [region, setRegion] = useState(st?.region || "asia-east1");
  const [services, setServices] = useState((st?.services ?? []).join(", "));
  const [jobs, setJobs] = useState((st?.jobs ?? []).join(", "));
  const { busy, error, run } = useRun();
  const field = (label: string, v: string, set: (s: string) => void, ph: string) => <><span>{label}</span><input className="input" placeholder={ph} value={v} onChange={(e) => set(e.target.value)} /></>;
  return (
    <div className="col gap12">
      <div style={{ display: "grid", gridTemplateColumns: "auto 1fr", gap: "8px 10px", alignItems: "center" }}>
        {field("專案 id", project, setProject, "例如 shuttle-045094509")}
        {field("區域", region, setRegion, "asia-east1")}
        {field("追蹤的服務", services, setServices, "用逗號分隔;空白表示全部")}
        {field("追蹤的 Job", jobs, setJobs, "用逗號分隔;空白表示全部")}
      </div>
      {error && <Banner>{error}</Banner>}
      <div className="row gap8" style={{ justifyContent: "flex-end" }}>
        {onCancel && <button className="btn" onClick={onCancel}>取消</button>}
        <button className={"btn" + (onCancel ? " primary" : "")} disabled={busy || !project.trim()} onClick={async () => {
          const r = await run(() => action("gcp_settings", { project: project.trim(), region: region.trim(), services: splitList(services), jobs: splitList(jobs) }));
          if (r !== undefined) onSaved();
        }}>儲存</button>
      </div>
    </div>
  );
}

function stat(label: string, value: string, c?: string) {
  return <div className="col" style={{ gap: 2 }}><span className="caption muted">{label}</span><span className="num" style={{ fontSize: 15, fontWeight: 600, color: c ? `var(--${c})` : undefined }}>{value}</span></div>;
}

function ServiceDetail({ s, st }: { s: GCPService; st: GCPSettings }) {
  const [hours, setHours] = useState(24);
  const [metrics, setMetrics] = useState<GCPMetrics>();
  const [mErr, setMErr] = useState<string>();
  const [revisions, setRevisions] = useState<GCPRevision[]>();
  const [severity, setSeverity] = useState("ERROR");
  const [logs, setLogs] = useState<GCPLogEntry[]>();
  const [lErr, setLErr] = useState<string>();
  useEffect(() => {
    setMetrics(undefined); setMErr(undefined);
    action<GCPMetrics>("gcp_metrics", { service: s.id, hours }).then(setMetrics).catch((e) => setMErr("讀不到指標:" + errMsg(e)));
  }, [s.id, hours]);
  useEffect(() => {
    setLogs(undefined); setLErr(undefined);
    action<GCPLogEntry[]>("gcp_logs", { service: s.id, severity, hours, limit: 100 }).then((l) => setLogs(Array.isArray(l) ? l : [])).catch((e) => setLErr("讀不到日誌:" + errMsg(e)));
  }, [s.id, severity, hours]);
  useEffect(() => { action<GCPRevision[]>("gcp_revisions", { service: s.id }).then((r) => setRevisions(Array.isArray(r) ? r : [])).catch(() => setRevisions([])); }, [s.id]);

  const total = (k: string) => (metrics?.requests ?? []).filter((p) => p.series === k).reduce((a, p) => a + p.value, 0);
  const totalReq = Math.trunc((metrics?.requests ?? []).reduce((a, p) => a + p.value, 0));
  const errRate = totalReq > 0 ? total("5xx") / totalReq : 0;
  const maxP95 = Math.max(0, ...(metrics?.latencyP95 ?? []).map((p) => p.value));
  const reqRows = new Map<number, any>();
  (metrics?.requests ?? []).forEach((p) => { const k = Date.parse(p.time); const r = reqRows.get(k) ?? { x: k }; r[p.series] = (r[p.series] ?? 0) + p.value; reqRows.set(k, r); });
  const reqData = [...reqRows.values()].sort((a, b) => a.x - b.x);
  const latData = (metrics?.latencyP95 ?? []).map((p) => ({ x: Date.parse(p.time), v: p.value })).sort((a, b) => a.x - b.x);
  const fmtX = (v: number) => (hours > 48 ? `${new Date(v).getMonth() + 1}/${new Date(v).getDate()}` : hhmm(v));
  const tip = { contentStyle: { fontSize: 12, background: "var(--bg2)", border: "1px solid var(--line)" }, labelFormatter: (v: any) => monthDayTime(Number(v)) };
  return (
    <div className="col" style={{ padding: 20, gap: 14, maxWidth: 980 }}>
      <div className="row gap8" style={{ alignItems: "baseline" }}>
        <span className="h2">{s.id}</span><Pill text={stateLabel(s)} color={serviceColor(s)} />
        <span className="grow" />
        <div style={{ width: 200 }}><Seg value={String(hours)} onChange={(v) => setHours(Number(v))} options={[{ value: "6", label: "6 小時" }, { value: "24", label: "24 小時" }, { value: "168", label: "7 天" }]} /></div>
        <button className="btn" onClick={() => openURL(consoleURL(st, s.id))}>在 Console 打開</button>
      </div>
      {s.message && !s.ready && <div className="row gap6" style={{ color: "var(--red)" }}><SIcon name="exclamationmark.triangle" color="red" />{s.message}</div>}
      <div className="ops-kbd-row">
        <span className="muted">網址</span>{s.url ? <a className="mono" href={s.url} target="_blank" rel="noreferrer">{s.url}</a> : <span>—</span>}
        <span className="muted">目前版本</span><span className="mono">{s.latestReady + (rolloutPending(s) ? `(最新建立:${s.latestCreated},還沒就緒)` : "")}</span>
        <span className="muted">流量</span><span className="mono">{(s.traffic ?? []).map((x) => `${x.revision} ${x.percent}%` + (x.tag ? `(${x.tag})` : "")).join("、")}</span>
        <span className="muted">映像</span><span className="caption mono ellipsis" style={{ userSelect: "text" }} title={s.image}>{s.image}</span>
        <span className="muted">更新</span><span>{(s.updatedAt ? relative(s.updatedAt) : "—") + (s.lastModifier ? ` · ${s.lastModifier}` : "") + (s.maxInstances > 0 ? ` · 最多 ${s.maxInstances} 個執行個體` : "")}</span>
      </div>
      <Card title="請求與延遲" subtitle={`Cloud Monitoring,最近 ${hours >= 168 ? "7 天" : `${hours} 小時`}`}>
        {mErr && <div className="caption" style={{ color: "var(--orange)" }}>{mErr}</div>}
        {metrics ? (
          <>
            <div className="row wrap" style={{ gap: 24 }}>
              {stat("請求", String(totalReq))}
              {stat("5xx", String(Math.trunc(total("5xx"))), total("5xx") > 0 ? "red" : undefined)}
              {stat("4xx", String(Math.trunc(total("4xx"))))}
              {stat("5xx 比例", `${(errRate * 100).toFixed(2)}%`, errRate > 0.003 ? "red" : undefined)}
              {stat("最高 p95", maxP95 > 0 ? `${maxP95.toFixed(0)} ms` : "—")}
              {stat("最多執行個體", String(Math.trunc(Math.max(0, ...(metrics.instances ?? []).map((p) => p.value)))))}
            </div>
            {reqData.length === 0 ? <div className="caption muted">這段時間沒有請求</div> : (
              <div style={{ height: 150 }}>
                <ResponsiveContainer>
                  <BarChart data={reqData} margin={{ top: 4, right: 6, left: -18, bottom: 0 }}>
                    <CartesianGrid stroke="var(--line)" vertical={false} />
                    <XAxis dataKey="x" tick={axisStyle} stroke="var(--line)" tickFormatter={fmtX} minTickGap={30} />
                    <YAxis tick={axisStyle} stroke="var(--line)" />
                    <Tooltip {...tip} />
                    <Legend wrapperStyle={{ fontSize: 11 }} iconSize={8} />
                    {["2xx", "3xx", "4xx", "5xx", "其他"].filter((k) => reqData.some((r) => r[k] != null)).map((k) => <RBar key={k} dataKey={k} stackId="a" fill={color(SERIES_COLOR[k])} isAnimationActive={false} />)}
                  </BarChart>
                </ResponsiveContainer>
              </div>
            )}
            {latData.length > 0 && (
              <div style={{ height: 110 }}>
                <ResponsiveContainer>
                  <LineChart data={latData} margin={{ top: 4, right: 6, left: -6, bottom: 0 }}>
                    <CartesianGrid stroke="var(--line)" vertical={false} />
                    <XAxis dataKey="x" tick={axisStyle} stroke="var(--line)" tickFormatter={fmtX} minTickGap={30} />
                    <YAxis tick={axisStyle} stroke="var(--line)" label={{ value: "p95 延遲 (ms)", angle: -90, position: "insideLeft", fontSize: 10, fill: "var(--fg2)" }} />
                    <Tooltip {...tip} formatter={(v: any) => `${Number(v).toFixed(0)} ms`} />
                    <Line dataKey="v" name="p95" stroke="var(--blue)" dot={false} isAnimationActive={false} />
                  </LineChart>
                </ResponsiveContainer>
              </div>
            )}
          </>
        ) : !mErr && <Spinner />}
      </Card>
      <Card>
        <div className="row"><span className="card-title">日誌</span><span className="grow" /><SeverityPicker v={severity} set={setSeverity} /></div>
        <LogList logs={logs} error={lErr} />
      </Card>
      <Card title="最近的版本">
        {revisions ? revisions.map((r) => (
          <div key={r.id} className="row gap8">
            <SIcon name={r.ready ? "checkmark.circle" : "xmark.circle"} color={r.ready ? "green" : "red"} />
            <span className="mono">{r.id}</span>
            {r.id === s.latestReady && <Pill text="目前" color="green" />}
            {r.message && !r.ready && <span className="caption ellipsis" style={{ color: "var(--red)" }}>{r.message}</span>}
            <span className="grow" />
            <span className="caption mono muted">{imageTag(r.image)}</span>
            {r.createdAt && <span className="caption muted">{relative(r.createdAt)}</span>}
          </div>
        )) : <Spinner />}
      </Card>
    </div>
  );
}

const Spinner = () => <div className="row" style={{ justifyContent: "center" }}><SIcon name="arrow.triangle.2.circlepath" spin className="muted" /></div>;
const SeverityPicker = ({ v, set }: { v: string; set: (s: string) => void }) => (
  <select className="select" style={{ width: 120 }} value={v} onChange={(e) => set(e.target.value)}>
    <option value="ERROR">錯誤以上</option><option value="WARNING">警告以上</option><option value="DEFAULT">全部</option>
  </select>
);

function LogList({ logs, error }: { logs?: GCPLogEntry[]; error?: string }) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  return (
    <>
      {error && <div className="caption" style={{ color: "var(--orange)" }}>{error}</div>}
      {logs ? (logs.length === 0 ? <div className="row gap6 muted"><SIcon name="checkmark.seal" />這段時間沒有符合的日誌</div> : (
        <div className="col">
          {logs.map((l) => {
            const open = expanded.has(l.id);
            return (
              <div key={l.id} className="row gap6" style={{ padding: "4px 0", borderBottom: "1px solid var(--line)", alignItems: "flex-start" }}
                onClick={() => setExpanded((s) => { const n = new Set(s); if (open) n.delete(l.id); else n.add(l.id); return n; })}>
                <span style={{ width: 58, flex: "none", fontSize: 10.5, fontWeight: 700, color: color(sevColor(l.severity)) }}>{l.severity}</span>
                {l.timestamp && <span className="caption muted num" style={{ flex: "none" }}>{new Date(l.timestamp).toLocaleString("zh-TW", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false })}</span>}
                {l.httpStatus != null && l.httpStatus > 0 && <span className="caption mono" style={{ color: l.httpStatus >= 500 ? "var(--red)" : "var(--fg2)", flex: "none" }}>{l.httpStatus}</span>}
                <span className={"caption mono" + (open ? "" : " ellipsis")} style={{ userSelect: "text", whiteSpace: open ? "pre-wrap" : undefined, wordBreak: "break-all" }}>{l.text}</span>
              </div>
            );
          })}
        </div>
      )) : !error && <Spinner />}
    </>
  );
}

function JobDetail({ j, st }: { j: GCPJob; st: GCPSettings }) {
  const [executions, setExecutions] = useState<GCPExecution[]>();
  const [eErr, setEErr] = useState<string>();
  const [severity, setSeverity] = useState("ERROR");
  const [logs, setLogs] = useState<GCPLogEntry[]>();
  const [lErr, setLErr] = useState<string>();
  useEffect(() => { action<GCPExecution[]>("gcp_executions", { job: j.id }).then((x) => setExecutions(Array.isArray(x) ? x : [])).catch((e) => setEErr("讀不到執行:" + errMsg(e))); }, [j.id]);
  useEffect(() => {
    setLogs(undefined); setLErr(undefined);
    action<GCPLogEntry[]>("gcp_logs", { job: j.id, severity, hours: 168, limit: 100 }).then((l) => setLogs(Array.isArray(l) ? l : [])).catch((e) => setLErr("讀不到日誌:" + errMsg(e)));
  }, [j.id, severity]);
  return (
    <div className="col" style={{ padding: 20, gap: 14, maxWidth: 980 }}>
      <div className="row gap8" style={{ alignItems: "baseline" }}>
        <span className="h2">{j.id}</span>{j.latest && <Pill text={"最近一次:" + execLabel(j.latest)} color={execColor(j.latest)} />}
        <span className="grow" /><button className="btn" onClick={() => openURL(consoleURL(st, undefined, j.id))}>在 Console 打開</button>
      </div>
      <Card title="最近的執行">
        {eErr && <div className="caption" style={{ color: "var(--orange)" }}>{eErr}</div>}
        {executions ? executions.map((e) => (
          <div key={e.id} className="row gap8">
            <span className="dot" style={{ width: 8, height: 8, background: color(execColor(e)) }} />
            <span className="mono">{e.id}</span>
            <span className="caption" style={{ color: color(execColor(e)) }}>{execLabel(e)}</span>
            <span className="caption muted">{e.succeeded}/{e.tasks} 個工作成功</span>
            {e.message && <span className="caption ellipsis" style={{ color: "var(--red)" }}>{e.message}</span>}
            <span className="grow" />
            {ghDuration(execDuration(e)) && <span className="caption muted">{ghDuration(execDuration(e))}</span>}
            {e.createdAt && <span className="caption muted">{relative(e.createdAt)}</span>}
          </div>
        )) : !eErr && <Spinner />}
      </Card>
      <Card>
        <div className="row"><span className="card-title">日誌(最近 7 天)</span><span className="grow" /><SeverityPicker v={severity} set={setSeverity} /></div>
        <LogList logs={logs} error={lErr} />
      </Card>
    </div>
  );
}

// MARK: - 登入引導

function GCPSetup({ gcp }: { gcp: GCPView }) {
  const conn = gcp.connection;
  const st = gcp.state;
  const [clientId, setClientId] = useState(conn?.oauth?.clientId ?? "");
  const [secret, setSecret] = useState("");
  const { busy, error, run } = useRun();
  const createURL = `https://console.cloud.google.com/auth/clients/create${gcp.settings?.project ? `?project=${encodeURIComponent(gcp.settings.project)}` : ""}`;
  return (
    <div className="col" style={{ gap: 16 }}>
      <div className="row gap8 h2"><SIcon name="cloud" size={20} />連上 Google Cloud</div>
      <div className="muted">連上後,這裡會顯示 Cloud Run 服務的狀態、版本與流量、Job 的執行結果、錯誤日誌,以及請求量、5xx 與延遲;虛擬角色也能透過 MCP 讀。omixor 只讀(cloud-platform.read-only),不會部署、改流量或執行 Job。</div>
      {!gcp.configured && <Card title="專案"><SettingsForm st={gcp.settings} onSaved={() => undefined} /></Card>}
      {!conn ? (
        <button className="btn primary" style={{ alignSelf: "flex-start" }} disabled={busy} onClick={() => run(() => action("gcp_ensure_connection"))}>開始設定登入</button>
      ) : !conn.oauth?.clientId || !conn.oauth?.hasClientSecret ? (
        <Card>
          <div className="card-title">1. 在 Google Cloud 建一個 OAuth 用戶端(只要做一次)</div>
          <div className="col gap4 muted">
            <span>• 按下面的按鈕打開 Google Auth Platform › 用戶端 › 建立用戶端。</span>
            <span>• 應用程式類型選「電腦版應用程式」(Desktop app),名稱填 omixor,按建立。</span>
            <span>• 第一次用的話會先要你設定 OAuth 同意畫面:使用者類型選「外部」,把你自己的 Google 帳號加進「測試使用者」。</span>
            <span>• 同意畫面在「測試」狀態時,登入大約 7 天會過期要重登;改成「正式版」(不用送審,自己用會看到未驗證警告)就不會。</span>
          </div>
          <button className="btn" style={{ alignSelf: "flex-start" }} onClick={() => openURL(createURL)}>打開 Google Cloud 建立用戶端</button>
          <div className="divider" />
          <div className="card-title">2. 把用戶端 ID 和用戶端密鑰貼到這裡</div>
          <input className="input" placeholder="用戶端 ID(….apps.googleusercontent.com)" value={clientId} onChange={(e) => setClientId(e.target.value)} />
          <input className="input" type="password" placeholder="用戶端密鑰(存在鑰匙圈)" value={secret} onChange={(e) => setSecret(e.target.value)} />
          <button className="btn primary" style={{ alignSelf: "flex-start" }} disabled={busy || !clientId.trim() || (!secret && !conn.oauth?.hasClientSecret)} onClick={() => run(async () => {
            await action("connection_upsert", { ...conn, oauth: { ...conn.oauth, clientId: clientId.trim() } });
            if (secret.trim()) await action("connection_secret", { id: conn.id, secret: secret.trim() });
            setSecret("");
            await action("connection_login", { id: conn.id });
          })}>儲存並用瀏覽器登入</button>
        </Card>
      ) : (
        <Card>
          <div className="card-title">用瀏覽器登入 Google</div>
          <div className="muted">會打開 Google 的授權頁,選帳號、按允許後回到 omixor 就完成了。只要求唯讀權限(cloud-platform.read-only)。帳號需要這個專案的檢視權限。</div>
          <div className="row gap8">
            <button className="btn primary" disabled={busy || !!st?.busy} onClick={() => run(() => action("connection_login", { id: conn.id }))}>用瀏覽器登入</button>
            {(busy || st?.busy) && <><SIcon name="arrow.triangle.2.circlepath" spin className="muted" /><span className="caption">等瀏覽器授權…</span></>}
            <span className="grow" />
            <button className="btn link" onClick={() => run(async () => {
              await action("connection_upsert", { ...conn, oauth: { ...conn.oauth, clientId: "" } });
              await action("connection_secret", { id: conn.id, secret: "" });
              setClientId("");
            })}>重新設定 OAuth 用戶端</button>
          </div>
        </Card>
      )}
      {(st?.error || error) && <Banner>{st?.error || error}</Banner>}
    </div>
  );
}
