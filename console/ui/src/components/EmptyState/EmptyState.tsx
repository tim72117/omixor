// 空狀態:整區置中(EmptyStateView);卡片內一行的 InlineEmpty 在 InlineEmpty.tsx
import type { ReactNode } from "react";
import { Icon } from "../Icon";
import s from "./EmptyState.module.css";

export function EmptyState({ title, icon = "doc", message }: { title: string; icon?: string; message?: ReactNode }) {
  return (
    <div className={s.empty}>
      <Icon name={icon} size={30} className="muted" />
      <div className={s.title}>{title}</div>
      {message && <div className={"muted caption " + s.message}>{message}</div>}
    </div>
  );
}
