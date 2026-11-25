// 規則分頁:告警規則表(名稱、運算式、嚴重度、通知管道、自動修復、啟用開關)(Swift 的 MonRulesTab)
import { useEffect, useState } from "react";
import { action, useRun, useView } from "../../api";
import { cardClass } from "../../components/Card";
import { Chip } from "../../components/Chip";
import { EmptyState } from "../../components/EmptyState";
import { ErrorText } from "../../components/ErrorText";
import { Icon } from "../../components/Icon";
import { useContextMenu } from "../../components/Menu";
import { Pill } from "../../components/Pill";
import { SearchField } from "../../components/SearchField";
import { Switch } from "../../components/Switch";
import { copyText } from "../../lib/clipboard";
import { go } from "../../lib/nav";
import { SEVERITIES, SEV_COLOR, SEV_LABEL, arr, sevRank, type Alert, type AlertRule, type Automation, type Severity } from "../../models";
import css from "./monitoring.module.css";
import { canGo } from "../../features";
import { rowButtonClass } from "../../components/RowButton";
import { tableClass } from "../../components/Table";

function channelIcon(ch: string): string {
  const c = ch.toLowerCase();
  if (c.includes("slack")) return "number";
  if (c.includes("pagerduty") || c.includes("電話") || c.includes("phone")) return "phone.fill";
  if (c.includes("mail") || c.includes("信")) return "envelope.fill";
  if (c.includes("line") || c.includes("簡訊") || c.includes("sms")) return "message.fill";
  if (c.includes("webhook")) return "link";
  return "bell.fill";
}

type SortKey = "name" | "expression" | "severity";

