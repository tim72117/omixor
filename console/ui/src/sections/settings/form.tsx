// 設定頁用的 macOS 風格分組表單(對應 SwiftUI 的 Form + .formStyle(.grouped))
import type { ReactNode } from "react";
import { Icon } from "../../components/Icon";
import css from "./settings.module.css";

export function Form({ children, width = 760 }: { children: ReactNode; width?: number }) {
  return (
    <div className="page-scroll">
      <div className={css.form} style={{ maxWidth: width }}>{children}</div>
    </div>
  );
}

export function Section({ header, icon, footer, children }: { header?: ReactNode; icon?: string; footer?: ReactNode; children: ReactNode }) {
  return (
    <div className={css.formSection}>
      {header && <div className={css.formHead}>{icon && <Icon name={icon} size={12} />}{header}</div>}
      <div className={css.formBox}>{children}</div>
      {footer && <div className={css.formFoot}>{footer}</div>}
    </div>
  );
}

/** alignTop:多行內容時靠上對齊(預設置中) */
export function Row({ label, children, sub, alignTop }: { label?: ReactNode; sub?: ReactNode; children?: ReactNode; alignTop?: boolean }) {
  return (
    <div className={css.formRow + (alignTop ? " " + css.alignTop : "")}>
      {label != null && (
        <div className="label col">
          <span>{label}</span>
          {sub && <span className="caption muted">{sub}</span>}
        </div>
      )}
      {children}
    </div>
  );
}
