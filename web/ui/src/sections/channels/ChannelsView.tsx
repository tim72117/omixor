// 對話:把組織成員與虛擬角色拉進同一個頻道溝通(核心的訊息頻道,規則見 omixor-core 的 service/channel.go)。
// 左邊頻道清單(未讀數)、右邊訊息串(Markdown)、下方輸入框(Enter 送出、Shift+Enter 換行、打 @ 選成員)、頻道設定(名稱、主題、成員)。
// @ 角色只通知、不排任務:角色下次執行時在 agent_context 的 mentions 看得到。
// 更新:每 5 秒,或後端通知 channels 有變動時,讀這個頻道的新訊息;打開頻道、收到新訊息時標已讀。
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { action, useView } from "../../api";
import { AgentAvatar, go, Markdown, SearchField } from "../../components/bits";
import { Icon } from "../../components/Icon";
import type { Agent, Channel, ChannelMessage, ChannelPage, ChannelsView as ChannelsData } from "../../components/models";
import { EmptyState, ErrorText, Modal, Pill } from "../../components/ui";
import { relative } from "../../fmt";
import type { SectionProps } from "../index";
import { canGo } from "../../features";

const POLL_MS = 5000;
const PAGE = 50;

type Who = { node: string; name: string; agent?: Agent };

/** 節點 → 顯示資訊(角色名稱與頭像、成員名稱或 email) */
function useDirectoryOf(data?: ChannelsData) {
  return useMemo(() => {
    const agents = new Map((data?.agents ?? []).map((a) => [a.id, a]));
    const members = new Map((data?.members ?? []).map((m) => [m.email.toLowerCase(), m]));
    const who = (node: string): Who => {
      if (node.startsWith("agent:")) {
        const a = agents.get(node.slice(6));
        return { node, name: a?.name ?? node.slice(6), agent: a };
      }
      if (node.startsWith("user:")) {
        const e = node.slice(5);
        const m = members.get(e);
        return { node, name: m?.name || e };
      }
      return { node, name: node };
    };
    // 可以加進頻道的對象:角色與組織成員
    const candidates: Who[] = [
      ...(data?.agents ?? []).map((a) => ({ node: `agent:${a.id}`, name: a.name, agent: a })),
      ...(data?.members ?? []).map((m) => ({ node: `user:${m.email.toLowerCase()}`, name: m.name || m.email })),
    ];
    return { who, candidates };
  }, [data]);
}

function Avatar({ w, size = 28 }: { w: Who; size?: number }) {
  if (w.node.startsWith("agent:")) return <AgentAvatar agent={w.agent ?? { name: w.name }} size={size} />;
  return <Icon name="person.crop.circle" size={size} className="muted" />;
}

