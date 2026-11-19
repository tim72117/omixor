// 命令解析（純 JS，可在 Node 測試）
//   <角色> <動詞> <目標>，角色可用 id / 中文名 / 別名，多個角色用「、 , 和 與 跟 and &」分隔，
//   「全部 / all / 大家」代表所有角色；省略角色時用目前選取的角色。多條命令可用「；」或「;」分開。

const ALL_WORDS = ['全部', '全部人', '所有人', '大家', '每個人', '全員', 'all', 'everyone', 'everybody'];
const SEPARATORS = ['、', ',', '，', '和', '與', '跟', 'and', '&', '+'];
// 動詞（正規化後比對，最長者優先）
const VERBS = [
  ['return', ['回座位', '回到座位', '回自己座位', '回自己的座位', '回位子', '回到位子', '回座', '回位', '回去', '回來', '歸位', '坐下', '坐回去',
    'returntoseat', 'returntoseats', 'return', 'goback', 'back', 'gohome', 'sitdown', 'sit']],
  ['find', ['去找', '找到', '找', '去見', '見', '跟上', '跟著', '走向', 'find', 'meet', 'visit', 'follow']],
  ['goto', ['走到', '走去', '前往', '去到', '移動到', '移到', '過去', '到', '去', 'goto', 'walkto', 'moveto', 'go', 'walk', 'move', 'head']],
  ['stop', ['停下來', '停下', '停止', '停', '別動', 'stop', 'halt', 'wait']],
];
const SEAT_SUFFIX = ['的座位', '座位', '的位子', '位子', '的位置', 'seat', '的椅子', 'chair'];
const ZONE_SUFFIX = ['的工作區', '工作區', '的桌子', '桌子', '辦公桌', 'desk', 'workspace', 'workstation', 'zone'];
const TARGET_TRIM_HEAD = ['to', 'the', '到'];
const TARGET_TRIM_TAIL = ['那裡', '那邊', '那兒', '旁邊', '身邊', '去', '一下', '吧', '!', '！', '。', '.'];

