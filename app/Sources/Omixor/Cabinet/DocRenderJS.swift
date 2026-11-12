// 自動產生,不要手改:內容和 web/spec/docrender.js 一模一樣(DocTemplateTests 會比對)。
// 改轉換器時改 web/spec/docrender.js,再跑 app/scripts/gen-docrender.py。
enum DocRenderJS {
    static let source = ##"""
// 文件櫃的通用轉換器(Swift 版與 Go 版共用這一份,Swift 版的 DocRenderJS.swift 是它的副本):
// Markdown(開頭欄位 + 固定小標)→ 資料 → 套樣板產生 HTML。不認得任何課程欄位,所以新類型不用改這裡。
(function (global) {
  const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]))

  // ---- 1. Markdown → 資料 ----
  function parseDoc(md) {
    const doc = { meta: {}, title: '', s: {} }
    let lines = md.replace(/\r/g, '').split('\n')
    if (lines[0] === '---') {
      const end = lines.indexOf('---', 1)
      for (const l of lines.slice(1, end)) { const i = l.indexOf(':'); if (i > 0) doc.meta[l.slice(0, i).trim()] = l.slice(i + 1).trim() }
      lines = lines.slice(end + 1)
    }
    const block = () => ({ text: '', items: {}, list: [], pairs: [], subs: [], code: '' })
    let sec = null, cur = null, inCode = false, code = []
    for (const l of lines) {
      if (inCode) { if (l.startsWith('```')) { cur.code = code.join('\n'); inCode = false } else code.push(l); continue }
      if (l.startsWith('```')) { if (!cur) continue; inCode = true; code = []; continue }
      let m
      if ((m = l.match(/^# (.+)/))) { doc.title = m[1].trim(); continue }
      if ((m = l.match(/^## (.+)/))) { sec = cur = block(); doc.s[m[1].trim()] = sec; continue }
      if ((m = l.match(/^### (.+)/)) && sec) { cur = { ...block(), title: m[1].trim(), n: sec.subs.length + 1 }; sec.subs.push(cur); continue }
      if (!cur || !l.trim() || /^[^\s].{0,12}：$/.test(l.trim())) continue
      if ((m = l.match(/^[-*] (.+)/))) {
        const item = m[1].trim(), kv = item.match(/^([^：:]{1,12})[：:]\s*(.+)$/)
        cur.list.push(item)
        if (kv) { cur.items[kv[1]] = kv[2]; cur.pairs.push({ k: kv[1], v: kv[2] }) }
        continue
      }
      cur.text += (cur.text ? '\n' : '') + l.trim()
    }
    return doc
  }

  // ---- 2. 樣板(Mustache 的小子集):{{a.b}}、{{#a}}…{{/a}}(陣列重複、有值才顯示)、{{^a}}…{{/a}}(沒值才顯示)、{{.}} ----
  function compile(tpl) {
    const root = [], stack = [root], re = /{{\s*([#^/]?)\s*([^}]+?)\s*}}/g
    let last = 0, m
    while ((m = re.exec(tpl))) {
      stack.at(-1).push(tpl.slice(last, m.index)); last = re.lastIndex
      if (m[1] === '#' || m[1] === '^') { const node = { kind: m[1], name: m[2], body: [] }; stack.at(-1).push(node); stack.push(node.body) }
      else if (m[1] === '/') stack.pop()
      else stack.at(-1).push({ kind: 'v', name: m[2] })
    }
    stack.at(-1).push(tpl.slice(last))
    return root
  }
  function lookup(name, ctx) {
    if (name === '.') return ctx[ctx.length - 1]
    const parts = name.split('.')
    for (let i = ctx.length - 1; i >= 0; i--) {
      const c = ctx[i]
      if (c && typeof c === 'object' && parts[0] in c) return parts.reduce((o, p) => (o == null ? undefined : o[p]), c)
    }
  }
  const truthy = v => Array.isArray(v) ? v.length > 0 : !!v && !(typeof v === 'object' && Object.keys(v).length === 0)
  function run(nodes, ctx) {
    let out = ''
    for (const n of nodes) {
      if (typeof n === 'string') out += n
      else if (n.kind === 'v') out += esc(lookup(n.name, ctx))
      else {
        const v = lookup(n.name, ctx)
        if (n.kind === '^') { if (!truthy(v)) out += run(n.body, ctx) }
        else if (Array.isArray(v)) v.forEach(x => (out += run(n.body, [...ctx, x])))
        else if (truthy(v)) out += run(n.body, typeof v === 'object' ? [...ctx, v] : ctx)
      }
    }
    return out
  }
  // 樣板只能有 HTML 和 CSS:拿掉 script、事件屬性與外部網址
  const sanitize = t => t.replace(/<script[\s\S]*?<\/script>/gi, '').replace(/\son\w+="[^"]*"/gi, '').replace(/(src|href)="(?!#)[^"]*"/gi, '')
  const render = (tpl, data) => run(compile(sanitize(tpl)), [data])

  // ---- 3. 沒有樣板時:通用 Markdown 顯示 ----
  function plain(md) {
    const doc = md.replace(/^---[\s\S]*?\n---\n/, '')
    const inline = s => esc(s).replace(/\*\*(.+?)\*\*/g, '<b>$1</b>').replace(/`(.+?)`/g, '<code>$1</code>')
    let html = '', list = false, code = null
    for (const l of doc.split('\n')) {
      if (code !== null) { if (l.startsWith('```')) { html += `<pre>${esc(code.join('\n'))}</pre>`; code = null } else code.push(l); continue }
      if (l.startsWith('```')) { code = []; continue }
      const li = l.match(/^[-*] (.+)/)
      if (!li && list) { html += '</ul>'; list = false }
      let m
      if ((m = l.match(/^(#{1,3}) (.+)/))) html += `<h${m[1].length + 1}>${inline(m[2])}</h${m[1].length + 1}>`
      else if (li) { if (!list) { html += '<ul class="plain-list">'; list = true } html += `<li>${inline(li[1])}</li>` }
      else if (l.trim()) html += `<p class="lp-text">${inline(l)}</p>`
    }
    return html + (list ? '</ul>' : '')
  }

  // 文件櫃的單一文件頁:套範本後放進 #root;高度回報給外層(Go 版網頁的 iframe 用)
  function mount(data) {
    const root = document.getElementById('root')
    root.innerHTML = render(data.template, { ...parseDoc(data.md), first: true })
    root.querySelectorAll('details').forEach(d => (d.open = true))
    root.addEventListener('click', e => {
      const b = e.target.closest('button.copy'); if (!b) return
      const pre = b.parentElement.querySelector('pre'); if (!pre) return
      const done = () => { b.textContent = '已複製'; setTimeout(() => (b.textContent = '複製'), 1200) }
      try { navigator.clipboard.writeText(pre.textContent).then(done, () => {}) } catch (_) {}
    })
    // 在 iframe 裡時高度跟著內容(min-height: 100vh 會和外層互相撐大)
    const framed = window.parent !== window
    if (framed) document.body.style.minHeight = '0'
    const report = () => { if (framed) window.parent.postMessage({ docHeight: document.documentElement.scrollHeight }, '*') }
    report()
    if (window.ResizeObserver) new ResizeObserver(report).observe(document.body)
  }

  global.MdRender = { parseDoc, render, plain, mount }
})(typeof window !== 'undefined' ? window : globalThis)
"""##
}