export default function ChannelsView({ focusId, clearFocus }: SectionProps) {
  const [data, err] = useView<ChannelsData>("channels", ["orgs", "agents"]);
  const channels = Array.isArray(data?.channels) ? data!.channels : [];
  const dir = useDirectoryOf(data);
  const [selected, setSelected] = useState<string>();
  const [query, setQuery] = useState("");
  const [creating, setCreating] = useState(false);

  // 從其他頁跳過來(例如 Issue 的「開對話頻道」)時選中那個頻道
  useEffect(() => {
    if (!focusId || !data) return;
    const c = channels.find((x) => x.id.toLowerCase() === focusId.toLowerCase());
    if (c) setSelected(c.id);
    clearFocus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusId, data ? 1 : 0]);
  useEffect(() => {
    if (!selected && channels.length) setSelected(channels[0].id);
  }, [channels.length, selected]);

  if (err || data?.error) return <EmptyState title="讀不到對話" icon="exclamationmark.triangle" message={err ?? data?.error} />;
  if (!data) return <div className="muted" style={{ padding: 20 }}>載入中…</div>;

  const q = query.trim().toLowerCase();
  const list = channels.filter((c) => !q || c.name.toLowerCase().includes(q) || c.topic.toLowerCase().includes(q) || c.issueId.toLowerCase().includes(q));
  const current = channels.find((c) => c.id === selected);

  return (
    <div className="split" style={{ height: "100%" }}>
      <div className="split-list col" style={{ width: 280 }}>
        <div className="row gap6" style={{ padding: "10px 12px" }}>
          <SearchField value={query} onChange={setQuery} placeholder="搜尋頻道" width="100%" />
          <button className="btn" title="新增頻道" onClick={() => setCreating(true)}><Icon name="plus" size={14} /></button>
        </div>
        <div className="ops-list">
          {list.length === 0 && <div className="muted caption" style={{ padding: 16 }}>{channels.length ? "沒有符合的頻道" : "還沒有頻道,按 + 建立一個"}</div>}
          {list.map((c) => (
            <div key={c.id} className={"ops-list-row col gap4" + (c.id === selected ? " on" : "")} onClick={() => setSelected(c.id)}>
              <div className="row gap6">
                <Icon name="number" size={13} className="muted" />
                <span className="ellipsis grow" style={{ fontWeight: c.unread ? 600 : undefined }}>{c.name}</span>
                {c.unread > 0 && <span className="pill" style={{ background: "var(--accent)", color: "white", borderRadius: 9, padding: "0 6px", fontSize: 11 }}>{c.unread}</span>}
              </div>
              <div className="row gap6 caption muted">
                <span className="mono">{c.id}</span>
                {c.issueId && <span className="mono">· {c.issueId}</span>}
                <span>· {c.members.length} 位</span>
                <span className="grow" />
                <span>{relative(c.lastMessageAt ?? c.updatedAt)}</span>
              </div>
            </div>
          ))}
        </div>
      </div>
      <div className="split-detail" style={{ display: "flex", flexDirection: "column", minHeight: 0 }}>
        {current
          ? <ChannelPane key={current.id} channel={current} me={data.me} dir={dir} />
          : <EmptyState title="選一個頻道" icon="bubble.left.and.bubble.right" message="頻道把人與虛擬角色拉進同一個對話;@ 角色只通知,不會排任務。" />}
      </div>
      <Modal open={creating} onClose={() => setCreating(false)} title="新增頻道" width={560}>
        {creating && <ChannelForm dir={dir} me={data.me} onDone={(id) => { setCreating(false); if (id) setSelected(id); }} />}
      </Modal>
    </div>
  );
}

type Dir = ReturnType<typeof useDirectoryOf>;

