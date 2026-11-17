// 事件管理:事件列表 + 詳情、值班、統計(Swift 的 IncidentsView、IncListPane、IncDeclareSheet)
import { useEffect, useMemo, useState } from "react";
import { tool, useRun, useView } from "../../api";
import { ActorAvatar, PageHeader, SearchField, StatusDot, useDirectory, useNow } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { HEALTH_COLOR, INC_STATUS_COLOR, INC_STATUS_LABEL, arr, type Alert, type Incident, type OnCallShift, type Service, type WorkItem } from "../../components/models";
import { EmptyState, ErrorText, Modal, Segmented, color } from "../../components/ui";
import { relative } from "../../fmt";
import type { SectionProps } from "../index";
import { IncDetail } from "./IncDetail";
import { IncOnCall } from "./IncOnCall";
import { IncStats } from "./IncStats";
import { useFeatureTabs } from "../../features";
import { IncCtx, SEVS, SevBadge, durationOf, fmtShort, isActive, sevLabel, sevMeaning, t, useInc, type IncData } from "./incSupport";

type Tab = "incidents" | "oncall" | "stats";
const TABS: { value: Tab; label: string }[] = [{ value: "incidents", label: "事件" }, { value: "oncall", label: "值班" }, { value: "stats", label: "統計" }];
const TAB_KEY: Record<Tab, string> = { incidents: "incidents.list", oncall: "incidents.oncall", stats: "incidents.stats" };

export default function IncidentsView({ focusId, clearFocus }: SectionProps) {
  const [incRaw, incErr] = useView<Incident[]>("incidents");
  const [alerts] = useView<Alert[]>("alerts");
  const [services] = useView<Service[]>("services");
  const [work] = useView<WorkItem[]>("work_items");
  const [onCall] = useView<OnCallShift[]>("on_call");
  const data: IncData = useMemo(() => ({
    incidents: arr(incRaw), alerts: arr(alerts), services: arr(services), workItems: arr(work), onCall: arr(onCall), loaded: incRaw !== undefined, error: incErr,
  }), [incRaw, alerts, services, work, onCall, incErr]);
  const [rawTab, setRawTab] = useState<Tab>("incidents");
  const tabs = useFeatureTabs(TABS.map((x) => x.value), rawTab, setRawTab, (x) => TAB_KEY[x]);
  const tab = tabs.shown, setTab = tabs.select;
  const [selected, setSelected] = useState<string>();
  const [declaring, setDeclaring] = useState(false);

  const open = data.incidents.filter(isActive);
  const defaultSel = () => {
    const o = [...open].sort((a, b) => a.sev - b.sev || a.startedAt.localeCompare(b.startedAt));
    return o[0]?.id ?? [...data.incidents].sort((a, b) => b.startedAt.localeCompare(a.startedAt))[0]?.id;
  };
  const select = (id: string) => { setTab("incidents"); setSelected(id); };

  useEffect(() => {
    if (focusId && data.incidents.some((i) => i.id === focusId)) { select(focusId); clearFocus(); }
    else if (focusId && data.loaded) clearFocus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusId, data.loaded]);
  useEffect(() => {
    if (!data.loaded) return;
    if (selected && data.incidents.some((i) => i.id === selected)) return;
    setSelected(defaultSel());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data.incidents.map((i) => i.id).join(",")]);

  // ⇧⌘N 宣告事件
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key.toLowerCase() === "n" && e.shiftKey && (e.metaKey || e.ctrlKey)) { e.preventDefault(); setDeclaring(true); } };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, []);

  const worst = open.length ? Math.min(...open.map((i) => i.sev)) : 4;
  const subtitle = open.length === 0 ? "目前沒有進行中的事件" : `${open.length} 起進行中 · 最高 ${sevLabel(worst)}`;

  return (
    <IncCtx.Provider value={data}>
      <div className="col" style={{ flex: 1, minHeight: 0 }}>
        <PageHeader title="事件" subtitle={subtitle} right={
          <div className="row gap8">
            <Segmented value={tab} onChange={setTab} options={TABS.filter((x) => tabs.on(x.value))} />
            <button className="btn red" title="宣告新的事件(⇧⌘N)" onClick={() => setDeclaring(true)}><Icon name="flame.fill" size={12} />宣告事件</button>
          </div>
        } />
        {incErr ? <EmptyState title="讀不到事件" icon="exclamationmark.triangle" message={incErr} />
          : tab === "incidents" ? (
            <div className="split">
              <div className="split-list" style={{ width: "clamp(260px, 32%, 360px)" }}><IncList selected={selected} onSelect={setSelected} /></div>
              <div className="split-detail">
                {selected && data.incidents.some((i) => i.id === selected) ? <IncDetail key={selected} id={selected} />
                  : <EmptyState title="選一個事件" icon="flame" message={data.incidents.length ? "從左側列表選擇事件查看詳情。" : "目前沒有任何事件紀錄。有狀況時按「宣告事件」。"} />}
              </div>
            </div>
          ) : tab === "oncall" ? <IncOnCall /> : <IncStats onSelect={select} />}
      </div>
      <DeclareSheet open={declaring} onClose={() => setDeclaring(false)} onCreated={select} />
    </IncCtx.Provider>
  );
}

