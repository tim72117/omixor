#!/usr/bin/env python3
# 把 ops-web/spec/docrender.js(文件櫃的通用轉換器)複製成 Swift 字串:ops/Sources/Ops/Cabinet/DocRenderJS.swift
import pathlib
root = pathlib.Path(__file__).resolve().parents[2]
js = (root / 'ops-web/spec/docrender.js').read_text()
assert '"""##' not in js
(root / 'ops/Sources/Ops/Cabinet/DocRenderJS.swift').write_text(
    '// 自動產生,不要手改:內容和 ops-web/spec/docrender.js 一模一樣(DocTemplateTests 會比對)。\n'
    '// 改轉換器時改 ops-web/spec/docrender.js,再跑 ops/scripts/gen-docrender.py。\n'
    'enum DocRenderJS {\n    static let source = ##"""\n' + js + '"""##\n}\n')
