// CI/CD › 旁白發佈:送出旁白語音批次、等它完成、試聽、按下發佈到 evoleap(Swift 的 PipeVoiceTab.swift)。
// 送出/試算走 MCP 工具 ops_voice_submit;發佈只能由人按(action voice_publish)。
import { useRef, useState } from "react";
import { action, tool, useView } from "../../api";
import { Card, EmptyState, Modal, Pill } from "../../components/ui";
import { Check, hhmm, SIcon, useAct, useConfirm, useList, useOps } from "../../components/opsKit";
import { relative } from "../../fmt";
import type { Connection, ConnState } from "../development/DevGitHubTab";

type VoiceState = "queued" | "submitting" | "processing" | "collecting" | "ready" | "publishing" | "published" | "nothingToDo" | "failed" | "canceled";
const STATE: Record<VoiceState, { label: string; icon: string }> = {
  queued: { label: "排隊中", icon: "clock" }, submitting: { label: "送出中", icon: "arrow.triangle.2.circlepath" },
  processing: { label: "產生中", icon: "waveform" }, collecting: { label: "收結果中", icon: "arrow.triangle.2.circlepath" },
  ready: { label: "待發佈", icon: "hand.raised.fill" }, publishing: { label: "發佈中", icon: "arrow.triangle.2.circlepath" },
  published: { label: "已發佈", icon: "checkmark.seal.fill" }, nothingToDo: { label: "沒有缺的旁白", icon: "checkmark.circle" },
  failed: { label: "失敗", icon: "xmark.circle.fill" }, canceled: { label: "已取消", icon: "slash.circle" },
};
const isFinal = (s: VoiceState) => ["published", "nothingToDo", "failed", "canceled"].includes(s);
type VoiceClip = { text: string; file: string; ms: number };
type VoiceFileChange = { path: string; isNew: boolean; clips: number; totalMs: number; added: VoiceClip[]; removed: number; course?: string | null; lesson?: string | null; sha: string; publish?: string | null };
type VoiceJob = {
  id: string; scripts: string[]; all: boolean; limit?: number | null; state: VoiceState; createdAt: string; updatedAt: string; by: string;
  batchName?: string | null; clipCount?: number | null; nextCheckAt?: string | null; lastCheckAt?: string | null; checks: number; batchState?: string | null;
  error?: string | null; log: { date: string; text: string }[]; changes: VoiceFileChange[]; failedClips: string[]; publishedAt?: string | null; publishedTo?: string | null;
};
type ConnView = { connections: Connection[]; states: Record<string, ConnState> };
/** 旁白設定(發佈目的地、查詢間隔、shuttle 專案路徑);後端沒有這個 view 時顯示 — */
type VoiceSettings = { connection?: string; pollMinutes?: number; shuttlePath?: string; hasToken?: boolean; target?: { id: string; name: string; baseURL: string; production: boolean } | null };

function jobTitle(j: VoiceJob) {
  const lim = j.limit ? `(每份前 ${j.limit} 段)` : "";
  if (j.all) return "全部腳本" + lim;
  const s = j.scripts ?? [];
  return s.slice(0, 3).join("、") + (s.length > 3 ? ` 等 ${s.length} 份` : "") + lim;
}

export function PipeVoiceTab() {
  const [jobs, err] = useList<VoiceJob>("voice_jobs");
  const [conns] = useView<ConnView>("connections");
  const [settings] = useView<VoiceSettings>("voice_settings");
  const audio = useRef<HTMLAudioElement | null>(null);
  const [playing, setPlaying] = useState<string>();
  const toggle = (c: VoiceClip) => {
    audio.current?.pause();
    if (playing === c.file) { setPlaying(undefined); return; }
    const a = new Audio("https://storage.googleapis.com/shuttle-voice/" + c.file);
    a.onended = () => setPlaying(undefined);
    a.play().catch(() => setPlaying(undefined));
    audio.current = a;
    setPlaying(c.file);
  };
  const connections = Array.isArray(conns?.connections) ? conns!.connections : [];
  const target: Connection | undefined = connections.find((c) => c.id === settings?.connection)
    ?? (settings?.target ? { id: settings.target.id, name: settings.target.name, baseURL: settings.target.baseURL, isProduction: settings.target.production, auth: "approval", oauth: { clientId: "", hasClientSecret: false } } : undefined);
  const tstate = target ? conns?.states?.[target.id] : undefined;
  return (
    <div className="page-scroll">
      <div className="page-inner">
        <ConnectionCard connections={connections} target={target} state={tstate} settings={settings} />
        <NewBatchCard />
        {err && jobs.length === 0 ? (
          <Card><div style={{ minHeight: 160, display: "flex" }}><EmptyState title="讀不到旁白批次" icon="waveform" message={err} /></div></Card>
        ) : jobs.length === 0 ? (
          <Card><div style={{ minHeight: 160, display: "flex" }}><EmptyState title="還沒有旁白批次" icon="waveform" message="在上面送出批次,或讓 AI 透過 MCP 的 ops_voice_submit 分派過來。" /></div></Card>
        ) : jobs.map((j) => <VoiceJobCard key={j.id} job={j} target={target} loggedIn={!!tstate?.account || !!settings?.hasToken} playing={playing} toggle={toggle} />)}
      </div>
    </div>
  );
}

