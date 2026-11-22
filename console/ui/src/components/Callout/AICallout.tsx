import type { ReactNode } from "react";
import { Icon } from "../Icon";
import s from "./Callout.module.css";

/** 紫色的 AI 說明框(原本在 bits.tsx) */
export function AICallout({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className={s.ai}>
      <Icon name="sparkles" size={13} color="var(--purple)" />
      <div className="col gap4 grow">
        <div className={"caption " + s.aiTitle}>{title}</div>
        <div className={s.aiBody}>{children}</div>
      </div>
    </div>
  );
}
