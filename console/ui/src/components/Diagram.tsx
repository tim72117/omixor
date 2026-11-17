// 文件裡的圖表程式碼區塊:```wireframe(線框圖 JSON)、```flowchart(流程圖 JSON 或 Mermaid)、
// 以及 flowchart / graph 語法的 ```mermaid(其他 Mermaid 圖型照原本顯示程式碼)。
// 渲染在後端(POST /api/action/diagram_svg {kind, spec} → omixor-core/tools/wireframe、flowchart;本機與遠端模式都在本機算),
// 前端只用 <img src="data:image/svg+xml,…"> 顯示:img 裡的 SVG 不會執行腳本、不會載入外部資源,
// 所以不用 dangerouslySetInnerHTML。點圖切換「版面寬度 / 原始大小」,圖下方可以展開原始描述;編輯文件時照舊改原文。
import { useEffect, useState } from "react";
import { action } from "../api";

export type DiagramKind = "wireframe" | "flowchart";

type Rendered = { svg?: string; width?: number; height?: number; warnings?: string[]; error?: string };

const NAMES: Record<DiagramKind, { noun: string; source: string }> = {
  wireframe: { noun: "線框圖", source: "線框圖描述(JSON)" },
  flowchart: { noun: "流程圖", source: "流程圖描述" },
};

// Mermaid 區塊是不是 flowchart / graph 圖(跳過空行、%% 註解與 --- 前置區塊;和後端 flowchart.IsMermaidFlowchart 一致)
export function isMermaidFlowchart(src: string): boolean {
  let front = false;
  for (const raw of src.split("\n")) {
    const t = raw.trim();
    if (!t || t.startsWith("%%")) continue;
    if (t === "---") { front = !front; continue; }
    if (front) continue;
    return /^(flowchart|graph|flowchart-elk)(\s|;|$)/i.test(t);
  }
  return false;
}

// 程式碼區塊的語言 → 要畫成哪種圖(不是圖就回 null)
export function diagramKindOf(lang: string, code: string): DiagramKind | null {
  if (lang === "wireframe") return "wireframe";
  if (lang === "flowchart") return "flowchart";
  if (lang === "mermaid" && isMermaidFlowchart(code)) return "flowchart";
  return null;
}

// 同一段描述不重算(文件重新整理、切換頁面時);最多留 40 份
const cache = new Map<string, Rendered>();
function remember(key: string, r: Rendered) {
  if (cache.size >= 40) cache.delete(cache.keys().next().value as string);
  cache.set(key, r);
}

export function DiagramBlock({ kind, source }: { kind: DiagramKind; source: string }) {
  const key = `${kind}\n${source}`;
  const [r, setR] = useState<Rendered | null>(() => cache.get(key) ?? null);
  const [zoom, setZoom] = useState(false);
  useEffect(() => {
    const hit = cache.get(key);
    if (hit) { setR(hit); return; }
    setR(null);
    let live = true;
    action<Rendered>("diagram_svg", { kind, spec: source }).then(
      (x) => { const v = x ?? { error: "沒有回應" }; if (!v.error) remember(key, v); if (live) setR(v); },
      (e) => live && setR({ error: String(e?.message ?? e) }),
    );
    return () => { live = false; };
  }, [key, kind, source]);
  const name = NAMES[kind];
  const src = r?.svg ? `data:image/svg+xml;charset=utf-8,${encodeURIComponent(r.svg)}` : null;
  return (
    <div className={`diagram diagram-${kind} col gap8`} style={{ margin: "8px 0" }}>
      {r?.error ? <span className="caption" style={{ color: "var(--red)" }}>{name.noun}畫不出來:{r.error}</span>
        : src ? (
          <div style={{ overflowX: zoom ? "auto" : "hidden", border: "1px solid var(--line)", borderRadius: 8, background: "#fff" }}>
            <img src={src} alt={name.noun} title={zoom ? "點一下縮成版面寬度" : "點一下看原始大小"} onClick={() => setZoom(!zoom)}
              style={{ display: "block", width: zoom ? r?.width : "100%", maxWidth: zoom ? "none" : r?.width, height: "auto", cursor: zoom ? "zoom-out" : "zoom-in" }} />
          </div>
        ) : <span className="caption muted">畫{name.noun}中…</span>}
      {r?.warnings && r.warnings.length > 0 && <span className="caption muted">{r.warnings.join(";")}</span>}
      <details>
        <summary className="caption muted" style={{ cursor: "pointer" }}>{name.source}</summary>
        <pre><code>{source}</code></pre>
      </details>
    </div>
  );
}
