import type { ReactNode } from "react";
import s from "./Field.module.css";

/** 表單欄位:<label> 包住標籤與控制項(點標籤會聚焦控制項);控制項的 .input / .select / textarea 撐滿寬度 */
export function Field({ label, children, className }: { label: ReactNode; children: ReactNode; className?: string }) {
  return <label className={s.field + (className ? " " + className : "")}><span>{label}</span>{children}</label>;
}

/** 給自己組欄位的地方:<label className={fieldClass}><span>標籤</span>…</label> */
export const fieldClass = s.field;
