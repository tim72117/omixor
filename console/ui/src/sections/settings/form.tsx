// 設定頁用的 macOS 風格分組表單(對應 SwiftUI 的 Form + .formStyle(.grouped))
import type { CSSProperties, ReactNode } from "react";
import { Icon } from "../../components/Icon";

export function Form({ children, width = 760 }: { children: ReactNode; width?: number }) {
  return (
    <div className="page-scroll">
      <div className="bx-form" style={{ maxWidth: width, margin: "0 auto", padding: 20 }}>{children}</div>
    </div>
  );
}

export function Section({ header, icon, footer, children }: { header?: ReactNode; icon?: string; footer?: ReactNode; children: ReactNode }) {
  return (
    <div className="bx-form-section">
      {header && <div className="bx-form-head">{icon && <Icon name={icon} size={12} />}{header}</div>}
      <div className="bx-form-box">{children}</div>
      {footer && <div className="bx-form-foot">{footer}</div>}
    </div>
  );
}

export function Row({ label, children, style, sub }: { label?: ReactNode; sub?: ReactNode; children?: ReactNode; style?: CSSProperties }) {
  return (
    <div className="bx-form-row" style={style}>
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
