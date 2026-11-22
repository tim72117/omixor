import { useCopied } from "../../lib/clipboard";
import { color } from "../../lib/color";
import { Icon } from "../Icon";
import { iconButtonClass } from "../IconButton";
import s from "./CodeBlock.module.css";

/** 等寬程式碼區塊,附複製按鈕 */
export function CodeBlock({ text, maxHeight, tint }: { text: string; maxHeight?: number; tint?: string }) {
  const [copied, copy] = useCopied();
  return (
    <div className={s.code} style={{ maxHeight, color: tint ? color(tint) : undefined }}>
      <pre>{text}</pre>
      <button className={iconButtonClass.normal + " " + s.copy} title={copied ? "已複製" : "複製"} onClick={() => copy("c", text)}>
        <Icon name={copied ? "checkmark" : "doc.on.doc"} size={12} />
      </button>
    </div>
  );
}