function ConnectionCard({ connections, target, state, settings }: { connections: Connection[]; target?: Connection; state?: ConnState; settings?: VoiceSettings }) {
  const o = useOps();
  const { run, toastEl } = useAct();
  const poll = settings?.pollMinutes ?? 10;
  return (
    <Card title="發佈目的地" subtitle="發佈 = 把 .voice.json 寫進 evoleap 的課程資料庫(PUT /api/admin/scripts),線上立即生效,不用重新部署;寫完會用 API 讀回比對">
      <div className="row gap12 wrap">
        <select className="select" value={settings?.connection ?? ""} onChange={(e) => run(() => action("voice_settings", { connection: e.target.value }))}>
          {!target && <option value="">—</option>}
          {(connections.some((c) => c.id === target?.id) || !target ? connections : [target, ...connections]).map((c) => <option key={c.id} value={c.id}>{c.name + (c.isProduction ? "(正式)" : "")}</option>)}
        </select>
        {target && (state?.busy ? (
          <><SIcon name="arrow.triangle.2.circlepath" spin className="muted" /><span className="muted">{target.auth === "approval" ? "請在瀏覽器以管理員身分按「核准」" : "請在瀏覽器完成登入"}</span></>
        ) : state?.account ? (
          <><Pill text={state.account} color="green" icon="person.crop.circle.badge.checkmark" /><button className="btn" onClick={() => run(() => action("connection_logout", { id: target.id }))}>登出</button></>
        ) : <button className="btn" onClick={() => run(() => action("connection_login", { id: target.id }))}>登入「{target.name}」…</button>)}
        <span className="grow" />
        <button className="btn" onClick={() => o.go("settings", "connections")}>管理連線…</button>
      </div>
      {state?.error && <div className="caption row gap4" style={{ color: "var(--orange)" }}><SIcon name="exclamationmark.triangle.fill" size={12} />{state.error}</div>}
      <div className="row gap12 wrap">
        <span className="row gap6"><span className="muted">shuttle 專案</span><span className="caption mono" style={{ userSelect: "text" }}>{settings?.shuttlePath ?? "—"}</span></span>
        <span className="row gap6">
          <span>每 {poll} 分鐘查一次</span>
          <button className="btn small" disabled={poll <= 1} onClick={() => run(() => action("voice_settings", { pollMinutes: poll - 1 }))}>−</button>
          <button className="btn small" disabled={poll >= 120} onClick={() => run(() => action("voice_settings", { pollMinutes: poll + 1 }))}>+</button>
        </span>
      </div>
      {toastEl}
    </Card>
  );
}

