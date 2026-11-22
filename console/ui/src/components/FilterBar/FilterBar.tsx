// 精簡篩選列:搜尋框 + 一排「篩選膠囊」(下拉按鈕),右側可放結果數與動作按鈕。
// 膠囊沒設定時只顯示名稱(「標籤 ▾」),設定後顯示值與 ✕(「標籤：bug ✕」,多選顯示數量);按下打開 Dropdown + MenuList(有勾選)。
// 容器寬度小於 collapseBelow(預設 600px)時,所有膠囊收成一個「篩選 (n)」按鈕,打開的面板裡逐項設定;搜尋框一直留著。
import { useEffect, useLayoutEffect, useRef, useState, type FocusEvent, type KeyboardEvent, type ReactNode } from "react";
import { Icon } from "../Icon";
import { Dropdown, MenuList, menuItemClass, menuSepClass, type MenuItem } from "../Menu";
import { SearchField } from "../SearchField";
import s from "./FilterBar.module.css";

/** 一個選項;text 是搜尋與膠囊上顯示用的純文字(label 不是字串時給) */
export type FilterOption = { value: string; label: ReactNode; text?: string };

type FilterBase = {
  /** 識別用(React key) */
  key: string;
  /** 膠囊上的名稱,例如「標籤」 */
  label: string;
  /** 選項;"sep" 是分隔線 */
  options: (FilterOption | "sep")[];
  /** 給了就在選單最上面加一個「全部」項目(= 清除) */
  allLabel?: string;
  /** 選單裡有搜尋框;預設選項超過 8 個時才有 */
  searchable?: boolean;
  /** 沒設定(等於預設值)時也在膠囊上顯示目前的值(例如「狀態：未解決」) */
  showDefault?: boolean;
  title?: string;
};
/** 單選:value 等於 defaultValue(預設 "")= 沒設定;清除 = 設回 defaultValue */
export type SingleFilter = FilterBase & { multi?: false; value: string; defaultValue?: string; onChange: (v: string) => void };
/** 多選:空陣列 = 沒設定 */
export type MultiFilter = FilterBase & { multi: true; value: string[]; onChange: (v: string[]) => void };
export type FilterDef = SingleFilter | MultiFilter;

type Search = { value: string; onChange: (v: string) => void; placeholder?: string };

const isOpt = (o: FilterOption | "sep"): o is FilterOption => o !== "sep";
const optText = (o: FilterOption) => o.text ?? (typeof o.label === "string" || typeof o.label === "number" ? String(o.label) : o.value);
export const filterActive = (f: FilterDef) => (f.multi ? f.value.length > 0 : f.value !== (f.defaultValue ?? ""));
const clearFilter = (f: FilterDef) => (f.multi ? f.onChange([]) : f.onChange(f.defaultValue ?? ""));
function valueText(f: FilterDef): string {
  const find = (v: string) => f.options.filter(isOpt).find((o) => o.value === v);
  if (f.multi) {
    if (f.value.length === 1) { const o = find(f.value[0]); return o ? optText(o) : f.value[0]; }
    return `${f.value.length} 項`;
  }
  if (!filterActive(f) && f.allLabel && !find(f.value)) return f.allLabel;
  const o = find(f.value);
  return o ? optText(o) : f.value;
}

/**
 * 篩選列。filters 裡的 null / false 會略過(例如沒有倉庫時不顯示倉庫篩選)。
 * end:右側(結果數、動作按鈕);給函式時收到目前是不是收合模式(窄的時候可以把按鈕改成只有圖示)。
 * 有任何篩選或搜尋時顯示「清除」(把篩選設回預設值、清空搜尋)。
 */
