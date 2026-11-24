import SwiftUI

/// AI 自動化 › 執行日誌(對應 web 的 AutoLog.tsx、RunLog.tsx):這台 Mac 上「自動執行」跑過的任務(最近 RunLogLimits.keep 筆),
/// 可依角色、結果篩選;每筆可展開成工具呼叫的時間線。
struct AutoLogView: View {
    @ObservedObject var runner: AgentRunner
    /// 點任務 id:到那個任務的派發細節
    let openTask: (String) -> Void
    @State private var agentId = ""
    @State private var outcome = RunOutcomeFilter.all
    /// 展開的那一筆(一次一筆)
    @State private var openRun: String?

    var body: some View {
        let rows = runner.history.filtered(agent: agentId, outcome: outcome)
        ScrollView {
            Card("執行日誌", subtitle: runner.history.isEmpty ? nil : "\(rows.count) / \(runner.history.count) 筆") {
                if runner.history.isEmpty {
                    EmptyStateView(title: "還沒有執行紀錄", icon: "clock", message: "角色自動執行過的任務會出現在這裡。")
                } else {
                    filters
                    if rows.isEmpty { Text("沒有符合的紀錄").foregroundStyle(.secondary) }
                    ForEach(rows) { r in
                        RunRecordRow(log: r, open: openRun == r.runId, openTask: openTask) { openRun = openRun == r.runId ? nil : r.runId }
                        Divider()
                    }
                }
            }
            .padding(20)
            .frame(maxWidth: 1000, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task { await runner.loadHistory() }   // 讀日誌檔在背景,不擋主執行緒
    }

    private var filters: some View {
        HStack {
            Picker("角色", selection: $agentId) {
                Text("全部角色").tag("")
                ForEach(runner.history.roles, id: \.id) { Text($0.name).tag($0.id) }
            }
            .labelsHidden().fixedSize()
            Picker("結果", selection: $outcome) { ForEach(RunOutcomeFilter.allCases) { Text($0.label).tag($0) } }
                .labelsHidden().fixedSize()
            Spacer()
        }
    }
}

/// 結果的呈現:顯示文字、顏色、圖示(沒列到的結果值照原字串顯示)
struct RunOutcomeStyle: Equatable {
    var label: String
    var color: Color
    var icon: String

    static func of(_ outcome: String) -> RunOutcomeStyle {
        switch outcome {
        case RunOutcome.done: return RunOutcomeStyle(label: "完成", color: .green, icon: "checkmark.circle")
        case RunOutcome.blocked: return RunOutcomeStyle(label: "需要協助", color: .orange, icon: "hand.raised")
        case RunOutcome.error:
            return RunOutcomeStyle(label: "錯誤", color: .red, icon: "exclamationmark.triangle")
        default: return RunOutcomeStyle(label: outcome, color: .gray, icon: "questionmark.circle")
        }
    }
}

extension RunOutcomeFilter {
    var label: String { self == .all ? "全部結果" : RunOutcomeStyle.of(rawValue).label }
}

/// 一筆執行:摘要 + 警示標籤 + 可展開的時間線
private struct RunRecordRow: View {
    let log: RunLog
    let open: Bool
    let openTask: (String) -> Void
    let toggle: () -> Void

    var body: some View {
        let o = RunOutcomeStyle.of(log.outcomeKey)
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Image(systemName: o.icon).foregroundStyle(o.color)
                Button(log.taskId) { openTask(log.taskId) }.buttonStyle(.link).font(.body.monospaced())
                Text(log.title + (log.error.isEmpty ? "" : " · \(log.error)")).lineLimit(1).help(log.error.isEmpty ? log.title : log.error)
                Spacer(minLength: 0)
                RunWarnings(log: log)
                Text(log.agentName).font(.caption).foregroundStyle(.secondary).lineLimit(1).frame(width: 90, alignment: .leading)
                Pill(text: o.label, color: o.color)
                Text(Fmt.duration(TimeInterval(log.durationSeconds))).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                    .frame(width: 70, alignment: .trailing).help("耗時")
                Text(String(format: "$%.2f", log.costUsd)).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                    .frame(width: 56, alignment: .trailing).help("Claude 估計花費(換算成 API 價格;用訂閱不另外收費)")
                Text(log.finishedAt, format: .dateTime.month().day().hour().minute()).font(.caption).foregroundStyle(.secondary)
                    .frame(width: 90, alignment: .trailing)
                Button(open ? "收合" : "執行日誌 \(log.entries.count)", action: toggle).controlSize(.small)
            }
            if open { RunLogPanel(log: log) }
        }
        .padding(.vertical, 3)
    }
}

/// 一眼看出這筆執行有問題:角色沒回報(由 omixor 補)、整個過程沒呼叫任何工具
private struct RunWarnings: View {
    let log: RunLog

    var body: some View {
        if log.reportedBy == RunReportedBy.runner {
            Pill(text: "由 omixor 補回報", color: .orange, icon: "exclamationmark.triangle")
        }
        if log.toolCalls == 0 && log.serverCalls == 0 {
            Pill(text: "沒有呼叫任何工具", color: .red, icon: "exclamationmark.triangle")
        }
    }
}

/// 展開的執行日誌:依序的時間線;文字摘錄預設收合
private struct RunLogPanel: View {
    let log: RunLog

    private var reporter: String {
        switch log.reportedBy {
        case RunReportedBy.role: return "角色自己"
        case RunReportedBy.runner: return "omixor 補回報"
        default: return "未知"
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("\(log.model.isEmpty ? "模型未知" : log.model) · MCP \(log.mcpStatus.isEmpty ? "未知" : log.mcpStatus) · 工具呼叫 \(log.toolCalls)、伺服器確認 \(log.serverCalls) · 回報者 \(reporter)"
                 + (log.truncated ? " · 項目過多,後面的已省略" : ""))
                .font(.caption).foregroundStyle(.secondary)
            ForEach(log.entries) { RunLogLine(item: $0) }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.quaternary.opacity(0.3), in: RoundedRectangle(cornerRadius: 6))
    }
}

private struct RunLogLine: View {
    let item: RunLogItem
    private static let kinds = ["start": "開始", "text": "文字", "tool_call": "呼叫", "tool_result": "結果", "server_call": "伺服器", "error": "錯誤", "end": "結束"]
    private var time: String { item.at.formatted(date: .omitted, time: .standard) }

    var body: some View {
        let bad = item.status == "error" || item.kind == "error"
        if item.kind == "text" {
            DisclosureGroup("\(time) 文字:\(String((item.text ?? "").prefix(40)))…") {
                Text(item.text ?? "").textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
            }.font(.caption)
        } else {
            HStack(spacing: 8) {
                Text(time).foregroundStyle(.secondary).monospacedDigit()
                Text(Self.kinds[item.kind] ?? item.kind)
                if let t = item.tool { Text(t).font(.system(.caption, design: .monospaced)) }
                if let a = item.args { Text(a).foregroundStyle(.secondary).lineLimit(1).help(a) }
                if let s = item.status { Pill(text: s == "ok" ? "ok" : "error", color: s == "ok" ? .green : .red) }
                if item.kind != "tool_call", let t = item.text { Text(t).foregroundStyle(bad ? .orange : .secondary).lineLimit(1).help(t) }
            }.font(.caption).foregroundStyle(bad ? .orange : .primary)
        }
    }
}
