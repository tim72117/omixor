import SwiftUI

/// 「自動執行」精簡狀態列(對應 web 的 RunnerStrip.tsx):claude 狀態、執行中的任務(可停止)、最近的錯誤,
/// 以及到「執行日誌」頁籤的連結。歷史與日誌在 AutoLogView;各角色的設定在 AutoRunnerView。
struct AutoRunnerStrip: View {
    @EnvironmentObject var app: AppState
    @ObservedObject var runner: AgentRunner
    /// 到「執行日誌」頁籤
    let openLog: () -> Void
    /// 點執行中的任務 id
    let openTask: (String) -> Void
    @State private var confirmStop = false

    var body: some View {
        Card("自動執行", subtitle: "開了自動執行的角色被派發任務時,在這台 Mac 上以 Claude Code 自動處理,一次一筆") {
            HStack(spacing: 6) {
                claudePill
                if !runner.featureEnabled { Pill(text: "系統設定已關閉", color: .orange, icon: "slash.circle") }
                Spacer()
                Button("查看執行日誌", action: openLog).controlSize(.small)
            }
            if !runner.featureEnabled {
                Text("「自動執行」在 系統 › 功能開關 關閉了:不會領新的任務,也不會因 @ 排回覆任務;進行中的那筆會做完")
                    .font(.caption).foregroundStyle(.orange)
            }
            if !runner.claudeFound {
                Text("請先安裝 Claude Code 並用訂閱帳號登入(終端機執行 claude 再輸入 /login)。").font(.caption).foregroundStyle(.secondary)
            } else if runner.login == .loggedOut {
                Text(AgentRunner.loginOutHint).font(.caption).foregroundStyle(.orange)
            }
            if !runner.serverReady {
                Text("還連不上 omixor server(本機模式是本機服務):連上後才會自動執行").font(.caption).foregroundStyle(.secondary)
            }
            if let e = runner.lastError { Text(e).font(.caption).foregroundStyle(.orange).textSelection(.enabled) }
            running
        }
        .alert("停止 \(runner.running?.task.id ?? "")?任務會回報「需要協助」(人工停止)。", isPresented: $confirmStop) {
            Button("取消", role: .cancel) {}
            Button("停止", role: .destructive) { runner.stop() }
        }
    }

    @ViewBuilder private var claudePill: some View {
        if !runner.claudeFound {
            Pill(text: "找不到 claude", color: .orange, icon: "exclamationmark.triangle")
        } else {
            switch runner.login {
            case .loggedIn: Pill(text: "claude 已就緒", color: .green, icon: "checkmark.circle")
            case .loggedOut: Pill(text: "claude 尚未登入", color: .orange, icon: "exclamationmark.triangle")
            case .unknown: Pill(text: "檢查中…", color: .gray)
            }
        }
    }

    @ViewBuilder private var running: some View {
        if let r = runner.running {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Button(r.task.id) { openTask(r.task.id) }.buttonStyle(.link).font(.body.monospaced())
                    Text(r.task.title).lineLimit(1)
                    Spacer()
                    Text("\(app.agents.first { $0.id == r.task.agentId }?.name ?? r.task.agentId) · \(Fmt.relative(r.started))開始")
                        .font(.caption).foregroundStyle(.secondary)
                    Button("停止", role: .destructive) { confirmStop = true }.controlSize(.small)
                }
                if !r.lastText.isEmpty {
                    Text(r.lastText).font(.caption).foregroundStyle(.secondary).lineLimit(3).textSelection(.enabled)
                }
            }
            .padding(8)
            .background(Color.blue.opacity(0.08), in: RoundedRectangle(cornerRadius: 6))
        } else {
            Text("目前沒有在執行的任務").foregroundStyle(.secondary)
        }
    }
}