export function FilterBar({ search, filters, end, collapseBelow = 600, className }: {
  search?: Search;
  filters: (FilterDef | null | false | undefined)[];
  end?: ReactNode | ((compact: boolean) => ReactNode);
  collapseBelow?: number;
  className?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState<number>();
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    setWidth(el.getBoundingClientRect().width);
    const ro = new ResizeObserver((es) => setWidth(es[0].contentRect.width + paddingX(el)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const compact = width !== undefined && width < collapseBelow;
  const defs = filters.filter((f): f is FilterDef => !!f);
  const any = defs.some(filterActive) || !!search?.value;
  const clearAll = () => { defs.forEach(clearFilter); search?.onChange(""); };
  return (
    <div ref={ref} className={s.root + (compact ? " " + s.compact : "") + (className ? " " + className : "")}>
      {search && <SearchField className={s.search} value={search.value} onChange={search.onChange} placeholder={search.placeholder} />}
      {compact ? (
        defs.length > 0 && <CompactFilters filters={defs} clearAll={() => defs.forEach(clearFilter)} />
      ) : (
        <>
          {defs.map((f) => <FilterPill key={f.key} f={f} />)}
          {any && <button className="btn link" onClick={clearAll}>清除</button>}
        </>
      )}
      {end != null && <div className={s.end}>{typeof end === "function" ? end(compact) : end}</div>}
    </div>
  );
}

/** ResizeObserver 的 contentRect 不含內距;門檻用整個元素的寬度比 */
function paddingX(el: HTMLElement) {
  const cs = getComputedStyle(el);
  return (parseFloat(cs.paddingLeft) || 0) + (parseFloat(cs.paddingRight) || 0);
}

// MARK: - 下拉的開關(點外面、Escape、焦點離開時關閉;Escape 關閉後焦點回到按鈕)

function usePopover() {
  const [open, setOpen] = useState(false);
  const [alignRight, setAlignRight] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const btn = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!open) return;
    const h = (e: MouseEvent) => { if (!wrap.current?.contains(e.target as Node)) setOpen(false); };
    window.addEventListener("mousedown", h);
    return () => window.removeEventListener("mousedown", h);
  }, [open]);
  const toggle = () => {
    if (!open) {
      // 靠右邊放不下時往左展開
      const r = wrap.current?.getBoundingClientRect();
      setAlignRight(!!r && r.left + 300 > window.innerWidth);
    }
    setOpen(!open);
  };
  const close = () => { setOpen(false); btn.current?.focus(); };
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Escape" && open) { e.preventDefault(); e.stopPropagation(); close(); }
  };
  const onBlur = (e: FocusEvent) => {
    if (open && e.relatedTarget && !wrap.current?.contains(e.relatedTarget as Node)) setOpen(false);
  };
  return { open, toggle, close, alignRight, wrap, btn, onKeyDown, onBlur };
}

/** 上下鍵在選單項目間移動焦點(搜尋框按下鍵到第一項) */
function arrowNav(e: KeyboardEvent<HTMLElement>) {
  if (e.defaultPrevented || (e.key !== "ArrowDown" && e.key !== "ArrowUp")) return;
  const items = [...e.currentTarget.querySelectorAll<HTMLElement>("button:not(:disabled)")];
  if (items.length === 0) return;
  e.preventDefault();
  const i = items.indexOf(document.activeElement as HTMLElement);
  const next = e.key === "ArrowDown" ? (i < 0 ? 0 : Math.min(i + 1, items.length - 1)) : (i <= 0 ? (i < 0 ? 0 : -1) : i - 1);
  if (next < 0) e.currentTarget.querySelector<HTMLElement>("input")?.focus();
  else items[next].focus();
}

// MARK: - 選項清單(膠囊的下拉與收合面板共用)

function OptionList({ f, done }: { f: FilterDef; done: () => void }) {
  const [q, setQ] = useState("");
  const root = useRef<HTMLDivElement>(null);
  const opts = f.options.filter(isOpt);
  const searchable = f.searchable ?? opts.length > 8;
  const needle = q.trim().toLowerCase();
  const isOn = (v: string) => (f.multi ? f.value.includes(v) : f.value === v);
  const pick = (v: string) => {
    if (f.multi) f.onChange(isOn(v) ? f.value.filter((x) => x !== v) : [...f.value, v]);
    else f.onChange(v);
  };
  const items: MenuItem[] = [];
  if (f.allLabel && !needle) items.push({ label: f.allLabel, checked: !filterActive(f), onClick: () => clearFilter(f) }, "sep");
  for (const o of needle ? opts.filter((o) => optText(o).toLowerCase().includes(needle)) : f.options) {
    items.push(o === "sep" ? "sep" : { label: o.label, checked: isOn(o.value), onClick: () => pick(o.value) });
  }
  // 分隔線不放在頭尾、不連續
  const clean = items.filter((it, i) => it !== "sep" || (i > 0 && i < items.length - 1 && items[i - 1] !== "sep"));
  // 打開時:有搜尋框就聚焦搜尋框,否則聚焦勾選中的項目(沒有就第一項)
  useEffect(() => {
    const el = root.current;
    if (!el) return;
    const input = el.querySelector<HTMLElement>("input");
    if (input) { input.focus(); return; }
    const btns = [...el.querySelectorAll<HTMLElement>("button:not(:disabled)")];
    (btns.find((b) => b.textContent?.startsWith("✓")) ?? btns[0])?.focus();
  }, []);
  return (
    <div ref={root} className={s.options} onKeyDown={arrowNav}>
      {searchable && (
        <input className={"input " + s.find} placeholder={`搜尋${f.label}`} value={q} aria-label={`搜尋${f.label}`}
          onChange={(e) => setQ(e.target.value)} />
      )}
      <div className={s.scroll}>
        {/* 多選時點了不關閉 */}
        <MenuList items={clean} close={f.multi ? () => {} : done} />
        {clean.length === 0 && <div className={s.none}>沒有符合的項目</div>}
      </div>
    </div>
  );
}