// MARK: - 左側列表

function IncList({ selected, onSelect }: { selected?: string; onSelect: (id: string) => void }) {
  const { incidents, services } = useInc();
  const [scope, setScope] = useState<"all" | "active" | "resolved">("all");
  const [sevFilter, setSevFilter] = useState<Set<number>>(new Set());
  const [query, setQuery] = useState("");
  const [menu, setMenu] = useState(false);
  const svc = (id: string) => services.find((s) => s.id === id)?.name ?? id;
  const filtered = incidents.filter((i) => {
    if (scope === "active" && !isActive(i)) return false;
    if (scope === "resolved" && isActive(i)) return false;
    if (sevFilter.size && !sevFilter.has(i.sev)) return false;
    const q = query.trim().toLowerCase();
    return !q || `${i.id} ${i.title} ${i.services.map(svc).join(" ")}`.toLowerCase().includes(q);
  });
  const active = filtered.filter(isActive).sort((a, b) => a.sev - b.sev || a.startedAt.localeCompare(b.startedAt));
  const resolved = filtered.filter((i) => !isActive(i)).sort((a, b) => (b.resolvedAt ?? b.startedAt).localeCompare(a.resolvedAt ?? a.startedAt));
  const head = (title: string, n: number, c: string) => (
    <div className="list-head row gap6"><span className="dot" style={{ background: color(c), width: 6, height: 6 }} />{title}<span className="muted">{n}</span></div>
  );
  return (
    <div className="col" style={{ minHeight: "100%", background: "color-mix(in srgb, var(--bg2) 40%, transparent)" }}>
      <div className="col gap8" style={{ padding: 10, borderBottom: "1px solid var(--line)" }}>
        <SearchField value={query} onChange={setQuery} placeholder="搜尋事件、服務" />
        <div className="row gap6" style={{ position: "relative" }}>
          <div className="grow"><Segmented value={scope} onChange={setScope} options={[{ value: "all", label: "全部" }, { value: "active", label: "進行中" }, { value: "resolved", label: "已解決" }]} /></div>
          <button className="btn link" title="依嚴重程度篩選" onClick={() => setMenu(!menu)}>
            <Icon name="line.3.horizontal.decrease.circle" size={13} />{sevFilter.size ? [...sevFilter].sort().join(",") : "SEV"}
          </button>
          {menu && (
            <div className="bx-menu" style={{ right: 0, top: 28, width: 300 }} onMouseLeave={() => setMenu(false)}>
              {SEVS.map((s) => (
                <label key={s} className="menu-item">
                  <input type="checkbox" checked={sevFilter.has(s)} onChange={(e) => setSevFilter((f) => { const n = new Set(f); if (e.target.checked) n.add(s); else n.delete(s); return n; })} />
                  {sevLabel(s)} — {sevMeaning(s)}
                </label>
              ))}
              {sevFilter.size > 0 && <><div className="menu-sep" /><button className="menu-item" onClick={() => setSevFilter(new Set())}>清除 SEV 篩選</button></>}
            </div>
          )}
        </div>
      </div>
      {filtered.length === 0 ? (
        <EmptyState title={incidents.length ? "沒有符合的事件" : "沒有事件"} icon={incidents.length ? "line.3.horizontal.decrease.circle" : "checkmark.shield"}
          message={incidents.length ? "試著調整篩選條件。" : "一切正常。"} />
      ) : (
        <div>
          {active.length > 0 && head("進行中", active.length, "red")}
          {active.map((i) => <IncRow key={i.id} inc={i} on={selected === i.id} onClick={() => onSelect(i.id)} />)}
          {resolved.length > 0 && head("已解決", resolved.length, "green")}
          {resolved.map((i) => <IncRow key={i.id} inc={i} on={selected === i.id} onClick={() => onSelect(i.id)} />)}
        </div>
      )}
    </div>
  );
}

function IncRow({ inc, on, onClick }: { inc: Incident; on: boolean; onClick: () => void }) {
  const { services } = useInc();
  const now = useNow(30000);
  const act = isActive(inc);
  return (
    <div className={"list-item" + (on ? " on" : "")} onClick={onClick} style={{ gap: 5 }}>
      <div className="row gap6">
        {act ? <StatusDot c={INC_STATUS_COLOR[inc.status]} pulsing /> : <Icon name="checkmark.circle.fill" size={12} color="var(--green)" />}
        <SevBadge sev={inc.sev} />
        <span className="mono caption muted">{inc.id}</span>
        <div className="grow" />
        <span className="caption num" style={{ color: act ? undefined : "var(--fg2)" }}>{fmtShort(durationOf(inc, now))}</span>
      </div>
      <div style={{ fontWeight: 500, color: act ? undefined : "var(--fg2)" }}>{inc.title}</div>
      <div className="row gap6 caption">
        <span style={{ fontWeight: 500, color: color(INC_STATUS_COLOR[inc.status]) }}>{INC_STATUS_LABEL[inc.status]}</span>
        {inc.services.length > 0 && <><span className="faint">·</span><span className="muted ellipsis">{inc.services.map((id) => services.find((s) => s.id === id)?.name ?? id).join("、")}</span></>}
        <div className="grow" />
        {act ? <ActorAvatar actor={{ kind: "person", id: inc.commander }} size={16} /> : <span className="faint" style={{ fontSize: 10.5 }}>{relative(inc.resolvedAt ?? inc.startedAt)}</span>}
      </div>
    </div>
  );
}

