// 離線檢查 index.html importmap 的 SRI：node test/sri_test.mjs
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const html = readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', 'index.html'), 'utf8');
let pass = 0, fail = 0;
const ok = (c, m) => { if (c) pass++; else { fail++; console.log('  FAIL', m); } };

const m = html.match(/<script type="importmap">([\s\S]*?)<\/script>/);
ok(!!m, 'importmap 存在');
const map = JSON.parse(m[1]);
const imports = map.imports || {}, integrity = map.integrity || {};

const versions = new Set();
for (const [k, u] of Object.entries(imports)) {
  const v = u.match(/^https:\/\/cdn\.jsdelivr\.net\/npm\/three@(\d+\.\d+\.\d+)\//);
  ok(!!v, `import ${k} 必須是 jsdelivr three@x.y.z 固定版本：${u}`);
  if (v) versions.add(v[1]);
}
for (const u of Object.keys(integrity)) {
  const v = u.match(/three@([^/]+)\//);
  ok(!!v && /^\d+\.\d+\.\d+$/.test(v[1]), `integrity URL 版本固定：${u}`);
  if (v) versions.add(v[1]);
  ok(/^sha384-[A-Za-z0-9+/]{64}$/.test(integrity[u]), `integrity 格式（sha384）：${u}`);
}
ok(versions.size === 1, `版本一致：${[...versions]}`);
ok(!/latest|@\d+(\.\d+)?\//.test(JSON.stringify(map)), '不得用 latest 或不完整版本');
ok(Object.keys(integrity).length >= 6, `integrity 項目數 ${Object.keys(integrity).length}`);

// 每個 import 到的模組檔都要有 integrity；bare specifier 對應的檔案必須列入
const importedFiles = [
  imports['three'],
  ...['controls/OrbitControls.js', 'loaders/GLTFLoader.js', 'renderers/CSS2DRenderer.js', 'environments/RoomEnvironment.js', 'utils/BufferGeometryUtils.js']
    .map((p) => imports['three/addons/'] + p),
];
for (const u of importedFiles) ok(!!integrity[u], `缺 integrity：${u}`);

// 全頁面不得有未列入 integrity 的 jsdelivr URL（也不得有其他外部 script/link）
for (const u of html.match(/https?:\/\/[^\s"'<>)]+/g) || []) {
  if (/jsdelivr\.net/.test(u)) ok(!!integrity[u] || u === imports['three'] && !!integrity[u] || u === imports['three/addons/'], `jsdelivr URL 未列入 integrity：${u}`);
  else ok(false, `非預期的外部網址：${u}`);
}
ok(!/<script[^>]+src=["']https?:/i.test(html) && !/<link[^>]+href=["']https?:/i.test(html), '沒有外部 script/link');

console.log(`sri_test: ${pass} pass, ${fail} fail`);
process.exit(fail ? 1 : 0);
