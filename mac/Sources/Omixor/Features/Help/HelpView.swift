import SwiftUI

/// 需要協助:需要真人處理的事集中在這裡
///   - 卡住(blocked)的角色任務:顯示角色最後的問題或說明,可以回覆並重新排入
///   - 頻道裡 @ 我、還沒讀的訊息:按了跳到該頻道
struct HelpView: View {
    @EnvironmentObject var app: AppState

    private var blocked: [AgentTask] { NeedsHelpModel.tasks(app.agentTasks) }

    var body: some View {
        Group {
            if blocked.isEmpty && app.myMentions.isEmpty {
                EmptyStateView(title: "目前沒有需要你處理的事", icon: "hand.thumbsup",
                               message: "角色回報「需要協助」的任務,和頻道裡 @ 你的未讀訊息會列在這裡。")
            } else {
                PageScroll {
                    if !blocked.isEmpty {
                        header("卡住的任務", count: blocked.count, icon: "hand.raised")
                        NeedsHelpList(tasks: blocked) { app.go(.automation, focus: $0.id) }
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
