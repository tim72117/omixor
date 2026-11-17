// 監控、事件、AI 自動化、文件櫃、設定、助理共用的小元件(對應 Swift 版 Components.swift 裡 ui.tsx 沒有的部分)
import { createContext, useContext, useEffect, useMemo, useState, type CSSProperties, type ReactNode } from "react";
import { useView } from "../api";
import { guardedNavigate } from "../features";
import type { Actor, SectionId } from "../types";
import { Icon } from "./Icon";
import { DiagramBlock, diagramKindOf } from "./Diagram";
import { color, Pill } from "./ui";
import "./bits.css";

// MARK: - 資料型別(Swift 模型的 JSON)

export type Person = { id: string; name: string; role: string; colorHex: number };
export type AgentLite = { id: string; name: string; symbol: string; color: string; role?: string };

export const hexColor = (n?: number) => (n == null ? "var(--gray)" : `#${n.toString(16).padStart(6, "0")}`);

// MARK: - 名稱目錄(人、AI 自動化、虛擬角色)

type Directory = {
  me: string;
  people: Person[];
  person: (id?: string) => Person | undefined;
  name: (a?: Actor | null) => string;
  agent: (id?: string) => AgentLite | undefined;
  /** Issue/文件的作者字串:person id、"mcp" 或 "agent:<id>" */
  authorName: (author: string) => string;
};

const DirCtx = createContext<Directory | null>(null);

function buildDirectory(me: string, people: Person[], autos: { id: string; name: string }[], agents: AgentLite[]): Directory {
  const person = (id?: string) => people.find((p) => p.id === id);
  const agent = (id?: string) => agents.find((a) => a.id === id);
  const name = (a?: Actor | null) => {
    if (!a) return "—";
    if (a.kind === "person") return person(a.id)?.name ?? a.id ?? "—";
    if (a.kind === "ai") {
      const au = autos.find((x) => x.id === a.id);
      if (au) return au.name;
      const ag = agent(a.id);
      if (ag) return ag.name;
      if (a.id === "mcp") return "MCP 用戶端";
      return "AI";
    }
    return "系統";
  };
  const authorName = (author: string) => {
    if (author === "routine") return "例行排程";
    if (author.startsWith("agent:")) return agent(author.slice(6))?.name ?? author;
    if (author === "mcp") return "MCP 用戶端";
    return person(author)?.name ?? author;
  };
  return { me, people, person, name, agent, authorName };
}

/** 放在 App 最外層,讓頭像與名稱共用一份資料 */
export function DirectoryProvider({ children }: { children: ReactNode }) {
  const [people] = useView<Person[]>("people");
  const [autos] = useView<{ id: string; name: string }[]>("automations");
  const [agents] = useView<{ agents: AgentLite[] }>("agents");
  const [me] = useView<{ id: string }>("me", ["orgs"]);
  const dir = useMemo(
    () => buildDirectory(me?.id ?? "", people ?? [], autos ?? [], Array.isArray(agents?.agents) ? agents!.agents : []),
    [me, people, autos, agents],
  );
  return <DirCtx.Provider value={dir}>{children}</DirCtx.Provider>;
}

export function useDirectory(): Directory {
  return useContext(DirCtx) ?? buildDirectory("", [], [], []);
}

// MARK: - 版面

export function PageHeader({ title, subtitle, right, children }: { title: string; subtitle?: ReactNode; right?: ReactNode; children?: ReactNode }) {
  return (
    <div className="bx-header">
      <div className="bx-header-row">
        <div className="col gap4 grow">
          <div className="page-title">{title}</div>
          {subtitle && <div className="muted">{subtitle}</div>}
        </div>
        {right}
      </div>
      {children}
    </div>
  );
}

export function Flow({ children, gap = 6, style }: { children: ReactNode; gap?: number; style?: CSSProperties }) {
  return <div className="row wrap" style={{ gap, ...style }}>{children}</div>;
}

