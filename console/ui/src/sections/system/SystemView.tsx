// 系統:這台伺服器開放哪些功能(系統層,整台伺服器共用,存在核心的資料庫;清單也由核心提供,見 ../../features.ts)。
// 只有伺服器管理員能改,其他人唯讀。沒開放的功能就像沒有這個功能:各組織的「設定 › 功能」不列、不能開。
// 原本只有開發版的功能(實驗、範例資料、家庭助理、外部 MCP 閘道等)預設不開放;總覽、設定、系統永遠開放。
// 組織要用哪些功能(組織層)在 設定 › 功能(../settings/SettingsFeatures.tsx)。
// 對應 App 的 Features/System/SystemView.swift。
import { useState } from "react";
import { useRun } from "../../api";
import { go } from "../../lib/nav";
import { Card } from "../../components/Card";
import { EmptyState } from "../../components/EmptyState";
import { ErrorText } from "../../components/ErrorText";
import { Icon } from "../../components/Icon";
import { PageScroll } from "../../components/Layout";
import { PageHeader } from "../../components/PageHeader";
import { dateTime } from "../../fmt";
import { reloadFeatures, saveServerFeature, useFeatures, type ServerFeatureState, type ServerView } from "../../features";
import type { SectionProps } from "../index";
import { FeatureList, type FeatureItem } from "./FeatureList";
import css from "./system.module.css";

export default function SystemView(_: SectionProps) {
  const flags = useFeatures();
  const sv = flags.view?.server;
  const { busy: reloading, error: reloadErr, run } = useRun();
  const [saving, setSaving] = useState<string>();
  const [saveErr, setSaveErr] = useState<string>();

  const byKey = new Map((sv?.features ?? []).map((f) => [f.key, f]));
  const set = async (it: FeatureItem, on: boolean) => {
    const f = byKey.get(it.key);
    if (!sv?.canEdit || !f || f.required) return;
    setSaving(f.key);
    setSaveErr(undefined);
    try { await saveServerFeature(f, on); } catch (e: any) { setSaveErr(String(e?.message ?? e)); } finally { setSaving(undefined); }
  };
  const items: FeatureItem[] = (sv?.features ?? []).map((f: ServerFeatureState) => ({
    ...f,
    warn: f.on && !f.available ? "所屬頁面沒開放" : undefined,
    tags: f.systemDefault || f.required ? [] : [{ text: "預設不開放", color: "indigo", icon: "hammer" }],
  }));

  return (
    <div className={"col " + css.page}>
      <PageHeader
        title="系統"
        subtitle="選擇這台伺服器開放哪些功能(所有組織共用)。沒開放的功能各組織都看不到、也不能開;各組織要用哪些,由組織管理員在 設定 › 功能 決定。"
        right={
          <div className="row gap8">
            <button className="btn" onClick={() => go("settings", "features")}><Icon name="switch.2" size={12} />設定 › 功能</button>
            <button className="btn" disabled={reloading} onClick={() => run(reloadFeatures)}><Icon name="arrow.clockwise" size={12} />重新讀取</button>
          </div>
        }
      />
      <PageScroll>
        <div className={"col " + css.body}>
          {!sv ? <EmptyState title="讀取中…" icon="switch.2" /> : <>
            <Status sv={sv} />
            {reloadErr && <ErrorText>重新讀取失敗:{reloadErr}</ErrorText>}
            {saveErr && <Card><ErrorText>{saveErr}</ErrorText></Card>}
            <FeatureList items={items} canEdit={sv.canEdit} saving={saving} onSet={set} offWord="不開放" />
          </>}
        </div>
      </PageScroll>
    </div>
  );
}

/** 讀取狀態、權限 */
function Status({ sv }: { sv: ServerView }) {
  if (sv.error || sv.source === "fallback" || sv.source === "cached") {
    const noDB = /OMIXOR_DATABASE_URL|OPS_DATABASE_URL/.test(sv.error);
    return (
      <Card>
        <div className={"row gap6 " + css.warn}><Icon name="exclamationmark.triangle.fill" size={14} />讀不到這台伺服器開放的功能</div>
        {sv.error && <div className={css.selectable}>{sv.error}</div>}
        {noDB && <div className="caption muted">功能開關存在核心的資料庫(和 Issue、事件同一個)。</div>}
        <div className="caption muted">
          {sv.source === "cached" ? "目前照這次執行最後一次讀到的設定顯示,暫時不能修改。" : "目前顯示預設:原本正式版的功能開放、其他不開放,暫時不能修改。"}
        </div>
      </Card>
    );
  }
  if (sv.source === "loading") return <div className="caption muted">讀取中…(先照{sv.saved ? "最後一份" : "預設"}顯示)</div>;
  if (!sv.canEdit) {
    return <Card><div className="row gap6 muted"><Icon name="lock" size={13} />{sv.readOnlyReason || "只有伺服器管理員可以修改這台伺服器開放的功能;你可以看目前的設定。組織要用哪些功能請到 設定 › 功能。"}</div></Card>;
  }
  if (sv.updatedBy && sv.updatedAt) return <div className="caption muted">最後由 {sv.updatedBy} 於 {dateTime(sv.updatedAt)} 修改</div>;
  return <div className="caption muted">這台伺服器還沒改過,全部用預設(原本正式版的功能開放、實驗與還在實作中的功能不開放)。</div>;
}
