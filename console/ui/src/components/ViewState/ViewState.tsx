// view 的標準載入 / 錯誤畫面:搭配 useView / useActionQuery 的回傳值(ViewResult)。
//
//   const v = useView<Incident[]>("incidents");
//   return <ViewState view={v}>{(list) => <IncidentList list={list} />}</ViewState>;
//
//   - 還沒有資料也沒有錯誤:轉圈 +「載入中…」(variant="block" 整區置中;"inline" 一行,卡片內用)
//   - 沒有資料、讀取失敗:錯誤訊息 +「重試」
//   - 有資料但最後一次重讀失敗:照常畫資料,上面一行橘色錯誤(資料可能不是最新的)
import type { ReactNode } from "react";
import { EmptyState } from "../EmptyState";
import { ErrorText } from "../ErrorText";
import { Spinner } from "../Spinner";
import s from "./ViewState.module.css";

/** useView / useActionQuery 的回傳值(只用到這幾個欄位,也可以自己組) */
export type ViewLike<T> = { data: T | undefined; error: string | undefined; reload?: () => void };

export function ViewState<T>({ view, children, variant = "block", what = "資料" }: {
  view: ViewLike<T>;
  children: (data: T) => ReactNode;
  variant?: "block" | "inline";
  /** 錯誤標題用:「讀不到{what}」 */
  what?: string;
}) {
  const { data, error, reload } = view;
  if (data === undefined) {
    if (error === undefined) return <ViewLoading variant={variant} />;
    const retry = reload && <button className="btn small" onClick={reload}>重試</button>;
    if (variant === "inline") return <div className={"row gap8 " + s.inline}><ErrorText>讀不到{what}:{error}</ErrorText>{retry}</div>;
    return (
      <div className={s.block}>
        <EmptyState title={`讀不到${what}`} icon="exclamationmark.triangle" message={error} />
        {retry}
      </div>
    );
  }
  return (
    <>
      {error !== undefined && <div className={s.stale}><ErrorText>重新讀取失敗(顯示的可能不是最新資料):{error}</ErrorText></div>}
      {children(data)}
    </>
  );
}

/** 載入中:block 整區置中,inline 一行 */
export function ViewLoading({ variant = "block" }: { variant?: "block" | "inline" }) {
  return (
    <div className={"muted row gap6 " + (variant === "inline" ? s.inline : s.loading)}>
      <Spinner size={13} /> 載入中…
    </div>
  );
}