// MARK: - 人與 AI

export function ActorAvatar({ actor, size = 22 }: { actor: Actor; size?: number }) {
  const dir = useDirectory();
  if (actor.kind === "person") {
    const p = dir.person(actor.id);
    return (
      <span className="bx-avatar" title={p?.name ?? actor.id} style={{ width: size, height: size, fontSize: size * 0.45, background: hexColor(p?.colorHex) }}>
        {p?.name.slice(0, 1) ?? "?"}
      </span>
    );
  }
  if (actor.kind === "ai") {
    const ag = dir.agent(actor.id);
    if (ag) return <AgentAvatar agent={ag} size={size} />;
    return (
      <span className="bx-avatar bx-ai" title={dir.name(actor)} style={{ width: size, height: size }}>
        <Icon name="sparkles" size={size * 0.5} color="white" />
      </span>
    );
  }
  return (
    <span className="bx-avatar" style={{ width: size, height: size, background: "color-mix(in srgb, var(--gray) 45%, transparent)" }}>
      <Icon name="gearshape" size={size * 0.5} color="white" />
    </span>
  );
}

export function ActorLabel({ actor, size = 18 }: { actor: Actor; size?: number }) {
  const dir = useDirectory();
  return (
    <span className="row gap6" style={{ display: "inline-flex", minWidth: 0 }}>
      <ActorAvatar actor={actor} size={size} />
      <span className="ellipsis">{dir.name(actor)}</span>
    </span>
  );
}

export function AgentAvatar({ agent, size = 32 }: { agent: { symbol?: string; color?: string; name?: string }; size?: number }) {
  return (
    <span className="bx-avatar" title={agent.name} style={{ width: size, height: size, background: color(agent.color || "blue") }}>
      <Icon name={agent.symbol || "person.crop.circle"} size={size * 0.5} color="white" />
    </span>
  );
}

/** Issue/文件作者的小圖示 */
export function AuthorIcon({ author, size = 16 }: { author: string; size?: number }) {
  const dir = useDirectory();
  if (author.startsWith("agent:")) {
    const a = dir.agent(author.slice(6));
    if (a) return <AgentAvatar agent={a} size={size} />;
  }
  if (author === "mcp") return <Icon name="sparkles" size={size} color="var(--purple)" />;
  return <Icon name="person.crop.circle" size={size} className="muted" />;
}

export function AIBadge({ text = "AI" }: { text?: string }) {
  return <Pill text={text} color="purple" icon="sparkles" />;
}

export function StatusDot({ c, pulsing }: { c: string; pulsing?: boolean }) {
  return <span className={"bx-dot" + (pulsing ? " pulse" : "")} style={{ ["--c" as string]: color(c) }} />;
}

/** 紫色的 AI 說明框 */
export function AICallout({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="bx-ai-callout">
      <Icon name="sparkles" size={13} color="var(--purple)" />
      <div className="col gap4 grow">
        <div className="caption" style={{ fontWeight: 600, color: "var(--purple)" }}>{title}</div>
        <div style={{ lineHeight: 1.5 }}>{children}</div>
      </div>
    </div>
  );
}

// MARK: - 控制項

/** 可切換的篩選膠囊(Swift 的 MonToggleChip) */
export function ToggleChip({ title, count, c, on, onClick, onContextMenu }: { title: string; count?: number; c: string; on: boolean; onClick: () => void; onContextMenu?: (e: React.MouseEvent) => void }) {
  const col = color(c);
  return (
    <button
      className="bx-chip"
      onClick={onClick}
      onContextMenu={onContextMenu}
      style={{
        background: on ? `color-mix(in srgb, ${col} 14%, transparent)` : "color-mix(in srgb, var(--fg) 4%, transparent)",
        borderColor: on ? `color-mix(in srgb, ${col} 35%, transparent)` : "var(--line)",
        color: on ? "var(--fg)" : "var(--fg2)",
      }}
    >
      <span className="dot" style={{ background: col, opacity: on ? 1 : 0.35 }} />
      {title}
      {count != null && <span className="muted num">{count}</span>}
    </button>
  );
}

