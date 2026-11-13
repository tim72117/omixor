// 系統:開關側邊欄的主要功能與頁內分頁(組織層級,存在核心;清單也由核心提供,見 ../../features.ts)。
// 實驗功能(只有模擬、範例資料)標「實驗」,預設關。總覽、設定、系統永遠開著。只有組織管理員能改。
// 頁面(大項目)關掉時,底下分頁的開關收合不顯示(分頁的值保留,打開頁面時再展開)。
// 對應 App 的 Features/System/SystemView.swift。
import { useState } from "react";
import { useRun } from "../../api";
import { PageHeader, Switch } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { Card, EmptyState, ErrorText, PageScroll, Pill } from "../../components/ui";
import { dateTime } from "../../fmt";
import { reloadFeatures, saveFeature, useFeatures, type FeatureState, type SystemView as SysView } from "../../features";
import type { SectionProps } from "../index";

/** 核心有登記、但 web 版還沒有畫面的功能(開關只影響 macOS App) */
const NOT_IN_WEB = new Set(["help", "familyAssistant", "automation.facebook"]);

export default function SystemView(_: SectionProps) {
  const flags = useFeatures();
  const v = flags.view;
  const { busy: reloading, error: reloadErr, run } = useRun();
  const [saving, setSaving] = useState<string>();
  const [saveErr, setSaveErr] = useState<string>();

  const set = async (f: FeatureState, on: boolean) => {
    if (!v?.canEdit || f.required) return;
    setSaving(f.key);
    setSaveErr(undefined);
    try { await saveFeature(f, on); } catch (e: any) { setSaveErr(String(e?.message ?? e)); } finally { setSaving(undefined); }
  };

  const pages = (v?.features ?? []).filter((f) => !f.parent);
  const kids = (key: string) => (v?.features ?? []).filter((f) => f.parent === key);

  return (
    <div className="col" style={{ flex: 1, minHeight: 0 }}>
      <PageHeader
        title="系統"
        subtitle="選擇這個組織要開啟哪些功能。關掉的功能不會出現在側邊欄、頁面分頁、搜尋(⌘K)、AI 助理的建議與跨頁連結。"
        right={
          <div className="row gap8">
            {v && <Edition v={v} />}
            <button className="btn" disabled={reloading} onClick={() => run(reloadFeatures)}><Icon name="arrow.clockwise" size={12} />重新讀取</button>
          </div>
        }
      />
      <PageScroll>
        <div className="col" style={{ gap: 16, maxWidth: 860, width: "100%", margin: "0 auto" }}>
          {!v ? <EmptyState title="讀取中…" icon="switch.2" /> : <>
            <Status v={v} />
            {reloadErr && <ErrorText>重新讀取失敗:{reloadErr}</ErrorText>}
            {saveErr && <Card><ErrorText>{saveErr}</ErrorText></Card>}
            {pages.map((p) => {
              const tabs = kids(p.key);
              const collapsed = !p.on && !p.required;
              return (
                <Card key={p.key}>
                  <Row f={p} page canEdit={v.canEdit} saving={saving} onSet={set} />
                  {tabs.length > 0 && (collapsed
                    ? <div className="sys-collapsed"><Icon name="chevron.right" size={11} />已關閉,底下 {tabs.length} 個分頁也不會顯示;重新打開時分頁照原本的設定</div>
                    : <div className="sys-kids">
                      <div className="caption muted">分頁</div>
                      {tabs.map((t) => <Row key={t.key} f={t} canEdit={v.canEdit} saving={saving} onSet={set} />)}
                    </div>)}
                </Card>
              );
            })}
          </>}
        </div>
      </PageScroll>
    </div>
  );
}

