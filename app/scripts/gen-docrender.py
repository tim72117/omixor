#!/usr/bin/env python3
# 把 web/spec/docrender.js(文件櫃的通用轉換器)複製成 Swift 字串:app/Sources/Omixor/Cabinet/DocRenderJS.swift
import pathlib
root = pathlib.Path(__file__).resolve().parents[2]
js = (root / 'web/spec/docrender.js').read_text()
assert '"""##' not in js
(root / 'app/Sources/Omixor/Cabinet/DocRenderJS.swift').write_text(
    '// 自動產生,不要手改:內容和 web/spec/docrender.js 一模一樣(DocTemplateTests 會比對)。\n'
    '// 改轉換器時改 web/spec/docrender.js,再跑 app/scripts/gen-docrender.py。\n'
    'enum DocRenderJS {\n    static let source = ##"""\n' + js + '"""##\n}\n')
