// 支援圖片的 Markdown 輸入框:貼上(Ctrl+V)、拖曳圖片,或按「插入圖片」選檔,
// 上傳到目前組織的附件後在游標位置插入 ![檔名](/api/attachments/…)。
import { useRef, useState } from "react";
import { uploadImage } from "../../api";
import { SIcon } from "../SIcon";
import s from "./MdTextarea.module.css";

const ACCEPT = "image/png,image/jpeg,image/gif,image/webp";

/**
 * className 加在 textarea 上(全域 .textarea 預設 min-height 160px、等寬字;要蓋過時用 textarea.x 提高權重);
 * wrapClassName 加在外層欄。
 */
export function MdTextarea({ value, onChange, placeholder, className, wrapClassName, autoFocus }: {
  value: string; onChange: (v: string) => void; placeholder?: string; className?: string; wrapClassName?: string; autoFocus?: boolean;
}) {
  const ta = useRef<HTMLTextAreaElement>(null);
  const file = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(0);
  const [error, setError] = useState<string>();
  const [over, setOver] = useState(false);
  // 上傳是非同步的:插入時用最新的值,而不是開始上傳時的值
  const latest = useRef(value);
  latest.current = value;

  const insert = async (files: File[]) => {
    const imgs = files.filter((f) => f.type.startsWith("image/"));
    if (imgs.length === 0) return;
    setError(undefined);
    const el = ta.current;
    let pos = el ? el.selectionEnd : latest.current.length;
    for (const f of imgs) {
      setBusy((n) => n + 1);
      try {
        const url = await uploadImage(f);
        const name = (f.name && f.name !== "image.png" ? f.name : "圖片").replace(/[[\]\n]/g, " ");
        const cur = latest.current;
        pos = Math.min(pos, cur.length);
        const before = cur.slice(0, pos);
        const md = `${before && !before.endsWith("\n") ? "\n" : ""}![${name}](${url})\n`;
        const next = before + md + cur.slice(pos);
        latest.current = next;
        onChange(next);
        pos += md.length;
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setBusy((n) => n - 1);
      }
    }
    requestAnimationFrame(() => { if (ta.current) { ta.current.focus(); ta.current.setSelectionRange(pos, pos); } });
  };

  return (
    <div className={"col gap4 grow " + s.root + (wrapClassName ? " " + wrapClassName : "")}>
      <textarea ref={ta} className={"textarea" + (className ? " " + className : "") + (over ? " " + s.over : "")} autoFocus={autoFocus} placeholder={placeholder} value={value}
        onChange={(e) => onChange(e.target.value)}
        onPaste={(e) => {
          const files = Array.from(e.clipboardData.files);
          if (files.some((f) => f.type.startsWith("image/"))) { e.preventDefault(); void insert(files); }
        }}
        onDragOver={(e) => { if (e.dataTransfer.types.includes("Files")) { e.preventDefault(); setOver(true); } }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          setOver(false);
          const files = Array.from(e.dataTransfer.files);
          if (files.length) { e.preventDefault(); void insert(files); }
        }} />
      <div className={"row gap8 caption muted " + s.foot}>
        <button type="button" className={"btn link " + s.insert} disabled={busy > 0} onClick={() => file.current?.click()}>
          <SIcon name="photo" size={12} /> 插入圖片
        </button>
        <span className={"ellipsis " + s.hint}>{busy > 0 ? "上傳中…" : "也可以直接貼上或拖曳圖片"}</span>
        {error && <span className={s.error}>{error}</span>}
      </div>
      <input ref={file} type="file" accept={ACCEPT} multiple hidden
        onChange={(e) => { const fs = Array.from(e.target.files ?? []); e.target.value = ""; void insert(fs); }} />
    </div>
  );
}