/** 目前版本:核心(server 或本機核心)的版本;web 程式的版本不同時也列出。任一邊是開發版時加「開發版」標籤 */
function Edition({ v }: { v: SysView }) {
  const dev = v.edition === "dev" || v.webEdition === "dev";
  const label = v.editionLabel ?? v.edition ?? "—";
  const differ = v.webEdition && v.edition && v.webEdition !== v.edition;
  return (
    <span className="row gap6 caption muted" title={v.mode === "remote" ? "omixor server 編譯的版本決定有哪些功能" : "本機核心編譯的版本決定有哪些功能"}>
      目前版本:{label}{differ ? `(web 程式:${v.webEditionLabel ?? v.webEdition})` : ""}
      {dev && <Pill text="開發版" color="indigo" icon="hammer" />}
    </span>
  );
}

/** 讀取狀態、權限 */
function Status({ v }: { v: SysView }) {
  if (v.error || v.source === "fallback" || v.source === "cached") {
    const noDB = v.mode === "local" && /OPS_DATABASE_URL/.test(v.error);
    return (
      <Card>
        <div className="row gap6" style={{ color: "var(--orange)", fontWeight: 500 }}><Icon name="exclamationmark.triangle.fill" size={14} />讀不到 omixor 的系統設定</div>
        {v.error && <div style={{ userSelect: "text" }}>{v.error}</div>}
        {noDB && <div className="caption muted">本機模式的功能開關存在本機核心的資料庫(和 Issue、事件同一個);沒有資料庫時各裝置都只能用預設。</div>}
        <div className="caption muted">
          {v.source === "cached" ? "目前照這次執行最後一次讀到的設定顯示,暫時不能修改。" : "目前用預設:已實作的功能開、實驗功能關,暫時不能修改。"}
        </div>
      </Card>
    );
  }
  if (v.source === "loading") return <div className="caption muted">讀取中…(先照{v.saved ? "最後一份" : "預設"}顯示)</div>;
  if (!v.canEdit) {
    return <Card><div className="row gap6 muted"><Icon name="lock" size={13} />只有組織管理員可以修改;你可以看目前的設定。</div></Card>;
  }
  if (v.updatedBy && v.updatedAt) return <div className="caption muted">最後由 {v.updatedBy} 於 {dateTime(v.updatedAt)} 修改</div>;
  return <div className="caption muted">這個組織還沒改過,全部用預設(已實作的開、實驗的關)。</div>;
}

function Row({ f, page, canEdit, saving, onSet }: { f: FeatureState; page?: boolean; canEdit: boolean; saving?: string; onSet: (f: FeatureState, on: boolean) => void }) {
  const on = f.on || !!f.required;
  const title = f.required ? "這個功能永遠開著" : canEdit ? (on ? "關閉" : "開啟") : "只有組織管理員可以修改";
  return (
    <div className="sys-row">
      <div className="col gap4 grow">
        <div className="row gap6 wrap">
          <span style={page ? { fontWeight: 600, fontSize: 14 } : undefined}>{f.name}</span>
          {f.edition === "dev" && <Pill text="開發版" color="indigo" icon="hammer" />}
          {f.experimental && <Pill text="實驗" color="purple" icon="flask" />}
          {f.required && <Pill text="永遠開著" icon="lock" />}
          {f.on && !f.enabled && !f.required && <Pill text={page ? "分頁都關了,不顯示" : "所屬頁面關了"} color="orange" />}
          {f.changed && <span className="caption muted">已改過</span>}
        </div>
        {f.description && <div className="caption muted">{f.description}</div>}
        {f.experimental && f.experimentalNote && <div className="caption" style={{ color: "var(--purple)" }}>{f.experimentalNote}</div>}
        {NOT_IN_WEB.has(f.key) && <div className="caption muted">web 版還沒有這個{page ? "頁面" : "分頁"},開關只影響 macOS App。</div>}
      </div>
      {saving === f.key && <span className="caption muted">儲存中…</span>}
      <Switch on={on} title={title} disabled={!!f.required || !canEdit || saving !== undefined} onChange={(x) => onSet(f, x)} />
    </div>
  );
}
