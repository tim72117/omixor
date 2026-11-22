import type { ReactNode } from "react";
import css from "./Markdown.module.css";

/** Markdown 簡易顯示(Swift 的 MarkdownText):標題、清單、程式碼區塊、行內粗體/程式碼/連結/圖片 */
export function MarkdownText({ source }: { source: string }) {
  type B = { k: "h"; l: number; t: string } | { k: "li"; m: string; t: string } | { k: "code"; t: string } | { k: "p"; t: string };
  const out: B[] = [];
  let code: string[] | null = null;
  let para: string[] = [];
  const flush = () => { if (para.length) { out.push({ k: "p", t: para.join("\n") }); para = []; } };
  for (const raw of source.split("\n")) {
    const line = raw.trim();
    if (line.startsWith("```")) {
      if (code) { out.push({ k: "code", t: code.join("\n") }); code = null; } else { flush(); code = []; }
      continue;
    }
    if (code) { code.push(raw); continue; }
    if (!line) { flush(); continue; }
    const h = /^(#{1,4}) (.*)$/.exec(line);
    if (h) { flush(); out.push({ k: "h", l: h[1].length, t: h[2].trim() }); continue; }
    const b = /^[-*+] (.*)$/.exec(line);
    if (b) { flush(); out.push({ k: "li", m: "•", t: b[1] }); continue; }
    const n = /^(\d+)\. (.*)$/.exec(line);
    if (n) { flush(); out.push({ k: "li", m: n[1] + ".", t: n[2] }); continue; }
    para.push(raw);
  }
  if (code) out.push({ k: "code", t: (code as string[]).join("\n") });
  flush();
  return (
    // overflowWrap anywhere:長識別字 / 網址 / 環境變數名放不下時斷行,不撐出父層的橫向捲動
    <div className={"col gap6 " + css.text}>
      {out.map((b, i) => {
        if (b.k === "h") return <div key={i} style={{ fontWeight: 600, fontSize: b.l <= 1 ? "var(--fs-lg)" : b.l === 2 ? "var(--fs-md)" : "var(--fs-base)", marginTop: 4 }}><Inline s={b.t} /></div>;
        if (b.k === "li") return <div key={i} className="row gap6" style={{ alignItems: "baseline" }}><span className="muted" style={{ flex: "none" }}>{b.m}</span><span style={{ minWidth: 0 }}><Inline s={b.t} /></span></div>;
        if (b.k === "code") return <pre key={i} className={css.code}>{b.t}</pre>;
        return <div key={i} style={{ whiteSpace: "pre-wrap", lineHeight: 1.5 }}><Inline s={b.t} /></div>;
      })}
    </div>
  );
}

/** 能顯示的圖片網址:本機附件(/api/attachments/…)或 https 圖片 */
const imageSrcOK = (u: string) => /^\/api\/attachments\/[0-9a-f]{32}\.(png|jpg|gif|webp)$/.test(u) || /^https:\/\//.test(u);

/** Markdown 圖片:點一下開新分頁看原圖 */
function MdImage({ alt, src }: { alt: string; src: string }) {
  if (!imageSrcOK(src)) return <>{alt || src}</>;
  return (
    <a href={src} target="_blank" rel="noreferrer" title={alt ? `${alt}(點一下看原圖)` : "點一下看原圖"} style={{ display: "inline-block", maxWidth: "100%" }}>
      <img src={src} alt={alt} loading="lazy" referrerPolicy="no-referrer"
        className={css.image} />
    </a>
  );
}

function Inline({ s }: { s: string }) {
  const parts: ReactNode[] = [];
  const re = /(!\[[^\]]*\]\([^)\s]+\)|\*\*[^*]+\*\*|`[^`]+`|\[[^\]]+\]\([^)]+\))/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let k = 0;
  while ((m = re.exec(s))) {
    if (m.index > last) parts.push(s.slice(last, m.index));
    const tok = m[0];
    if (tok.startsWith("![")) {
      const mm = /^!\[([^\]]*)\]\(([^)\s]+)\)$/.exec(tok)!;
      parts.push(<MdImage key={k++} alt={mm[1]} src={mm[2]} />);
    } else if (tok.startsWith("**")) parts.push(<b key={k++}>{tok.slice(2, -2)}</b>);
    else if (tok.startsWith("`")) parts.push(<code key={k++} className={css.inlineCode}>{tok.slice(1, -1)}</code>);
    else {
      const mm = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(tok)!;
      parts.push(/^https?:/.test(mm[2]) ? <a key={k++} href={mm[2]} target="_blank" rel="noreferrer">{mm[1]}</a> : mm[1]);
    }
    last = m.index + tok.length;
  }
  if (last < s.length) parts.push(s.slice(last));
  return <>{parts}</>;
}
