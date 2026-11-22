// 人、AI、角色的頭像與名稱(合併原本 bits.tsx 與 omixorKit.tsx 的 ActorAvatar / ActorLabel;AgentAvatar、AuthorIcon 原本在 bits.tsx)
// 名稱一律來自 useDirectory()(App 最外層的 DirectoryProvider),不需要 OmixorProvider。
import type { ComponentType, CSSProperties } from "react";
import { color, hexColor } from "../../lib/color";
import { useDirectory } from "../../lib/directory";
import type { Actor } from "../../types";
import { Icon } from "../Icon";
import s from "./Avatar.module.css";

/** 頭像裡畫圖示的元件:預設 Icon;要 lucide 額外圖示時傳 SIcon(由呼叫端 import,免得 Avatar 綁住 SIcon 的 chunk) */
export type AvatarIcon = ComponentType<{ name?: string; size?: number; color?: string; className?: string }>;

/**
 * actor 的頭像。沒有 actor 顯示虛線圓(未指派)。
 * plainAI:AI 一律畫紫色 sparkles(omixorKit 舊行為);預設會先找同 id 的虛擬角色,找到就畫角色自己的圖示與顏色(bits 舊行為)。
 */
export function ActorAvatar({ actor, size = 22, plainAI }: { actor?: Actor | null; size?: number; plainAI?: boolean }) {
  const dir = useDirectory();
  const box: CSSProperties = { width: size, height: size };
  if (!actor) return <span className={s.avatar + " " + s.none} style={box} title="未指派" />;
  if (actor.kind === "person") {
    const p = dir.person(actor.id);
    return (
      <span className={s.avatar} title={p?.name ?? actor.id} style={{ ...box, fontSize: size * 0.45, background: hexColor(p?.colorHex) }}>
        {p?.name.slice(0, 1) ?? "?"}
      </span>
    );
  }
  if (actor.kind === "ai") {
    const ag = plainAI ? undefined : dir.agent(actor.id);
    if (ag) return <AgentAvatar agent={ag} size={size} />;
    return <AIAvatar size={size} title={dir.name(actor)} />;
  }
  return (
    <span className={s.avatar} title="系統" style={box}>
      <Icon name="gearshape" size={size * 0.5} color="white" />
    </span>
  );
}

/** 沒有特定 actor 的 AI 頭像(紫靛漸層 + sparkles),例如 AI 助理 */
export function AIAvatar({ size = 22, title, icon: I = Icon }: { size?: number; title?: string; icon?: AvatarIcon }) {
  return (
    <span className={s.avatar + " " + s.ai} title={title} style={{ width: size, height: size }}>
      <I name="sparkles" size={size * 0.5} color="white" />
    </span>
  );
}

/**
 * 頭像 + 名稱。inline:用 inline-flex(bits 舊行為,可以放在文字行裡);預設是 flex 列(omixorKit 舊行為)。
 */
export function ActorLabel({ actor, size = 18, className, inline, plainAI }: { actor?: Actor | null; size?: number; className?: string; inline?: boolean; plainAI?: boolean }) {
  const dir = useDirectory();
  return (
    <span className={"row gap6 " + s.label + (inline ? " " + s.inline : "") + (className ? " " + className : "")}>
      <ActorAvatar actor={actor} size={size} plainAI={plainAI} />
      <span className="ellipsis">{dir.name(actor)}</span>
    </span>
  );
}

/**
 * 虛擬角色的頭像:角色的 SF Symbol 與顏色(沒給顏色是藍色、沒給圖示是 person.crop.circle)。
 * icon:畫圖示的元件,角色圖示是 lucide 額外圖示時傳 SIcon。
 */
export function AgentAvatar({ agent, size = 32, icon: I = Icon }: { agent: { symbol?: string; color?: string; name?: string }; size?: number; icon?: AvatarIcon }) {
  return (
    <span className={s.avatar} title={agent.name} style={{ width: size, height: size, background: color(agent.color ?? "blue") }}>
      <I name={agent.symbol ?? "person.crop.circle"} size={size * 0.5} color="white" />
    </span>
  );
}

/** 疊在一起的頭像(最多 max 個,其餘顯示「+N」) */
export function AvatarStack({ actors, size = 20, max = 4, plainAI }: { actors: Actor[]; size?: number; max?: number; plainAI?: boolean }) {
  return (
    <span className={s.stackRow}>
      <span className={s.stack}>{actors.slice(0, max).map((a) => <ActorAvatar key={a.kind + ":" + a.id} actor={a} size={size} plainAI={plainAI} />)}</span>
      {actors.length > max && <span className={"muted " + s.more}>+{actors.length - max}</span>}
    </span>
  );
}

/**
 * Issue/文件作者的小圖示(author 字串:person id、"mcp" 或 "agent:<id>")。
 * icon:畫圖示的元件(預設 Icon,要 lucide 額外圖示時傳 SIcon);mcpScale / personScale:MCP 與人的圖示相對 size 的比例。
 */
export function AuthorIcon({ author, size = 16, icon: I = Icon, mcpScale = 1, personScale = 1 }: {
  author: string; size?: number; icon?: AvatarIcon; mcpScale?: number; personScale?: number;
}) {
  const dir = useDirectory();
  if (author.startsWith("agent:")) {
    const a = dir.agent(author.slice(6));
    if (a) return <AgentAvatar agent={a} size={size} icon={I} />;
  }
  if (author === "mcp") return <I name="sparkles" size={size * mcpScale} color="var(--purple)" />;
  return <I name="person.crop.circle" size={size * personScale} className="muted" />;
}