function ChannelPane({ channel, me, dir }: { channel: Channel; me: string; dir: Dir }) {
  const [messages, setMessages] = useState<ChannelMessage[]>([]);
  const [hasOlder, setHasOlder] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string>();
  const [settings, setSettings] = useState(false);
  const lastId = useRef<string>();
  const readUpTo = useRef<string>();
  const bottom = useRef<HTMLDivElement>(null);
  const isMember = channel.members.includes(me);

  // 讀新訊息(第一次是最新的一頁;之後是 lastId 之後的)
  const fetchNew = async () => {
    try {
      const since = lastId.current;
      const p = await action<ChannelPage>("channel_messages", since ? { id: channel.id, since, limit: 200 } : { id: channel.id, limit: PAGE });
      setError(undefined);
      if (!since) setHasOlder(p.hasMore);
      if (p.messages.length) {
        lastId.current = p.messages[p.messages.length - 1].id;
        setMessages((old) => {
          const seen = new Set(old.map((m) => m.id));
          return [...old, ...p.messages.filter((m) => !seen.has(m.id))];
        });
      }
      setLoaded(true);
    } catch (e: any) {
      setError(String(e?.message ?? e));
    }
  };
  const loadOlder = async () => {
    if (!messages.length) return;
    try {
      const p = await action<ChannelPage>("channel_messages", { id: channel.id, before: messages[0].id, limit: PAGE });
      setHasOlder(p.hasMore);
      setMessages((old) => [...p.messages, ...old]);
    } catch (e: any) {
      setError(String(e?.message ?? e));
    }
  };

  useEffect(() => {
    fetchNew();
    const t = window.setInterval(fetchNew, POLL_MS);
    return () => window.clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channel.id]);
  // 後端通知有變動(別人發了訊息、角色回覆)時:頻道的最後訊息時間變了就讀新的
  useEffect(() => { if (loaded) fetchNew(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [channel.lastMessageAt]);
  // 有新訊息:捲到底、標已讀
  const newest = messages.length ? messages[messages.length - 1].id : undefined;
  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" });
    if (newest && isMember && readUpTo.current !== newest) {
      readUpTo.current = newest;
      action("channel_read", { id: channel.id, upTo: newest }).catch(() => undefined);
    }
  }, [newest, isMember, channel.id]);

  const send = async (text: string) => {
    await action("channel_post", { id: channel.id, text });
    await fetchNew();
  };

  return (
    <>
      <div className="row gap8" style={{ padding: "12px 16px", borderBottom: "1px solid var(--line)" }}>
        <div className="col gap4 grow" style={{ minWidth: 0 }}>
          <div className="row gap8" style={{ alignItems: "baseline" }}>
            <span className="h2 ellipsis">{channel.name}</span>
            <span className="mono muted caption">{channel.id}</span>
            {channel.issueId && canGo("development") && <button className="btn" style={{ padding: "0 6px", fontSize: 12 }} title="打開 Issue" onClick={() => go("development", channel.issueId)}><Icon name="link" size={11} /> {channel.issueId}</button>}
          </div>
          {channel.topic && <span className="caption muted ellipsis">{channel.topic}</span>}
        </div>
        <div className="row" style={{ gap: 2 }}>
          {channel.members.slice(0, 8).map((n) => { const w = dir.who(n); return <span key={n} title={w.name}><Avatar w={w} size={22} /></span>; })}
          {channel.members.length > 8 && <span className="caption muted">+{channel.members.length - 8}</span>}
        </div>
        <button className="btn" title="頻道設定" onClick={() => setSettings(true)}><Icon name="gearshape" size={14} /></button>
      </div>
      <div className="col" style={{ flex: 1, minHeight: 0, overflowY: "auto", padding: "12px 16px", gap: 12 }}>
        {hasOlder && <button className="btn" style={{ alignSelf: "center" }} onClick={loadOlder}>載入更早的訊息</button>}
        {loaded && messages.length === 0 && <div className="muted caption" style={{ textAlign: "center", padding: 24 }}>還沒有訊息。用 @ 提到角色,它下次執行時會看到。</div>}
        {messages.map((m) => <MessageRow key={m.id} m={m} me={me} dir={dir} />)}
        <div ref={bottom} />
      </div>
      {error && <div style={{ padding: "0 16px" }}><ErrorText>{error}</ErrorText></div>}
      {isMember
        ? <Composer members={channel.members.filter((n) => n !== me).map(dir.who)} onSend={send} />
        : <div className="caption muted" style={{ padding: 12, borderTop: "1px solid var(--line)" }}>你不是這個頻道的成員(組織管理員看得到全部頻道);要發訊息請在頻道設定把自己加進來。</div>}
      <Modal open={settings} onClose={() => setSettings(false)} title="頻道設定" width={560}>
        {settings && <ChannelForm channel={channel} dir={dir} me={me} onDone={() => setSettings(false)} />}
      </Modal>
    </>
  );
}