export function normalize(s) {
  return String(s ?? '')
    .normalize('NFKC')
    .toLowerCase()
    .replace(/['’]s\b/g, '')
    .replace(/[\s\-_'"’「」『』（）()【】]/g, '');
}

export function createParser({ agents, places }) {
  const agentAlias = new Map(); // 正規化別名 → id
  for (const a of agents) {
    for (const al of [a.id, a.name, ...(a.aliases || [])]) agentAlias.set(normalize(al), a.id);
  }
  const placeAlias = new Map();
  for (const p of places) {
    for (const al of [p.id, p.name, ...(p.aliases || [])]) {
      const k = normalize(al);
      if (!placeAlias.has(k)) placeAlias.set(k, p.id);
    }
  }
  const agentKeys = [...agentAlias.keys()].sort((a, b) => b.length - a.length);
  const allKeys = ALL_WORDS.map(normalize).sort((a, b) => b.length - a.length);
  const sepKeys = SEPARATORS.map(normalize).sort((a, b) => b.length - a.length);
  const verbList = [];
  for (const [type, words] of VERBS) for (const w of words) verbList.push([normalize(w), type]);
  verbList.sort((a, b) => b[0].length - a[0].length);
  const placeKeys = [...placeAlias.keys()].sort((a, b) => b.length - a.length);
  const agentName = id => agents.find(a => a.id === id)?.name ?? id;
  const placeName = id => places.find(p => p.id === id)?.name ?? id;

  function matchAgentAt(s, pos) {
    for (const k of allKeys) if (k && s.startsWith(k, pos)) return { all: true, len: k.length };
    for (const k of agentKeys) if (k && s.startsWith(k, pos)) return { id: agentAlias.get(k), len: k.length };
    return null;
  }

  function resolveAgent(t) {
    const k = normalize(t);
    if (agentAlias.has(k)) return agentAlias.get(k);
    return null;
  }

  /** 解析目標：回傳 {kind:'place', id} | {kind:'agent', id} | null */
  function resolveTarget(raw) {
    let t = normalize(raw);
    for (let changed = true; changed;) {
      changed = false;
      for (const h of TARGET_TRIM_HEAD) if (t.startsWith(h) && t.length > h.length && !placeAlias.has(t)) {
        const rest = t.slice(h.length);
        if (placeAlias.has(rest) || resolveAgent(rest) || rest.length >= 2) { t = rest; changed = true; }
      }
      for (const tl of TARGET_TRIM_TAIL) if (t.endsWith(tl) && t.length > tl.length) { t = t.slice(0, -tl.length); changed = true; }
    }
    if (!t) return null;
    if (placeAlias.has(t)) return { kind: 'place', id: placeAlias.get(t) };
    // <角色>(的)座位 / 工作區
    for (const [suffixes, prefix] of [[SEAT_SUFFIX, 'seat_'], [ZONE_SUFFIX, 'zone_']]) {
      for (const sfx of suffixes.map(normalize).sort((a, b) => b.length - a.length)) {
        if (t.endsWith(sfx) && t.length > sfx.length) {
          let who = t.slice(0, -sfx.length);
          if (who.endsWith('的')) who = who.slice(0, -1);
          const id = resolveAgent(who);
          if (id) return { kind: 'place', id: prefix + id };
        }
      }
    }
    const aid = resolveAgent(t);
    if (aid) return { kind: 'agent', id: aid };
    // 模糊：別名包含關係（至少 2 個字）
    if (t.length >= 2) {
      for (const k of placeKeys) if (k.length >= 2 && (k.includes(t) || t.includes(k))) return { kind: 'place', id: placeAlias.get(k) };
    }
    return null;
  }

  function parseOne(text, ctx) {
    const s = normalize(text);
    if (!s) return { ok: false, error: '請輸入命令，例如「工程師 去 會議室」' };
    // 1) 主詞
    let pos = 0;
    const subj = [];
    let all = false;
    for (;;) {
      const m = matchAgentAt(s, pos);
      if (!m) break;
      // 避免把動詞的一部分吃成角色別名（例如 "go" 不在別名裡，這裡只是保險）
      if (m.all) all = true; else if (!subj.includes(m.id)) subj.push(m.id);
      pos += m.len;
      let sep = false;
      for (const k of sepKeys) if (s.startsWith(k, pos) && matchAgentAt(s, pos + k.length)) { pos += k.length; sep = true; break; }
      if (!sep) break;
    }
    if (all) subj.splice(0, subj.length, ...agents.map(a => a.id));
    // 2) 動詞
    let verb = null;
    for (const [w, type] of verbList) if (s.startsWith(w, pos)) { verb = type; pos += w.length; break; }
    const rest = s.slice(pos);
    if (!verb) {
      // 「工程師 會議室」：省略動詞視為 goto；「會議室」：用選取的角色
      if (subj.length && rest) verb = 'goto';
      else if (!subj.length && ctx.selected && resolveTarget(rest)) verb = 'goto';
      else if (subj.length && !rest) return { ok: false, error: `「${text}」缺少動作。用法：${agentName(subj[0])} 去 會議室／${agentName(subj[0])} 找 CEO／${agentName(subj[0])} 回座位` };
      else return { ok: false, error: `看不懂「${text}」：找不到角色或動作。角色可用：${agents.map(a => a.name).join('、')}` };
    }
    if (!subj.length) {
      if (ctx.selected) subj.push(ctx.selected);
      else return { ok: false, error: `「${text}」沒有指定角色，請先點選角色或在命令開頭寫角色名稱` };
    }
    if (verb === 'return' || verb === 'stop') {
      if (verb === 'return' && rest) {
        // 「回 會議室」之類：當成 goto
        const tg = resolveTarget(rest);
        if (tg) return { ok: true, action: { type: tg.kind === 'agent' ? 'find' : 'goto', agents: subj, target: tg } };
      }
      return { ok: true, action: { type: verb, agents: subj } };
    }
    if (!rest) return { ok: false, error: `「${text}」缺少目的地` };
    const tg = resolveTarget(rest);
    if (!tg) {
      return { ok: false, error: `找不到地點或角色「${rest}」。可用地點：${places.filter(p => p.kind !== 'seat' && p.kind !== 'workspace').map(p => p.name).join('、')}；或「<角色>的座位」「<角色>的工作區」` };
    }
    if (verb === 'find') {
      if (tg.kind !== 'agent') return { ok: true, action: { type: 'goto', agents: subj, target: tg } };
    }
    const type = tg.kind === 'agent' ? 'find' : 'goto';
    if (type === 'find') {
      const others = subj.filter(id => id !== tg.id);
      if (!others.length) return { ok: false, error: `${agentName(tg.id)} 不能去找自己` };
      return { ok: true, action: { type, agents: others, target: tg } };
    }
    return { ok: true, action: { type, agents: subj, target: tg } };
  }

  function parse(text, ctx = {}) {
    const parts = String(text ?? '').split(/[;；\n]+/).map(t => t.trim()).filter(Boolean);
    if (!parts.length) return { ok: false, actions: [], errors: ['請輸入命令'] };
    const actions = [], errors = [];
    for (const p of parts) {
      const r = parseOne(p, ctx);
      if (r.ok) actions.push(r.action); else errors.push(r.error);
    }
    return { ok: errors.length === 0, actions, errors };
  }

  function describe(action) {
    const who = action.agents.length === agents.length ? '全部' : action.agents.map(agentName).join('、');
    if (action.type === 'return') return `${who} 回座位`;
    if (action.type === 'stop') return `${who} 停下`;
    if (action.type === 'find') return `${who} 去找 ${agentName(action.target.id)}`;
    return `${who} 去 ${placeName(action.target.id)}`;
  }

  return { parse, resolveTarget, resolveAgent, describe };
}