function NewBatchCard() {
  const [scripts, setScripts] = useState("");
  const [all, setAll] = useState(false);
  const [limit, setLimit] = useState(0);
  const [formError, setFormError] = useState<string>();
  const [dry, setDry] = useState<string>();
  const [dryRunning, setDryRunning] = useState(false);
  const args = () => ({ ...(all ? { all: true } : { scripts: scripts.split(/[\s,]+/).filter(Boolean) }), ...(limit > 0 ? { limit } : {}) });
  const submit = async () => {
    try { await tool("ops_voice_submit", args()); setFormError(undefined); setScripts(""); }
    catch (e: any) { setFormError(String(e?.message ?? e)); }
  };
  const dryRun = async () => {
    setDryRunning(true);
    try {
      const r: any = await tool("ops_voice_submit", { ...args(), dry_run: true });
      setDry(typeof r === "string" ? r : r?.output ?? JSON.stringify(r, null, 2));
      setFormError(undefined);
    } catch (e: any) { setFormError(String(e?.message ?? e)); }
    setDryRunning(false);
  };
  return (
    <Card title="新的旁白批次" subtitle="執行 npm run voice -- --batch:缺的旁白整包送到 Gemini Batch API(約一半價格,24 小時內完成)。一次只能有一批,其餘排隊">
      <div className="row gap12">
        <input className="input grow" disabled={all} placeholder="腳本,例如 launch/2.1.json launch/2.2.json" value={scripts} onChange={(e) => setScripts(e.target.value)} />
        <Check on={all} onChange={setAll}>全部腳本</Check>
        <span className="row gap6">
          <span>{limit === 0 ? "不限段數" : `每份前 ${limit} 段`}</span>
          <button className="btn small" disabled={limit <= 0} onClick={() => setLimit(limit - 1)}>−</button>
          <button className="btn small" disabled={limit >= 200} onClick={() => setLimit(limit + 1)}>+</button>
        </span>
      </div>
      <div className="row gap8">
        {formError && <span className="caption row gap4" style={{ color: "var(--red)" }}><SIcon name="exclamationmark.triangle.fill" size={12} />{formError}</span>}
        <span className="grow" />
        <button className="btn" disabled={dryRunning} onClick={dryRun}>{dryRunning ? <SIcon name="arrow.triangle.2.circlepath" spin size={12} /> : "試算"}</button>
        <button className="btn primary" onClick={submit}>送出批次</button>
      </div>
      <Modal open={dry != null} onClose={() => setDry(undefined)} width={720}>
        <div className="card-title">試算結果(沒有呼叫 API,不花錢)</div>
        <pre className="ops-code" style={{ maxHeight: 380, overflow: "auto", userSelect: "text" }}>{dry}</pre>
        <div className="row"><span className="grow" /><button className="btn primary" onClick={() => setDry(undefined)}>關閉</button></div>
      </Modal>
    </Card>
  );
}