function MessageRow({ m, me, dir }: { m: ChannelMessage; me: string; dir: Dir }) {
  const w = dir.who(m.author);
  const mentionsMe = m.mentions?.includes(me);
  return (
    <div className="row gap8" style={{ alignItems: "flex-start" }}>
      <Avatar w={w} size={28} />
      <div className="col gap4 grow" style={{ minWidth: 0, padding: mentionsMe ? "4px 8px" : undefined, borderRadius: 6, background: mentionsMe ? "color-mix(in srgb, var(--orange) 10%, transparent)" : undefined }}>
        <div className="row gap6">
          <span style={{ fontWeight: 600 }}>{w.name}</span>
          {m.author.startsWith("agent:") && <Pill text="角色" color="purple" />}
          <span className="caption muted" title={m.at}>{relative(m.at)}</span>
          {m.mentions?.length > 0 && <span className="caption muted">· 提到 {m.mentions.map((n) => dir.who(n).name).join("、")}</span>}
        </div>
        <div style={{ userSelect: "text" }}><Markdown text={m.text} /></div>
      </div>
    </div>
  );
}

/** 輸入框:Enter 送出、Shift+Enter 換行;打 @ 時列出頻道成員,↑↓ 選、Enter/Tab 插入 */
function Composer({ members, onSend }: { members: Who[]; onSend: (text: string) => Promise<void> }) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [pick, setPick] = useState(0);
  const ta = useRef<HTMLTextAreaElement>(null);
  const [caret, setCaret] = useState(0);

  // 游標前面的 @片段(@ 前面要是開頭或非英數)
  const at = useMemo(() => {
    const before = text.slice(0, caret);
    const m = /(^|[^A-Za-z0-9])@([^\s@]*)$/.exec(before);
    return m ? { start: before.length - m[2].length - 1, q: m[2].toLowerCase() } : undefined;
  }, [text, caret]);
  const options = at ? members.filter((w) => w.name.toLowerCase().includes(at.q) || w.node.toLowerCase().includes(at.q)).slice(0, 8) : [];

  const insert = (w: Who) => {
    if (!at) return;
    const handle = w.node.startsWith("agent:") ? w.name : w.node.slice(5);
    const next = text.slice(0, at.start) + "@" + handle + " " + text.slice(caret);
    const pos = at.start + handle.length + 2;
    setText(next);
    setCaret(pos);
    requestAnimationFrame(() => { ta.current?.focus(); ta.current?.setSelectionRange(pos, pos); });
  };
  const submit = async () => {
    const t = text.trim();
    if (!t || busy) return;
    setBusy(true);
    try {
      await onSend(t);
      setText("");
      setError(undefined);
    } catch (e: any) {
      setError(String(e?.message ?? e));
    } finally {
      setBusy(false);
    }
  };
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.nativeEvent.isComposing) return; // 中文輸入法選字中
    if (options.length) {
      if (e.key === "ArrowDown") { e.preventDefault(); setPick((p) => (p + 1) % options.length); return; }
      if (e.key === "ArrowUp") { e.preventDefault(); setPick((p) => (p - 1 + options.length) % options.length); return; }
      if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) { e.preventDefault(); insert(options[Math.min(pick, options.length - 1)]); return; }
      if (e.key === "Escape") { setCaret(-1); return; }
    }
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); submit(); }
  };

  return (
    <div className="col gap4" style={{ padding: 12, borderTop: "1px solid var(--line)", position: "relative" }}>
      {options.length > 0 && (
        <div className="card col" style={{ position: "absolute", bottom: "100%", left: 12, minWidth: 240, padding: 4, gap: 0, zIndex: 5 }}>
          {options.map((w, i) => (
            <div key={w.node} className={"ops-list-row row gap6" + (i === Math.min(pick, options.length - 1) ? " on" : "")} style={{ borderBottom: 0, borderRadius: 4 }}
              onMouseDown={(e) => { e.preventDefault(); insert(w); }}>
              <Avatar w={w} size={18} /><span>{w.name}</span><span className="caption muted mono">{w.node}</span>
            </div>
          ))}
        </div>
      )}
      <div className="row gap8" style={{ alignItems: "flex-end" }}>
        <textarea ref={ta} className="input grow" rows={Math.min(8, Math.max(1, text.split("\n").length))} style={{ resize: "none", fontFamily: "inherit" }}
          placeholder="寫訊息…(Enter 送出、Shift+Enter 換行,@ 提到成員)" value={text}
          onChange={(e) => { setText(e.target.value); setCaret(e.target.selectionStart ?? e.target.value.length); setPick(0); }}
          onSelect={(e) => setCaret((e.target as HTMLTextAreaElement).selectionStart ?? 0)}
          onKeyDown={onKey} />
        <button className="btn primary" disabled={busy || !text.trim()} onClick={submit}><Icon name="paperplane" size={14} /></button>
      </div>
      {error && <ErrorText>{error}</ErrorText>}
    </div>
  );
}

