// 設定 › 整合:依類別列出外部服務,可連線/中斷連線;內建 MCP 伺服器狀態(Swift 的 SettingsIntegrationsPane)
import { useState } from "react";
import { action, useRun, useView } from "../../api";
import { Spinner, copyText } from "../../components/bits";
import { Icon } from "../../components/Icon";
import { arr, type Integration } from "../../components/models";
import { EmptyState, ErrorText, Modal } from "../../components/ui";
import { Form, Row, Section } from "./form";

type Mcp = { status: { state: string; error?: string; requests: number; lastClient?: string; lastTool?: string; lastRequestAt?: string }; url: string };

export function SettingsIntegrations() {
  const [raw, err] = useView<Integration[]>("integrations");
  const [query, setQuery] = useState("");
  const [onlyConnected, setOnlyConnected] = useState(false);
  const [connecting, setConnecting] = useState<Set<string>>(new Set());
  const [pending, setPending] = useState<Integration>();
  const { error, run } = useRun();
  const all = arr<Integration>(raw);
  const q = query.trim().toLowerCase();
  const filtered = all.filter((i) => (!onlyConnected || i.connected) && (!q || [i.name, i.category, i.detail].some((s) => s.toLowerCase().includes(q))));
  const cats = [...new Set(filtered.map((i) => i.category))].sort();
  /** 模擬 OAuth/驗證往返後完成連線 */
  const connect = (i: Integration) => {
    setConnecting((s) => new Set([...s, i.id]));
    window.setTimeout(() => {
      run(() => action("integration_set", { id: i.id, connected: true })).finally(() => setConnecting((s) => { const n = new Set(s); n.delete(i.id); return n; }));
    }, 900);
  };
  return (
    <Form>
      <Section>
        <Row>
          <Icon name="puzzlepiece.extension.fill" size={22} color="var(--accent)" />
          <div className="col grow">
            <span style={{ fontWeight: 600 }}>已連線 {all.filter((i) => i.connected).length} / {all.length} 個整合</span>
            <span className="caption muted">omixor 透過這些整合讀取原始碼、CI/CD、雲端資源與監控資料,並送出通知。</span>
          </div>
        </Row>
        <Row>
          <input className="input grow" placeholder="搜尋整合" value={query} onChange={(e) => setQuery(e.target.value)} />
          <label className="row gap4"><input type="checkbox" checked={onlyConnected} onChange={(e) => setOnlyConnected(e.target.checked)} />只顯示已連線</label>
        </Row>
      </Section>
      <McpSection />
      <ErrorText>{error ?? err}</ErrorText>
      {raw === undefined && !err ? <div className="muted">載入中…</div> : all.length === 0 ? (
        <Section><EmptyState title="尚無可用的整合" icon="puzzlepiece.extension" message="整合清單載入後會顯示在這裡。" /></Section>
      ) : filtered.length === 0 ? (
        <Section><Row><span className="muted">找不到符合「{query}」的整合</span></Row></Section>
      ) : cats.map((c) => (
        <Section key={c} header={c}>
          {filtered.filter((i) => i.category === c).map((i) => (
            <Row key={i.id}>
              <span style={{ width: 30, height: 30, borderRadius: 7, display: "inline-flex", alignItems: "center", justifyContent: "center", flex: "none",
                background: `color-mix(in srgb, ${i.connected ? "var(--accent)" : "var(--fg2)"} 12%, transparent)` }}>
                <Icon name={i.icon} size={15} color={i.connected ? "var(--accent)" : "var(--fg2)"} />
              </span>
              <div className="col grow"><span style={{ fontWeight: 500 }}>{i.name}</span><span className="caption muted">{i.detail}</span></div>
              {connecting.has(i.id) ? <><Spinner size={12} /><span className="caption muted">連線中…</span></>
                : i.connected ? (
                  <>
                    <span className="row gap4 caption" style={{ color: "var(--green)" }}><Icon name="checkmark.circle.fill" size={12} />已連線</span>
                    <button className="btn small" onClick={() => setPending(i)}>中斷連線</button>
                  </>
                ) : <button className="btn small primary" onClick={() => connect(i)}>連線</button>}
            </Row>
          ))}
        </Section>
      ))}
      <Modal open={!!pending} onClose={() => setPending(undefined)} width={420} title={`中斷與 ${pending?.name ?? ""} 的連線?`}>
        <span className="muted">omixor 將停止從 {pending?.name} 讀取資料,相關的自動化與通知也會暫停。</span>
        <div className="row gap8" style={{ justifyContent: "flex-end" }}>
          <button className="btn" onClick={() => setPending(undefined)}>取消</button>
          <button className="btn red" onClick={() => { const p = pending!; setPending(undefined); run(() => action("integration_set", { id: p.id, connected: false })); }}>中斷連線</button>
        </div>
      </Modal>
    </Form>
  );
}

function McpSection() {
  const [m] = useView<Mcp>("mcp");
  const cmd = `claude mcp add --transport http ops ${m?.url ?? ""}`;
  const st = m?.status;
  const [text, c] = !st ? ["…", "var(--gray)"]
    : st.state === "listening" ? ["執行中", "var(--green)"]
    : st.state === "starting" ? ["啟動中", "var(--orange)"]
    : st.state === "failed" ? [`無法啟動:${st.error ?? ""}`, "var(--red)"] : ["未啟動", "var(--gray)"];
  return (
    <Section header="MCP 伺服器">
      <Row>
        <Icon name="point.3.connected.trianglepath.dotted" size={22} color="var(--accent)" />
        <div className="col grow">
          <span className="row gap6"><span className="dot" style={{ width: 8, height: 8, background: c }} /><span style={{ fontWeight: 600 }}>{text}</span></span>
          <span className="caption muted">MCP 用戶端可以讀取這裡的資料,並處理告警、事件、工作項目與送出待核准請求。只接受本機連線。</span>
        </div>
      </Row>
      <Row label="位址"><span className="mono" style={{ userSelect: "text" }}>{m?.url}</span></Row>
      <Row label="加到 Claude Code">
        <span className="mono caption ellipsis" style={{ maxWidth: 380, userSelect: "text" }}>{cmd}</span>
        <button className="btn small" onClick={() => copyText(cmd)}>複製</button>
      </Row>
      {st && st.requests > 0 && (
        <Row label="最近使用">
          <span className="muted">{[st.lastClient, st.lastTool, st.lastRequestAt && new Date(st.lastRequestAt).toLocaleTimeString("zh-TW", { hour12: false })].filter(Boolean).join(" · ")} · 共 {st.requests} 次請求</span>
        </Row>
      )}
    </Section>
  );
}
