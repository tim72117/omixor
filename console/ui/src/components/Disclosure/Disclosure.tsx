import type { DetailsHTMLAttributes, ReactNode } from "react";
import s from "./Disclosure.module.css";

/**
 * 可展開的區塊(<details>)。variant "chevron"(預設,自訂箭頭、summary 是 flex 列)或 "native"(原生三角形、小字次要色)。
 * summary 之外的 props(open、onToggle…)直接給 <details>。
 */
export function Disclosure({ summary, summaryClassName, variant = "chevron", className, children, ...p }: Omit<DetailsHTMLAttributes<HTMLDetailsElement>, "children"> & {
  summary: ReactNode; summaryClassName?: string; variant?: "chevron" | "native"; children?: ReactNode;
}) {
  return (
    <details className={s[variant] + (className ? " " + className : "")} {...p}>
      <summary className={summaryClassName}>{summary}</summary>
      {children}
    </details>
  );
}

/** 給自己寫 <details> 的地方:<details className={disclosureClass.chevron}> */
export const disclosureClass = { chevron: s.chevron, native: s.native };
