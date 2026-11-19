// 重算 index.html importmap 的 integrity 區塊（升級 three.js 版本時用）。
//   node tools/gen-sri.mjs            只印出結果
//   node tools/gen-sri.mjs --write    直接寫回 index.html
// 做法：讀 index.html 的 importmap imports，掃 js/*.js 實際 import 的 three 模組，
// 遞迴跟進這些模組的相依，逐一下載到記憶體只算 sha384（不執行、不存檔）。
import { readFileSync, writeFileSync, readdirSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const htmlPath = join(root, 'index.html');
const html = readFileSync(htmlPath, 'utf8');
const mapRe = /(<script type="importmap">)([\s\S]*?)(<\/script>)/;
const m = html.match(mapRe);
if (!m) throw new Error('index.html 找不到 importmap');
const map = JSON.parse(m[2]);
const imports = map.imports;

function resolveSpec(spec, base) {
  if (/^\.{0,2}\//.test(spec)) return new URL(spec, base).href;
  if (imports[spec]) return imports[spec];
  const prefix = Object.keys(imports).filter((k) => k.endsWith('/') && spec.startsWith(k)).sort((a, b) => b.length - a.length)[0];
  return prefix ? imports[prefix] + spec.slice(prefix.length) : null;
}
const specRe = /(?:\bimport\s*(?:[\w*{}\s,$]+?\s*from\s*)?|\bexport\s*(?:[\w*{}\s,$]+?)\s*from\s*|\bimport\s*\(\s*)(['"])([^'"]+)\1/g;
function specsOf(src) {
  const out = [];
  for (const x of src.matchAll(specRe)) out.push(x[2]);
  return out;
}

// 起點：viewer/js 底下所有 import 到 importmap 項目的 bare specifier
const entries = new Set();
const jsDir = join(root, 'js');
for (const f of readdirSync(jsDir).filter((f) => f.endsWith('.js'))) {
  for (const s of specsOf(readFileSync(join(jsDir, f), 'utf8'))) {
    if (!/^\.{0,2}\//.test(s)) {
      const u = resolveSpec(s, 'http://x/');
      if (!u) throw new Error(`${f}: 無法解析 ${s}`);
      entries.add(u);
    }
  }
}

const hashes = {};
const queue = [...entries];
while (queue.length) {
  const url = queue.shift();
  if (hashes[url]) continue;
  const res = await fetch(url);
  if (!res.ok) throw new Error(`${url} -> ${res.status}`);
  const buf = Buffer.from(await res.arrayBuffer());
  hashes[url] = 'sha384-' + createHash('sha384').update(buf).digest('base64');
  for (const s of specsOf(buf.toString('utf8'))) {
    const u = resolveSpec(s, url);
    if (!u) throw new Error(`${url}: 無法解析 ${s}`);
    queue.push(u);
  }
}
const integrity = Object.fromEntries(Object.entries(hashes).sort(([a], [b]) => a.localeCompare(b)));
console.error(`涵蓋 ${Object.keys(integrity).length} 個模組`);
const newMap = { imports, integrity };
const text = JSON.stringify(newMap, null, 2);
if (process.argv.includes('--write')) {
  writeFileSync(htmlPath, html.replace(mapRe, (_, a, __, c) => `${a}\n${text}\n${c}`));
  console.error('已寫入 index.html');
} else console.log(text);
