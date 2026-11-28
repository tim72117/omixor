// 兩套 Markdown 顯示(phase 2/3 再決定合併成一套;兩者輸出的版面不同):
//   Markdown:bits.tsx 舊版,h1/p/ul 等元素,支援 ```mermaid/```wireframe/```flowchart 圖表區塊(Diagram.tsx)
//   MarkdownText(MarkdownText.tsx):omixorKit.tsx 舊版,div 排版,支援圖片
import type { ReactNode } from "react";
import { DiagramBlock, diagramKindOf } from "../Diagram";
import css from "./Markdown.module.css";
import { safeHref } from "./safeHref";

// 簡易版:標題、清單、程式碼區塊、粗體、行內程式碼、連結
function inline(s: string, key: string): ReactNode[] {
  const out: ReactNode[] = [];
  const re = /(\*\*[^*]+\*\*|`[^`]+`|\[[^\]]+\]\([^)]+\))/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let i = 0;
  while ((m = re.exec(s))) {
    if (m.index > last) out.push(s.slice(last, m.index));
    const t = m[0];
    if (t.startsWith("**")) out.push(<strong key={key + i}>{t.slice(2, -2)}</strong>);
    else if (t.startsWith("`")) out.push(<code key={key + i}>{t.slice(1, -1)}</code>);
    else {
      const mm = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(t)!;
      out.push(safeHref(mm[2]) ? <a key={key + i} href={mm[2]} target="_blank" rel="noreferrer">{mm[1]}</a> : mm[1]);
    }
    last = m.index + t.length;
    i++;
  }
  if (last < s.length) out.push(s.slice(last));
  return out;
}

export function Markdown({ text, inlineOnly }: { text: string; inlineOnly?: boolean }) {
  if (inlineOnly) {
    return <span style={{ whiteSpace: "pre-wrap" }}>{text.split("\n").map((l, i) => <span key={i}>{i > 0 && "\n"}{inline(l, `l${i}-`)}</span>)}</span>;
  }
  const lines = text.split("\n");
  const blocks: ReactNode[] = [];
  let i = 0;
  let k = 0;
  while (i < lines.length) {
    const l = lines[i];
    if (l.startsWith("```")) {
      // 語言標記取 info string 的第一個字(「```mermaid title="x"」也算 mermaid)
      const lang = (l.replace(/^`+/, "").trim().split(/\s+/)[0] ?? "").toLowerCase();
      const code: string[] = [];
      i++;
      while (i < lines.length && !lines[i].startsWith("```")) code.push(lines[i++]);
      i++;
      // 圖表區塊:```wireframe、```flowchart、flowchart 語法的 ```mermaid 畫成圖(後端渲染,用 img 顯示;見 Diagram.tsx)
      const kind = diagramKindOf(lang, code.join("\n"));
      if (kind) blocks.push(<DiagramBlock key={k++} kind={kind} source={code.join("\n")} />);
      else blocks.push(<pre key={k++}><code>{code.join("\n")}</code></pre>);
      continue;
    }
    const h = /^(#{1,6})\s+(.*)$/.exec(l);
    if (h) {
      const n = Math.min(h[1].length, 3);
      const H = (`h${n}`) as "h1" | "h2" | "h3";
      blocks.push(<H key={k++}>{inline(h[2], `h${k}`)}</H>);
      i++;
      continue;
    }
    if (/^\s*([-*]|\d+\.)\s+/.test(l)) {
      const items: string[] = [];
      const ordered = /^\s*\d+\./.test(l);
      while (i < lines.length && /^\s*([-*]|\d+\.)\s+/.test(lines[i])) items.push(lines[i++].replace(/^\s*([-*]|\d+\.)\s+/, ""));
      const L = ordered ? "ol" : "ul";
      blocks.push(<L key={k++}>{items.map((t, j) => <li key={j}>{inline(t.replace(/^\[( |x)\]\s*/, (_m, x) => (x === "x" ? "☑ " : "☐ ")), `li${k}-${j}`)}</li>)}</L>);
      continue;
    }
    if (!l.trim()) { i++; continue; }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() && !/^(#{1,6}\s|```|\s*([-*]|\d+\.)\s+)/.test(lines[i])) para.push(lines[i++]);
    blocks.push(<p key={k++}>{para.map((t, j) => <span key={j}>{j > 0 && <br />}{inline(t, `p${k}-${j}`)}</span>)}</p>);
  }
  return <div className={css.markdown}>{blocks}</div>;
}
