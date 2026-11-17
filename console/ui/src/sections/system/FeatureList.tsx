// 功能開關的卡片清單:系統頁(系統層)與 設定 › 功能(組織層)共用。
// 每個頁面(大項目)一張卡片,分頁列在卡片裡;頁面關掉時分頁收合不顯示(分頁的值保留,打開頁面時再展開)。
// 實驗功能標「實驗」;必要功能標「永遠開著」、不能關。
import { Switch } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { Card, Pill } from "../../components/ui";

/** 清單裡的一項(兩層各自把自己的狀態換成這個) */
export type FeatureItem = {
  key: string;
  name: string;
  description: string;
  parent?: string;
  experimental: boolean;
  experimentalNote?: string;
  required?: boolean;
  /** 這一層自己的開關 */
  on: boolean;
  changed: boolean;
  /** 開著但實際不顯示/不開放的原因(橘色標籤) */
  warn?: string;
  /** 其他標籤 */
  tags?: { text: string; color?: string; icon?: string }[];
};

/** 核心有登記、但 web 版還沒有畫面的功能(開關只影響 macOS App) */
const NOT_IN_WEB = new Set(["help", "familyAssistant", "automation.facebook", "mcpGateway"]);

export function FeatureList({ items, canEdit, saving, onSet, offWord = "關閉" }: {
  items: FeatureItem[];
  canEdit: boolean;
  saving?: string;
  onSet: (f: FeatureItem, on: boolean) => void;
  /** 頁面關掉時的說法(系統層是「沒開放」) */
  offWord?: string;
}) {
  const pages = items.filter((f) => !f.parent);
  const kids = (key: string) => items.filter((f) => f.parent === key);
  return <>
    {pages.map((p) => {
      const tabs = kids(p.key);
      const collapsed = !p.on && !p.required;
      return (
        <Card key={p.key}>
          <Row f={p} page canEdit={canEdit} saving={saving} onSet={onSet} />
          {tabs.length > 0 && (collapsed
            ? <div className="sys-collapsed"><Icon name="chevron.right" size={11} />已{offWord},底下 {tabs.length} 個分頁也不會顯示;重新打開時分頁照原本的設定</div>
            : <div className="sys-kids">
              <div className="caption muted">分頁</div>
              {tabs.map((t) => <Row key={t.key} f={t} canEdit={canEdit} saving={saving} onSet={onSet} />)}
            </div>)}
        </Card>
      );
    })}
  </>;
}

function Row({ f, page, canEdit, saving, onSet }: { f: FeatureItem; page?: boolean; canEdit: boolean; saving?: string; onSet: (f: FeatureItem, on: boolean) => void }) {
  const on = f.on || !!f.required;
  const title = f.required ? "這個功能永遠開著" : canEdit ? (on ? "關閉" : "開啟") : "沒有權限修改";
  return (
    <div className="sys-row">
      <div className="col gap4 grow">
        <div className="row gap6 wrap">
          <span style={page ? { fontWeight: 600, fontSize: 14 } : undefined}>{f.name}</span>
          {f.experimental && <Pill text="實驗" color="purple" icon="flask" />}
          {f.required && <Pill text="永遠開著" icon="lock" />}
          {(f.tags ?? []).map((t) => <Pill key={t.text} text={t.text} color={t.color} icon={t.icon} />)}
          {f.on && f.warn && !f.required && <Pill text={f.warn} color="orange" />}
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
