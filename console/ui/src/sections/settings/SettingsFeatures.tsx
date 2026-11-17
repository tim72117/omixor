// 設定 › 功能:這個組織要開啟哪些功能(組織層,存在核心;清單也由核心提供,見 ../../features.ts)。
// 只列這台伺服器(系統層)有開放的功能;要開放別的功能請伺服器管理員到「系統」頁。只有組織管理員能改。
// 實驗功能(只有模擬、範例資料)標「實驗」,預設關。總覽、設定、系統永遠開著。
// 對應 App 的系統設定(組織層)。
import { useState } from "react";
import { action, useRun } from "../../api";
import { Icon } from "../../components/Icon";
import { Card, EmptyState, ErrorText } from "../../components/ui";
import { dateTime } from "../../fmt";
import { reloadFeatures, saveFeature, useFeatures, type FeatureState, type SystemView as SysView } from "../../features";
import { FeatureList, type FeatureItem } from "../system/FeatureList";

export function SettingsFeatures() {
  const flags = useFeatures();
  const v = flags.view;
  const { busy: reloading, error: reloadErr, run } = useRun();
  const [saving, setSaving] = useState<string>();
  const [saveErr, setSaveErr] = useState<string>();

  const byKey = new Map((v?.features ?? []).map((f) => [f.key, f]));
  const set = async (it: FeatureItem, on: boolean) => {
    const f = byKey.get(it.key);
    if (!v?.canEdit || !f || f.required) return;
    setSaving(f.key);
    setSaveErr(undefined);
    try { await saveFeature(f, on); } catch (e: any) { setSaveErr(String(e?.message ?? e)); } finally { setSaving(undefined); }
  };
  const items: FeatureItem[] = (v?.features ?? []).map((f: FeatureState) => ({
    ...f,
    warn: f.on && !f.enabled ? (!f.parent ? (flags.webOnlyTabs[f.key]?.length ? "分頁都關了,web 版只顯示 web 獨有的分頁" : "分頁都關了,不顯示") : "所屬頁面關了") : undefined,
  }));
  const hidden = v?.serverFeatures ? Object.values(v.serverFeatures).filter((x) => !x).length : 0;

  return (
    <div className="page-scroll">
      <div className="col" style={{ gap: 16, maxWidth: 860, width: "100%", margin: "0 auto", padding: 20 }}>
        <div className="row gap8" style={{ alignItems: "flex-start" }}>
          <div className="col gap4 grow">
            <div style={{ fontWeight: 600, fontSize: 15 }}>功能</div>
            <div className="caption muted">選擇這個組織要開啟哪些功能。關掉的功能不會出現在側邊欄、頁面分頁、搜尋(⌘K)、AI 助理的建議與跨頁連結。</div>
          </div>
          <button className="btn" disabled={reloading} onClick={() => run(reloadFeatures)}><Icon name="arrow.clockwise" size={12} />重新讀取</button>
        </div>
        {!v ? <EmptyState title="讀取中…" icon="switch.2" /> : <>
          <Status v={v} />
          {hidden > 0 && (
            <div className="caption muted row gap6 wrap">
              <Icon name="info.circle" size={12} />這台伺服器還有 {hidden} 項功能沒有開放,不會列在這裡;要開放請伺服器管理員到
              <button className="btn link" onClick={() => action("navigate", { section: "system" })}>系統</button>
            </div>
          )}
          {reloadErr && <ErrorText>重新讀取失敗:{reloadErr}</ErrorText>}
          {saveErr && <Card><ErrorText>{saveErr}</ErrorText></Card>}
          <FeatureList items={items} canEdit={v.canEdit} saving={saving} onSet={set} />
        </>}
      </div>
    </div>
  );
}

/** 讀取狀態、權限 */
function Status({ v }: { v: SysView }) {
  if (v.error || v.source === "fallback" || v.source === "cached") {
    const noDB = v.mode === "local" && /OMIXOR_DATABASE_URL|OPS_DATABASE_URL/.test(v.error);
    return (
      <Card>
        <div className="row gap6" style={{ color: "var(--orange)", fontWeight: 500 }}><Icon name="exclamationmark.triangle.fill" size={14} />讀不到 omixor 的功能設定</div>
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
    return <Card><div className="row gap6 muted"><Icon name="lock" size={13} />{v.readOnlyReason || "只有組織管理員可以修改;你可以看目前的設定。"}</div></Card>;
  }
  if (v.updatedBy && v.updatedAt) return <div className="caption muted">最後由 {v.updatedBy} 於 {dateTime(v.updatedAt)} 修改</div>;
  return <div className="caption muted">這個組織還沒改過,全部用預設(已實作的開、實驗的關)。</div>;
}