export function SearchField({ value, onChange, placeholder, width, mono }: { value: string; onChange: (v: string) => void; placeholder?: string; width?: number | string; mono?: boolean }) {
  return (
    <div className="bx-search" style={{ width, maxWidth: width }}>
      <Icon name="magnifyingglass" size={13} className="muted" />
      <input className={mono ? "mono" : undefined} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
      {value && (
        <button className="bx-icon-btn" onClick={() => onChange("")} title="清除">
          <Icon name="xmark.circle.fill" size={13} className="faint" />
        </button>
      )}
    </div>
  );
}

export function Switch({ on, onChange, disabled, title, small }: { on: boolean; onChange: (v: boolean) => void; disabled?: boolean; title?: string; small?: boolean }) {
  return (
    <button
      className={"bx-switch" + (on ? " on" : "") + (small ? " small" : "")}
      disabled={disabled}
      title={title}
      onClick={(e) => { e.stopPropagation(); onChange(!on); }}
      role="switch"
      aria-checked={on}
    >
      <span />
    </button>
  );
}

export function IconButton({ icon, title, onClick, tint, disabled, size = 14 }: { icon: string; title?: string; onClick: () => void; tint?: string; disabled?: boolean; size?: number }) {
  return (
    <button className="bx-icon-btn" title={title} disabled={disabled} onClick={(e) => { e.stopPropagation(); onClick(); }}>
      <Icon name={icon} size={size} color={tint ? color(tint) : undefined} />
    </button>
  );
}

export function Spinner({ size = 14 }: { size?: number }) {
  return <span className="bx-spinner" style={{ width: size, height: size }} />;
}

export async function copyText(s: string) {
  try {
    await navigator.clipboard.writeText(s);
  } catch {
    const t = document.createElement("textarea");
    t.value = s;
    document.body.appendChild(t);
    t.select();
    document.execCommand("copy");
    t.remove();
  }
}

/** 複製後顯示「已複製」1.5 秒 */
export function useCopied(): [string | undefined, (key: string, text: string) => void] {
  const [k, setK] = useState<string>();
  useEffect(() => {
    if (!k) return;
    const t = window.setTimeout(() => setK(undefined), 1600);
    return () => window.clearTimeout(t);
  }, [k]);
  return [k, (key, text) => { copyText(text); setK(key); }];
}

/** 等寬程式碼區塊,附複製按鈕 */
export function CodeBlock({ text, maxHeight, tint }: { text: string; maxHeight?: number; tint?: string }) {
  const [copied, copy] = useCopied();
  return (
    <div className="bx-code" style={{ maxHeight, color: tint ? color(tint) : undefined }}>
      <pre>{text}</pre>
      <button className="bx-icon-btn bx-code-copy" title={copied ? "已複製" : "複製"} onClick={() => copy("c", text)}>
        <Icon name={copied ? "checkmark" : "doc.on.doc"} size={12} />
      </button>
    </div>
  );
}

export function IconTile({ icon, size = 34, dimmed }: { icon: string; size?: number; dimmed?: boolean }) {
  return (
    <span className={"bx-tile" + (dimmed ? " dim" : "")} style={{ width: size, height: size, borderRadius: size * 0.26 }}>
      <Icon name={icon} size={size * 0.48} color="white" />
    </span>
  );
}

export function Chip({ text, icon, mono = true }: { text: string; icon?: string; mono?: boolean }) {
  return (
    <span className={"bx-tag" + (mono ? " mono" : "")}>
      {icon && <Icon name={icon} size={10} />}
      {text}
    </span>
  );
}

