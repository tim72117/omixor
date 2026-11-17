import SwiftUI

/// 需要協助:需要真人處理的事集中在這裡
///   - 卡住(blocked)的角色任務:顯示角色最後的問題或說明,可以回覆並重新排入
///   - 頻道裡 @ 我、還沒讀的訊息:按了跳到該頻道
struct HelpView: View {
    @EnvironmentObject var app: AppState

    private var blocked: [AgentTask] {
        app.agentTasks.filter { $0.status == .blocked }.sorted { $0.updatedAt > $1.updatedAt }
    }

    var body: some View {
        Group {
            if blocked.isEmpty && app.myMentions.isEmpty {
                EmptyStateView(title: "目前沒有需要你處理的事", icon: "hand.thumbsup",
                               message: "角色回報「需要協助」的任務,和頻道裡 @ 你的未讀訊息會列在這裡。")
            } else {
                PageScroll {
                    if !blocked.isEmpty {
                        header("卡住的任務", count: blocked.count, icon: "hand.raised")
                        ForEach(blocked) { BlockedTaskCard(task: $0) }
                    }
                    if !app.myMentions.isEmpty {
                        header("提到我的訊息", count: app.myMentions.count, icon: "at")
                            .padding(.top, blocked.isEmpty ? 0 : 8)
                        ForEach(app.myMentions, id: \.self) { MentionCard(mention: $0) }
                    }
                }
            }
        }
        .controlSize(.large)
        .navigationTitle("需要協助")
        // 打開頁面時在背景重讀(不擋主執行緒)
        .task {
            let agents = app.startAgents(), channels = app.startChannels()
            async let a: Bool = agents.reload()
            async let c: Void = channels.reload()
            _ = await (a, c)
        }
    }

    private func header(_ title: String, count: Int, icon: String) -> some View {
        Label("\(title)(\(count))", systemImage: icon).font(.title2.weight(.semibold))
    }
}

private struct BlockedTaskCard: View {
    @EnvironmentObject var app: AppState
    let task: AgentTask
    @State private var reply = ""
    @State private var error: String?
    @State private var busy = false
    @State private var expanded = false

    private var agent: Agent? { app.agents.first { $0.id == task.agentId } }

    /// 角色的問題:回報 blocked 時寫的結果(通常最完整);沒有就用最後一則過程記錄
    private var question: String {
        if !task.result.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return task.result }
        return task.notes.last?.text ?? ""
    }

    var body: some View {
        Card {
            VStack(alignment: .leading, spacing: 10) {
                HStack(spacing: 8) {
                    if let a = agent { AgentAvatar(agent: a, size: 24) }
                    VStack(alignment: .leading, spacing: 2) {
                        Text(task.title).font(.title3.weight(.semibold))
                            .fixedSize(horizontal: false, vertical: true)
                        Text("\(agent?.name ?? task.agentId) · \(task.id) · \(Fmt.relative(task.updatedAt))")
                            .font(.callout).foregroundStyle(.secondary)
                    }
                    Spacer()
                    if let iss = task.issueId {
                        Button(iss) { app.go(.development, focus: iss) }.buttonStyle(.link).font(.body)
                    }
                    Button("打開任務") { app.go(.automation, focus: task.id) }.buttonStyle(.link).font(.body)
                }
                if question.isEmpty {
                    Text("角色沒有留下說明").font(.body).foregroundStyle(.secondary)
                } else {
                    let short = Self.excerpt(question)
                    let long = short != question
                    // 摺起時只取開頭幾行(不用固定高度裁切,避免每行被截成「…」);文字一律換行顯示完整
                    MarkdownText(long && !expanded ? short + "…" : question)
                        .font(.title3)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(10)
                        .frame(maxWidth: .infinity, alignment: .topLeading)
                        .background(.orange.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
                    if long {
                        Button(expanded ? "收起" : "展開全部") { expanded.toggle() }
                            .buttonStyle(.link).font(.callout)
                    }
                }
                HStack(alignment: .bottom, spacing: 8) {
                    TextField("回覆角色(會加進任務說明,重新排入後角色會看到)", text: $reply, axis: .vertical)
                        .textFieldStyle(.roundedBorder)
                        .font(.title3)
                        .lineLimit(1...6)
                    Button(busy ? "送出中…" : "回覆並重新排入") { send(requeue: true) }
                        .buttonStyle(.borderedProminent)
                        .disabled(busy || reply.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    Button("只重新排入") { send(requeue: false) }
                        .disabled(busy)
                }
                if let error { Text(error).font(.callout).foregroundStyle(.red) }
            }
        }
    }

    /// 摺起時顯示的開頭:最多 6 行、400 字(沒有超過就原樣回傳)
    static func excerpt(_ text: String, lines: Int = 6, chars: Int = 400) -> String {
        var out = text.split(separator: "\n", omittingEmptySubsequences: false).prefix(lines).joined(separator: "\n")
        if out.count > chars { out = String(out.prefix(chars)) }
        return out
    }

    /// 有回覆就先加說明,再重新排入(遠端要組織管理員)。請求在背景跑,送出期間按鈕停用
    private func send(requeue withReply: Bool) {
        guard !busy else { return }
        let store = app.startAgents()
        let text = withReply ? reply.trimmingCharacters(in: .whitespacesAndNewlines) : ""
        let id = task.id
        busy = true
        error = nil
        Task {
            defer { busy = false }
            if !text.isEmpty {
                do { _ = try await store.reportAsync(id, note: text) } catch {
                    self.error = "回覆失敗:\(error.localizedDescription)"
                    return
                }
            }
            if await store.requeueAsync(id) == nil {
                error = (text.isEmpty ? "" : "已留下回覆,但") + "重新排入失敗:\(store.remoteError ?? "未知錯誤")(遠端要組織管理員)"
                return
            }
            reply = ""
        }
    }
}

private struct MentionCard: View {
    @EnvironmentObject var app: AppState
    let mention: ChannelInbox.Mention

    var body: some View {
        Card {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Image(systemName: mention.author.hasPrefix("agent:") ? "sparkles" : "person")
                        .foregroundStyle(.secondary)
                    Text(app.startChannels().name(of: mention.author)).font(.title3.weight(.semibold))
                    Text("在 #\(mention.channelName) · \(Fmt.relative(mention.at))")
                        .font(.callout).foregroundStyle(.secondary)
                    Spacer()
                    Button("到頻道回覆") { app.go(.channels, focus: mention.channelId) }.buttonStyle(.link).font(.body)
                }
                MarkdownText(mention.text).font(.title3)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}