// MARK: - 膠囊

function FilterPill({ f }: { f: FilterDef }) {
  const p = usePopover();
  const on = filterActive(f);
  const showVal = on || !!f.showDefault;
  return (
    <div ref={p.wrap} className={s.wrap} onKeyDown={p.onKeyDown} onBlur={p.onBlur}>
      <span className={s.pill + (on ? " " + s.on : "")}>
        <button ref={p.btn} className={s.main} aria-haspopup="true" aria-expanded={p.open} title={f.title} onClick={p.toggle}>
          <span className={s.name}>{f.label}{showVal ? "：" : ""}</span>
          {showVal && <span className={s.value}>{valueText(f)}</span>}
          {!on && <Icon name="chevron.down" size={11} className={s.chev} />}
        </button>
        {on && (
          <button className={s.clear} aria-label={"清除" + f.label} title={"清除" + f.label}
            onClick={() => { clearFilter(f); p.btn.current?.focus(); }}>
            <Icon name="xmark" size={11} />
          </button>
        )}
      </span>
      {p.open && (
        <Dropdown className={s.pop + (p.alignRight ? " " + s.right : "")}>
          <OptionList f={f} done={p.close} />
        </Dropdown>
      )}
    </div>
  );
}

// MARK: - 收合模式:「篩選 (n)」+ 面板(先列各篩選,點進去選值)

function CompactFilters({ filters, clearAll }: { filters: FilterDef[]; clearAll: () => void }) {
  const p = usePopover();
  const [active, setActive] = useState<string | null>(null);
  const panel = useRef<HTMLDivElement>(null);
  const last = useRef<string | null>(null);
  const n = filters.filter(filterActive).length;
  const cur = active ? filters.find((f) => f.key === active) : undefined;
  useEffect(() => { if (!p.open) setActive(null); }, [p.open]);
  // 從某個篩選回到總覽時,焦點回到那一列
  useEffect(() => {
    if (!p.open || active) return;
    const el = panel.current;
    const row = last.current && el?.querySelector<HTMLElement>(`[data-filter="${CSS.escape(last.current)}"]`);
    (row || el?.querySelector<HTMLElement>("button"))?.focus();
  }, [active, p.open]);
  const enter = (k: string) => { last.current = k; setActive(k); };
  return (
    <div ref={p.wrap} className={s.wrap} onKeyDown={p.onKeyDown} onBlur={p.onBlur}>
      <span className={s.pill + (n ? " " + s.on : "")}>
        <button ref={p.btn} className={s.main} aria-haspopup="true" aria-expanded={p.open} onClick={p.toggle}>
          <Icon name="line.3.horizontal.decrease.circle" size={13} className={s.chev} />
          <span>篩選{n ? ` (${n})` : ""}</span>
        </button>
      </span>
      {p.open && (
        <Dropdown className={s.pop + " " + s.panel + (p.alignRight ? " " + s.right : "")}>
          <div ref={panel} className={s.options} onKeyDown={arrowNav}>
            {cur ? (
              <>
                <button className={menuItemClass + " " + s.back} onClick={() => setActive(null)}>
                  <Icon name="chevron.left" size={12} />{cur.label}
                </button>
                <div className={menuSepClass} />
                <OptionList key={cur.key} f={cur} done={() => setActive(null)} />
              </>
            ) : (
              <>
                {filters.map((f) => {
                  const on = filterActive(f);
                  return (
                    <div key={f.key} className={s.panelRow}>
                      <button data-filter={f.key} className={menuItemClass + " " + s.panelItem} onClick={() => enter(f.key)}>
                        <span className={s.panelName}>{f.label}</span>
                        <span className={s.panelValue + (on ? " " + s.panelOn : "")}>{on || f.showDefault ? valueText(f) : (f.allLabel ?? "全部")}</span>
                        <Icon name="chevron.right" size={12} className={s.chev} />
                      </button>
                      {on && (
                        <button className={s.clear} aria-label={"清除" + f.label} title={"清除" + f.label}
                          onClick={() => { clearFilter(f); panel.current?.querySelector<HTMLElement>(`[data-filter="${CSS.escape(f.key)}"]`)?.focus(); }}>
                          <Icon name="xmark" size={11} />
                        </button>
                      )}
                    </div>
                  );
                })}
                {n > 0 && (
                  <>
                    <div className={menuSepClass} />
                    <button className={menuItemClass} onClick={() => { clearAll(); p.close(); }}>清除全部篩選</button>
                  </>
                )}
              </>
            )}
          </div>
        </Dropdown>
      )}
    </div>
  );
}