function VoiceJobCard({ job, target, loggedIn, playing, toggle }: { job: VoiceJob; target?: Connection; loggedIn: boolean; playing?: string; toggle: (c: VoiceClip) => void }) {
  const o = useOps();
  const [dlg, ask] = useConfirm();
  const { run, toastEl } = useAct();
  const st = STATE[job.state] ?? { label: job.state, icon: "circle" };
  const c = job.state === "ready" ? "purple" : job.state === "published" || job.state === "nothingToDo" ? "green" : job.state === "failed" ? "red" : job.state === "canceled" || job.state === "queued" ? "gray" : "blue";
  const isProd = target?.isProduction ?? true;
  const by = job.by === "external" ? "終端機送出" : job.by === "mcp" ? "MCP 用戶端" : o.person(job.by)?.name ?? job.by;
  const pending = (job.changes ?? []).filter((x) => x.publish !== "ok");
  const info = (k: string, v: string) => <span className="row gap4"><span className="muted">{k}</span><span className="num ellipsis" style={{ userSelect: "text" }}>{v}</span></span>;
  const totalAdded = (job.changes ?? []).reduce((a, x) => a + x.added.reduce((b, y) => b + y.ms, 0), 0);
  return (
    <Card>
      <div className="row gap8" style={{ alignItems: "baseline" }}>
        <span className="card-title mono">{job.id}</span>
        <span className="card-title ellipsis">{jobTitle(job)}</span>
        <Pill text={st.label} color={c} icon={st.icon} />
        <span className="grow" />
        <span className="caption muted" style={{ flex: "none" }}>{by} · {relative(job.createdAt)}</span>
      </div>
      <div className="row gap12 wrap caption" style={{ gap: 16 }}>
        {job.clipCount != null && info("段落", String(job.clipCount))}
        {job.batchName && info("批次", job.batchName)}
        {job.batchState && info("狀態", job.batchState.replace("JOB_STATE_", ""))}
        {job.checks > 0 && info("已查", `${job.checks} 次`)}
        {job.state === "processing" && job.nextCheckAt && info("下次查", Date.parse(job.nextCheckAt) <= Date.now() ? "馬上" : relative(job.nextCheckAt))}
        {job.publishedAt && info("發佈於", `${relative(job.publishedAt)} → ${job.publishedTo ?? ""}`)}
      </div>
      {job.error && (!isFinal(job.state) || job.state === "failed") && <div className="caption row gap4" style={{ color: "var(--orange)", userSelect: "text" }}><SIcon name="exclamationmark.triangle.fill" size={12} />{job.error}</div>}
      {job.changes?.length > 0 && (
        <div className="col gap6">
          <div className="row gap8"><span style={{ fontWeight: 600 }}>變動的旁白清單</span><span className="caption muted">新增 {job.changes.reduce((a, x) => a + x.added.length, 0)} 段,共 {(totalAdded / 1000).toFixed(0)} 秒</span></div>
          {job.changes.map((ch) => (
            <details key={ch.path} className="ops-disclosure">
              <summary>
                <span className="row gap8 wrap" style={{ display: "inline-flex", color: "var(--fg)" }}>
                  <span className="mono">{ch.path}</span>
                  <Pill text={ch.isNew ? "新檔" : "更新"} color={ch.isNew ? "teal" : "blue"} />
                  {ch.course && ch.lesson ? <span className="caption muted">{ch.course} / {ch.lesson}</span> : <span className="caption" style={{ color: "var(--orange)" }}>不屬於任何一課(會放進研發基地)</span>}
                  <span className="caption muted">+{ch.added.length} 段 −{ch.removed} 段 · 共 {ch.clips} 段 {(ch.totalMs / 1000).toFixed(0)} 秒</span>
                  {ch.publish === "ok" ? <Pill text="已發佈" color="green" icon="checkmark" /> : ch.publish ? <span title={ch.publish}><Pill text="失敗" color="red" icon="xmark" /></span> : null}
                </span>
              </summary>
              <div className="col gap4" style={{ padding: "4px 0 4px 16px" }}>
                {ch.added.map((clip) => (
                  <div key={clip.file} className="row gap8 caption">
                    <button className="ops-icon-btn" onClick={() => toggle(clip)}><SIcon name={playing === clip.file ? "stop.circle.fill" : "play.circle.fill"} color="var(--accent)" /></button>
                    <span className="muted num" style={{ width: 56, textAlign: "right" }}>{(clip.ms / 1000).toFixed(1)} 秒</span>
                    <span style={{ userSelect: "text" }}>{clip.text}</span>
                  </div>
                ))}
                {ch.added.length === 0 && <span className="caption muted">沒有新增段落(只有內容順序或長度改變)</span>}
              </div>
            </details>
          ))}
        </div>
      )}
      {job.failedClips?.length > 0 && (
        <details className="ops-disclosure"><summary>{job.failedClips.length} 段產生失敗(重送一次會補上)</summary>
          {job.failedClips.map((f) => <div key={f} className="caption mono" style={{ userSelect: "text" }}>{f}</div>)}
        </details>
      )}
      <div className="row gap8">
        {job.state === "processing" && <button className="btn" onClick={() => run(() => action("voice_retry", { id: job.id }))}>立即檢查</button>}
        {(job.state === "queued" || job.state === "failed") && <button className="btn" onClick={() => run(() => action("voice_cancel", { id: job.id }))}>取消</button>}
        <span className="grow" />
        {(job.state === "ready" || job.state === "publishing") && (
          <>
            {!loggedIn && <span className="caption muted">要先登入「{target?.name ?? "發佈目的地"}」才能發佈</span>}
            <button className={"btn " + (isProd ? "red" : "primary")} disabled={!loggedIn || job.state === "publishing"} onClick={() => ask({
              title: `發佈到「${target?.name ?? "?"}」?`,
              message: (isProd ? "這會直接改正式站的課程資料,學員立刻聽到新的旁白。\n" : "") + pending.map((x) => `• ${x.path}` + (x.course ? ` → ${x.course} ${x.lesson ?? ""}` : "(研發基地)")).join("\n"),
              buttons: [{ label: `發佈 ${pending.length} 份旁白清單`, danger: isProd, primary: !isProd, onClick: () => run(() => action("voice_publish", { id: job.id })) }],
            })}>
              {job.state === "publishing" ? <SIcon name="arrow.triangle.2.circlepath" spin size={12} /> : <><SIcon name="paperplane.fill" size={12} />發佈到「{target?.name ?? "?"}」</>}
            </button>
          </>
        )}
      </div>
      <details className="ops-disclosure">
        <summary>紀錄({job.log?.length ?? 0})</summary>
        <div className="col" style={{ gap: 3, paddingTop: 4 }}>
          {[...(job.log ?? [])].reverse().map((l, i) => <div key={i} className="row gap8 caption mono" style={{ alignItems: "flex-start" }}><span className="muted">{hhmm(l.date)}</span><span style={{ userSelect: "text" }}>{l.text}</span></div>)}
        </div>
      </details>
      {dlg}{toastEl}
    </Card>
  );
}