/** 載入狀態:還沒讀到、讀取失敗(後端還沒做好時顯示錯誤而不是壞掉) */
export function ViewState({ error, loading, what }: { error?: string; loading?: boolean; what?: string }) {
  if (error)
    return (
      <div className="empty">
        <Icon name="exclamationmark.triangle" size={28} color="var(--orange)" />
        <div className="empty-title">{what ? `讀不到${what}` : "讀取失敗"}</div>
        <div className="muted caption" style={{ maxWidth: 420, textAlign: "center" }}>{error}</div>
      </div>
    );
  if (loading) return <div className="muted" style={{ padding: 20 }}>載入中…</div>;
  return null;
}

// MARK: - 右鍵選單

export type MenuItem = { label: string; onClick: () => void; disabled?: boolean } | "-";

/** 用法:const menu = useContextMenu(); <div onContextMenu={menu.open(items)}>…</div>{menu.node} */
export function useContextMenu() {
  const [st, setSt] = useState<{ x: number; y: number; items: MenuItem[] }>();
  useEffect(() => {
    if (!st) return;
    const close = () => setSt(undefined);
    window.addEventListener("click", close);
    window.addEventListener("blur", close);
    window.addEventListener("keydown", close);
    return () => { window.removeEventListener("click", close); window.removeEventListener("blur", close); window.removeEventListener("keydown", close); };
  }, [st]);
  const open = (items: MenuItem[]) => (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setSt({ x: Math.min(e.clientX, window.innerWidth - 220), y: Math.min(e.clientY, window.innerHeight - items.length * 28 - 10), items });
  };
  const node = st ? (
    <div className="bx-menu" style={{ position: "fixed", left: st.x, top: st.y }} onClick={(e) => e.stopPropagation()}>
      {st.items.map((it, i) =>
        it === "-" ? <div key={i} className="menu-sep" /> : (
          <button key={i} className="menu-item" disabled={it.disabled} style={it.disabled ? { opacity: 0.4, pointerEvents: "none" } : undefined}
            onClick={() => { setSt(undefined); it.onClick(); }}>{it.label}</button>
        ),
      )}
    </div>
  ) : null;
  return { open, node };
}

// MARK: - Markdown(簡易版:標題、清單、程式碼區塊、粗體、行內程式碼、連結)

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
      out.push(<a key={key + i} href={mm[2]} target="_blank" rel="noreferrer">{mm[1]}</a>);
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
  return <div className="markdown">{blocks}</div>;
}

// MARK: - 格式化(對應 Swift 的 Fmt)

/** HH:mm */
export const hm = (iso?: string | null) => (iso ? new Date(iso).toLocaleTimeString("zh-TW", { hour: "2-digit", minute: "2-digit", hour12: false }) : "—");

/** Swift Fmt.duration:45s、3m 2s、1h 5m */
export function shortDuration(sec?: number | null): string {
  if (sec == null) return "—";
  const s = Math.floor(sec);
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

/** Swift Fmt.number:1.2k、3.4M */
export function num(v: number): string {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
  return v.toFixed(0);
}

export const percent = (v: number, digits = 1) => `${v.toFixed(digits)}%`;

/** 每隔幾秒重畫一次(倒數、持續時間用) */
export function useNow(ms = 30000): number {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), ms);
    return () => window.clearInterval(t);
  }, [ms]);
  return now;
}

/** 存在 localStorage 的狀態(對應 @AppStorage) */
export function useStored<T>(key: string, initial: T): [T, (v: T) => void] {
  const [v, setV] = useState<T>(() => {
    try {
      const s = localStorage.getItem(key);
      return s == null ? initial : (JSON.parse(s) as T);
    } catch {
      return initial;
    }
  });
  const set = (x: T) => {
    setV(x);
    try { localStorage.setItem(key, JSON.stringify(x)); } catch { /* 無法儲存就只放在記憶體 */ }
  };
  return [v, set];
}

/** 跳到別頁並選中項目(Swift 的 app.go);系統設定關掉的頁面不跳,改在畫面上方提示 */
export const go = (section: SectionId, focusId?: string) => guardedNavigate(section, focusId);