/** 新增頻道或頻道設定(名稱、主題、成員加減) */
function ChannelForm({ channel, dir, me, onDone }: { channel?: Channel; dir: Dir; me: string; onDone: (id?: string) => void }) {
  const [name, setName] = useState(channel?.name ?? "");
  const [topic, setTopic] = useState(channel?.topic ?? "");
  const [members, setMembers] = useState<string[]>(channel?.members ?? [me]);
  const [extra, setExtra] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const known = new Set(dir.candidates.map((w) => w.node));
  const rows: Who[] = [...dir.candidates, ...members.filter((n) => !known.has(n)).map(dir.who)];
  const toggle = (n: string) => setMembers((l) => (l.includes(n) ? l.filter((x) => x !== n) : [...l, n]));

  const save = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const want = [...members, ...extra.split(/[,,\s]+/).map((x) => x.trim()).filter(Boolean)];
      if (!channel) {
        const c = await action<Channel>("channel_create", { name: name.trim(), topic: topic.trim(), members: want.filter((n) => n !== me) });
        onDone(c.id);
        return;
      }
      if (name.trim() !== channel.name || topic.trim() !== channel.topic) await action("channel_update", { id: channel.id, name: name.trim(), topic: topic.trim() });
      const add = want.filter((n) => !channel.members.includes(n));
      const remove = channel.members.filter((n) => !members.includes(n));
      if (add.length || remove.length) await action("channel_members", { id: channel.id, add, remove });
      onDone(channel.id);
    } catch (e: any) {
      setError(String(e?.message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="col gap8">
      <input className="input" placeholder="頻道名稱(最多 80 字)" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} autoFocus />
      <input className="input" placeholder="主題(選填)" value={topic} maxLength={500} onChange={(e) => setTopic(e.target.value)} />
      {channel?.issueId && <span className="caption muted">綁定 {channel.issueId}</span>}
      <div className="card-title">成員</div>
      <div className="col" style={{ maxHeight: 260, overflowY: "auto", gap: 2 }}>
        {rows.map((w) => (
          <label key={w.node} className="row gap6" style={{ padding: "3px 4px" }}>
            <input type="checkbox" checked={members.includes(w.node)} disabled={!channel && w.node === me} onChange={() => toggle(w.node)} />
            <Avatar w={w} size={18} />
            <span>{w.name}</span>
            <span className="caption muted mono">{w.node === me ? "(你)" : w.node}</span>
          </label>
        ))}
        {!rows.some((w) => w.node === me) && <span className="caption muted">你(建立者)會自動成為成員</span>}
      </div>
      <input className="input" placeholder="其他成員:email 或角色 id,用逗號分隔(選填)" value={extra} onChange={(e) => setExtra(e.target.value)} />
      {error && <ErrorText>{error}</ErrorText>}
      <div className="row gap8">
        <span className="caption muted">角色被 @ 時只會收到通知,不會排任務</span>
        <span className="grow" />
        <button className="btn" onClick={() => onDone()}>取消</button>
        <button className="btn primary" disabled={busy || !name.trim()} onClick={save}>{channel ? "儲存" : "建立"}</button>
      </div>
    </div>
  );
}