export function RulesTab({ highlightedId, setHighlightedId }: { highlightedId?: string; setHighlightedId: (id?: string) => void }) {
  const [rulesRaw, err] = useView<AlertRule[]>("alert_rules");
  const [alertsRaw] = useView<Alert[]>("alerts");
  const [autosRaw] = useView<Automation[]>("automations");
  const rules = arr<AlertRule>(rulesRaw);
  const alerts = arr<Alert>(alertsRaw);
  const autos = arr<Automation>(autosRaw);
  const [query, setQuery] = useState("");
  const [severity, setSeverity] = useState<Severity | "">("");
  const [showDisabled, setShowDisabled] = useState(true);
  const [sort, setSort] = useState<{ key: SortKey; asc: boolean }>({ key: "severity", asc: true });
  const { error, run } = useRun();
  const menu = useContextMenu("simple");

  useEffect(() => {
    if (highlightedId) window.setTimeout(() => document.getElementById("rule-" + highlightedId)?.scrollIntoView({ block: "center" }), 100);
  }, [highlightedId]);

  // 啟用開關:omixor 文件櫃 DOC-32《console/docs/PORTING.md》 還沒有這個 action,用 alert_rule_toggle {id, enabled}(後端未完成時顯示錯誤)
  const setEnabled = (r: AlertRule, on: boolean) => { if (r.enabled !== on) run(() => action("alert_rule_toggle", { id: r.id, enabled: on })); };

  const q = query.trim().toLowerCase();
  const rows = rules
    .filter((r) => (!severity || r.severity === severity) && (showDisabled || r.enabled) && (!q || [r.name, r.expression, r.notify.join(" ")].some((s) => s.toLowerCase().includes(q))))
    .map((r) => ({
      rule: r,
      firing: alerts.filter((a) => a.state !== "resolved" && (a.rule === r.expression || a.rule === r.name || a.title.includes(r.name))).length,
      automationName: r.autoRemediation ? autos.find((a) => a.id === r.autoRemediation)?.name ?? r.autoRemediation : undefined,
    }))
    .sort((a, b) => {
      const d = sort.key === "severity" ? sevRank(a.rule.severity) - sevRank(b.rule.severity) : a.rule[sort.key].localeCompare(b.rule[sort.key]);
      return sort.asc ? d : -d;
    });

  const th = (k: SortKey, t: string, w?: number) => (
    <th className={css.thSort} style={{ width: w }} onClick={() => setSort((s) => ({ key: k, asc: s.key === k ? !s.asc : true }))}>
      {t} {sort.key === k && <Icon name={sort.asc ? "chevron.down" : "arrow.up"} size={9} />}
    </th>
  );

  if (err) return <EmptyState title="讀不到告警規則" icon="exclamationmark.triangle" message={err} />;

  return (
    <div className={"col " + css.fill + " " + css.tabBody}>
      <div className="row gap8">
        <span className="muted">{rules.length} 條規則 · {rules.filter((r) => r.enabled).length} 條啟用中</span>
        <div className="grow" />
        <select className="select" value={severity} onChange={(e) => setSeverity(e.target.value as Severity | "")}>
          <option value="">全部嚴重度</option>
          {SEVERITIES.map((s) => <option key={s} value={s}>{SEV_LABEL[s]}</option>)}
        </select>
        <label className="row gap4"><input type="checkbox" checked={showDisabled} onChange={(e) => setShowDisabled(e.target.checked)} />顯示停用</label>
        <SearchField value={query} onChange={setQuery} placeholder="搜尋規則" width={220} />
      </div>
      <ErrorText>{error}</ErrorText>
      {rulesRaw === undefined ? <div className="muted">載入中…</div> : rules.length === 0 ? (
        <EmptyState title="沒有告警規則" icon="list.bullet.rectangle" message="尚未設定任何告警規則。" />
      ) : (
        <div className={cardClass + " " + css.tableCard}>
          <table className={tableClass.simple}>
            <thead>
              <tr>
                <th className={css.w44}>啟用</th>
                {th("name", "名稱")}
                {th("expression", "運算式")}
                {th("severity", "嚴重度", 90)}
                <th>通知</th>
                <th>自動修復</th>
              </tr>
            </thead>
            <tbody>
              {rows.map(({ rule: r, firing, automationName }, i) => (
                <tr key={r.id} id={"rule-" + r.id} className={tableClass.click + (highlightedId === r.id ? " " + css.hl : i % 2 ? " " + css.odd : "")}
                  onClick={() => setHighlightedId(r.id)}
                  onContextMenu={menu.open([
                    { label: r.enabled ? "停用規則" : "啟用規則", onClick: () => setEnabled(r, !r.enabled) },
                    { label: "複製運算式", onClick: () => copyText(r.expression) },
                    ...(r.autoRemediation && canGo("automation") ? [{ label: "前往自動修復", onClick: () => go("automation", r.autoRemediation!) }] : []),
                  ])}>
                  <td><Switch small on={r.enabled} onChange={(v) => setEnabled(r, v)} /></td>
                  <td className={r.enabled ? undefined : css.half}>
                    <span className="row gap6"><span className={"ellipsis " + css.medium}>{r.name}</span>
                      {firing > 0 && <Pill text={`${firing} 觸發`} color="red" icon="bell.fill" />}</span>
                  </td>
                  <td className={"mono ellipsis " + css.expr + (r.enabled ? "" : " " + css.fg2)} title={r.expression}>{r.expression}</td>
                  <td><Pill text={SEV_LABEL[r.severity]} color={SEV_COLOR[r.severity]} /></td>
                  <td title={r.notify.join("、")}>
                    <span className="row gap4">
                      {r.notify.length === 0 ? <span className="faint">—</span> : r.notify.map((ch) => (
                        <Chip key={ch} text={ch} icon={channelIcon(ch)} mono={false} />
                      ))}
                    </span>
                  </td>
                  <td>
                    {r.autoRemediation && automationName ? (
                      <button className={rowButtonClass + " " + css.p0} title={`前往 AI 自動化:${automationName}`} onClick={canGo("automation") ? (e) => { e.stopPropagation(); go("automation", r.autoRemediation!); } : undefined}>
                        <Icon name="sparkles" size={12} color="var(--purple)" /><span className="ellipsis">{automationName}</span>
                      </button>
                    ) : <span className="faint">無</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {menu.node}
    </div>
  );
}
