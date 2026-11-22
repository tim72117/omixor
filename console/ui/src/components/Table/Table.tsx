import type { ReactNode, TableHTMLAttributes } from "react";
import s from "./Table.module.css";

/**
 * 表格:variant "data"(預設,黏表頭、整列 hover、tableClass.sel 標選取)或 "simple"(不黏表頭,tableClass.click 的列才 hover)。
 * clearHead:表頭不鋪底色(放在卡片裡時)。內容照一般 <thead>/<tbody> 寫。
 */
export function Table({ variant = "data", clearHead, className, children, ...p }: TableHTMLAttributes<HTMLTableElement> & {
  variant?: "data" | "simple"; clearHead?: boolean; children: ReactNode;
}) {
  return <table className={s[variant] + (clearHead ? " " + s.clearHead : "") + (className ? " " + className : "")} {...p}>{children}</table>;
}

/** 表格的 class:data / simple 是表格本身,clearHead 加在表格上,sel(選取)與 click(可點)加在 <tr> */
export const tableClass = { data: s.data, simple: s.simple, clearHead: s.clearHead, sel: s.sel, click: s.click };