// MARK: - 宣告事件

function DeclareSheet({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (id: string) => void }) {
  const { services, onCall } = useInc();
  const dir = useDirectory();
  const [title, setTitle] = useState("");
  const [sev, setSev] = useState(2);
  const [svc, setSvc] = useState<Set<string>>(new Set());
  const [commander, setCommander] = useState("");
  const [impact, setImpact] = useState("");
  const [sq, setSq] = useState("");
  const { busy, error, run, setError } = useRun();
  useEffect(() => {
    if (!open) return;
    setTitle(""); setSev(2); setSvc(new Set()); setImpact(""); setSq(""); setError(undefined);
    const now = Date.now();
    const cur = onCall.find((s) => s.role === "Primary" && t(s.start) <= now && t(s.end) > now);
    setCommander(cur?.person ?? (dir.person(dir.me) ? dir.me : dir.people[0]?.id ?? ""));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  // MCP 的 incident_declare 要求 impact,所以影響範圍也要填(Swift 版畫面可以留空)
  const can = title.trim() !== "" && commander !== "" && impact.trim() !== "";
  const list = services
    .filter((s) => !sq.trim() || s.name.toLowerCase().includes(sq.trim().toLowerCase()) || svc.has(s.id))
    .sort((a, b) => a.tier - b.tier || a.name.localeCompare(b.name));
  const submit = () => run(async () => {
    if (!can) return;
    const r = await tool<{ id?: string } | string>("incident_declare", {
      title: title.trim(), sev, impact: impact.trim(), commander, services: services.map((s) => s.id).filter((id) => svc.has(id)),
    });
    const id = typeof r === "string" ? (/INC-\d+/.exec(r)?.[0] ?? "") : r?.id ?? "";
    onClose();
    if (id) onCreated(id);
  });
  return (
    <Modal open={open} onClose={onClose} width={520}>
      <div className="row gap8">
        <Icon name="flame.fill" size={22} color="var(--red)" />
        <div className="col">
          <span className="modal-title">宣告事件</span>
          <span className="caption muted">建立後會通知值班人員,並開始記錄時間軸。</span>
        </div>
      </div>
      <label className="bx-field"><span>標題</span><input className="input" autoFocus value={title} placeholder="例如:結帳 API 回應變慢" onChange={(e) => setTitle(e.target.value)} onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && submit()} /></label>
      <label className="bx-field"><span>嚴重程度</span>
        <select className="select" value={sev} onChange={(e) => setSev(+e.target.value)}>{SEVS.map((s) => <option key={s} value={s}>{sevLabel(s)} — {sevMeaning(s)}</option>)}</select>
      </label>
      <label className="bx-field"><span>指揮官</span>
        <select className="select" value={commander} onChange={(e) => setCommander(e.target.value)}>
          <option value="">請選擇</option>
          {dir.people.map((p) => <option key={p.id} value={p.id}>{p.name}({p.role})</option>)}
        </select>
      </label>
      <label className="bx-field"><span>影響範圍</span><textarea className="input" rows={3} value={impact} placeholder="哪些使用者、什麼功能受影響" onChange={(e) => setImpact(e.target.value)} /></label>
      <div className="bx-field">
        <span>受影響的服務</span>
        {services.length === 0 ? <span className="muted">沒有可選的服務</span> : (
          <>
            {services.length > 8 && <input className="input" placeholder="搜尋服務" value={sq} onChange={(e) => setSq(e.target.value)} />}
            <div className="col gap4" style={{ maxHeight: 150, overflowY: "auto", padding: 4 }}>
              {list.map((s) => (
                <label key={s.id} className="row gap6">
                  <input type="checkbox" checked={svc.has(s.id)} onChange={(e) => setSvc((x) => { const n = new Set(x); if (e.target.checked) n.add(s.id); else n.delete(s.id); return n; })} />
                  <span className="dot" style={{ background: color(HEALTH_COLOR[s.health]) }} />{s.name}<span className="caption muted">Tier {s.tier}</span>
                </label>
              ))}
            </div>
          </>
        )}
      </div>
      <ErrorText>{error}</ErrorText>
      <div className="row gap8">
        {sev <= 2 && <span className="row gap4 caption" style={{ color: "var(--orange)" }}><Icon name="bell.and.waves.left.and.right" size={12} />SEV{sev} 會立即呼叫主要與次要值班</span>}
        <div className="grow" />
        <button className="btn" onClick={onClose}>取消</button>
        <button className="btn red" disabled={!can || busy} onClick={submit}>宣告事件</button>
      </div>
    </Modal>
  );
}
