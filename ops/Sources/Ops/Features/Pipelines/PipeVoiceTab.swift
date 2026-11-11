import AVFoundation
import SwiftUI

/// CI/CD › 旁白發佈:送出旁白語音批次、等它完成、試聽、按下發佈到 evoleap。
struct PipeVoiceTab: View {
    @EnvironmentObject var app: AppState
    @State private var scriptsText = ""
    @State private var all = false
    @State private var limit = 0
    @State private var formError: String?
    @State private var dryRunOutput: String?
    @State private var dryRunning = false
    @StateObject private var player = ClipPlayer()

    private var voice: VoicePipeline? { app.voice }

    var body: some View {
        PageScroll {
            connectionCard
            newBatchCard
            if app.voiceJobs.isEmpty {
                Card { EmptyStateView(title: "還沒有旁白批次", icon: "waveform",
                                      message: "在上面送出批次,或讓 AI 透過 MCP 的 ops_voice_submit 分派過來。").frame(minHeight: 160) }
            } else {
                ForEach(app.voiceJobs) { job in
                    VoiceJobCard(job: job, player: player)
                }
            }
        }
        .sheet(item: Binding(get: { dryRunOutput.map(DryRun.init) }, set: { dryRunOutput = $0?.text })) { d in
            VStack(alignment: .leading, spacing: 12) {
                Text("試算結果(沒有呼叫 API,不花錢)").font(.headline)
                ScrollView {
                    Text(d.text).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                HStack { Spacer(); Button("關閉") { dryRunOutput = nil }.keyboardShortcut(.defaultAction) }
            }
            .padding(20)
            .frame(width: 720, height: 460)
        }
    }

    // MARK: - 連線

    private var connectionCard: some View {
        let target = voice?.target
        let state = target.flatMap { app.connectionStates[$0.id] }
        return Card("發佈目的地", subtitle: "發佈 = 把 .voice.json 寫進 evoleap 的課程資料庫(PUT /api/admin/scripts),線上立即生效,不用重新部署;寫完會用 API 讀回比對") {
            HStack(spacing: 12) {
                Picker("連線", selection: Binding(get: { voice?.targetId ?? "" }, set: { voice?.targetId = $0 })) {
                    ForEach(app.connections) { c in
                        Text(c.name + (c.isProduction ? "(正式)" : "")).tag(c.id)
                    }
                }
                .labelsHidden()
                .fixedSize()
                if let target {
                    if state?.busy == true {
                        ProgressView().controlSize(.small)
                        Text(target.auth == .approval ? "請在瀏覽器以管理員身分按「核准」" : "請在瀏覽器完成登入").font(.callout).foregroundStyle(.secondary)
                    } else if let account = state?.account {
                        Pill(text: account, color: .green, icon: "person.crop.circle.badge.checkmark")
                        Button("登出") { Task { await app.connStore?.logout(target.id) } }
                    } else {
                        Button("登入「\(target.name)」…") { Task { await app.connStore?.login(target.id) } }
                    }
                }
                Spacer()
                Button("管理連線…") {
                    UserDefaults.standard.set(SettingsPane.connections.rawValue, forKey: SettingsKeys.pane)
                    app.go(.settings)
                }
            }
            if let err = state?.error {
                Label(err, systemImage: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.orange)
            }
            HStack(spacing: 16) {
                LabeledContent("shuttle 專案") {
                    Text(voice?.shuttleRoot.path ?? "—").font(.caption.monospaced()).textSelection(.enabled)
                }
                Stepper(value: Binding(get: { voice?.pollMinutes ?? 10 }, set: { voice?.pollMinutes = $0 }), in: 1...120) {
                    Text("每 \(voice?.pollMinutes ?? 10) 分鐘查一次").font(.callout)
                }
                .fixedSize()
            }
        }
    }

    // MARK: - 新批次

    private var newBatchCard: some View {
        Card("新的旁白批次", subtitle: "執行 npm run voice -- --batch:缺的旁白整包送到 Gemini Batch API(約一半價格,24 小時內完成)。一次只能有一批,其餘排隊") {
            HStack(alignment: .firstTextBaseline, spacing: 12) {
                TextField("腳本,例如 launch/2.1.json launch/2.2.json", text: $scriptsText)
                    .textFieldStyle(.roundedBorder)
                    .disabled(all)
                Toggle("全部腳本", isOn: $all).toggleStyle(.checkbox).fixedSize()
                Stepper(value: $limit, in: 0...200) {
                    Text(limit == 0 ? "不限段數" : "每份前 \(limit) 段").font(.callout)
                }
                .fixedSize()
            }
            HStack {
                if let formError {
                    Label(formError, systemImage: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.red)
                }
                Spacer()
                Button {
                    dryRun()
                } label: {
                    if dryRunning { ProgressView().controlSize(.small) } else { Text("試算") }
                }
                .disabled(dryRunning)
                Button("送出批次") { submit() }
                    .buttonStyle(.borderedProminent)
            }
        }
    }

    private var scripts: [String] { all ? [] : [scriptsText] }

    private func submit() {
        do {
            try voice?.submit(scripts: scripts, all: all, limit: limit > 0 ? limit : nil, by: .person(app.me))
            formError = nil
            scriptsText = ""
        } catch {
            formError = error.localizedDescription
        }
    }

    private func dryRun() {
        dryRunning = true
        Task {
            defer { dryRunning = false }
            do {
                dryRunOutput = try await voice?.dryRun(scripts: scripts, all: all, limit: limit > 0 ? limit : nil)
                formError = nil
            } catch {
                formError = error.localizedDescription
            }
        }
    }

    private struct DryRun: Identifiable {
        let text: String
        var id: String { text }
    }
}

// MARK: - 一個批次

private struct VoiceJobCard: View {
    @EnvironmentObject var app: AppState
    let job: VoiceJob
    @ObservedObject var player: ClipPlayer
    @State private var confirming = false

    private var target: Connection? { app.voice?.target }
    private var isProduction: Bool { target?.isProduction ?? true }
    private var loggedIn: Bool { target.map { app.connectionStates[$0.id]?.account != nil } ?? false }

    private var color: Color {
        switch job.state {
        case .ready: return .purple
        case .published, .nothingToDo: return .green
        case .failed: return .red
        case .canceled, .queued: return .gray
        default: return .blue
        }
    }

    var body: some View {
        Card {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(job.id).font(.headline.monospaced())
                Text(job.title).font(.headline).lineLimit(1)
                Pill(text: job.state.label, color: color, icon: job.state.icon)
                Spacer()
                Text(byLabel + " · " + Fmt.relative(job.createdAt)).font(.caption).foregroundStyle(.secondary)
            }
            details
            if let err = job.error, !job.state.isFinal || job.state == .failed {
                Label(err, systemImage: "exclamationmark.triangle.fill").font(.caption).foregroundStyle(.orange)
                    .textSelection(.enabled)
            }
            if !job.changes.isEmpty { changes }
            if !job.failedClips.isEmpty {
                DisclosureGroup("\(job.failedClips.count) 段產生失敗(重送一次會補上)") {
                    ForEach(job.failedClips, id: \.self) { Text($0).font(.caption.monospaced()).textSelection(.enabled) }
                }
                .font(.caption)
            }
            actions
            DisclosureGroup("紀錄(\(job.log.count))") {
                VStack(alignment: .leading, spacing: 3) {
                    ForEach(Array(job.log.enumerated().reversed()), id: \.offset) { _, line in
                        HStack(alignment: .top, spacing: 8) {
                            Text(Fmt.time(line.date)).foregroundStyle(.secondary)
                            Text(line.text).textSelection(.enabled)
                        }
                        .font(.caption.monospaced())
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .font(.caption)
        }
        .confirmationDialog("發佈到「\(target?.name ?? "?")」?", isPresented: $confirming) {
            Button("發佈 \(job.changes.filter { $0.publish != "ok" }.count) 份旁白清單", role: isProduction ? .destructive : nil) {
                Task { await app.voice?.publish(job.id) }
            }
        } message: {
            Text((isProduction ? "這會直接改正式站的課程資料,學員立刻聽到新的旁白。\n" : "")
                 + job.changes.filter { $0.publish != "ok" }
                    .map { c in "• \(c.path)" + (c.course.map { " → \($0) \(c.lesson ?? "")" } ?? "(研發基地)") }
                    .joined(separator: "\n"))
        }
    }

    private var byLabel: String {
        switch job.by {
        case "external": return "終端機送出"
        case Actor.mcpId: return "MCP 用戶端"
        default: return app.person(job.by)?.name ?? job.by
        }
    }

    private var details: some View {
        HStack(spacing: 16) {
            if let n = job.clipCount { info("段落", "\(n)") }
            if let b = job.batchName { info("批次", b) }
            if let s = job.batchState { info("狀態", s.replacingOccurrences(of: "JOB_STATE_", with: "")) }
            if job.checks > 0 { info("已查", "\(job.checks) 次") }
            if job.state == .processing, let next = job.nextCheckAt {
                info("下次查", next <= .now ? "馬上" : Fmt.relative(next))
            }
            if let at = job.publishedAt { info("發佈於", Fmt.relative(at) + " → " + (job.publishedTo ?? "")) }
        }
        .font(.caption)
    }

    private func info(_ k: String, _ v: String) -> some View {
        HStack(spacing: 4) {
            Text(k).foregroundStyle(.secondary)
            Text(v).monospacedDigit().textSelection(.enabled).lineLimit(1)
        }
    }

    private var changes: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text("變動的旁白清單").font(.subheadline.weight(.semibold))
                Text("新增 \(job.changes.reduce(0) { $0 + $1.added.count }) 段,共 \(String(format: "%.0f", Double(job.totalAddedMs) / 1000)) 秒")
                    .font(.caption).foregroundStyle(.secondary)
            }
            ForEach(job.changes, id: \.path) { c in
                DisclosureGroup {
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(c.added, id: \.file) { clip in
                            HStack(spacing: 8) {
                                Button {
                                    player.toggle(clip)
                                } label: {
                                    Image(systemName: player.playing == clip.file ? "stop.circle.fill" : "play.circle.fill")
                                }
                                .buttonStyle(.borderless)
                                Text(String(format: "%.1f 秒", Double(clip.ms) / 1000)).monospacedDigit().foregroundStyle(.secondary)
                                    .frame(width: 56, alignment: .trailing)
                                Text(clip.text).lineLimit(2).textSelection(.enabled)
                            }
                            .font(.caption)
                        }
                        if c.added.isEmpty { Text("沒有新增段落(只有內容順序或長度改變)").font(.caption).foregroundStyle(.secondary) }
                    }
                    .padding(.leading, 4)
                } label: {
                    HStack(spacing: 8) {
                        Text(c.path).font(.callout.monospaced())
                        Pill(text: c.isNew ? "新檔" : "更新", color: c.isNew ? .teal : .blue)
                        if let course = c.course, let lesson = c.lesson {
                            Text("\(course) / \(lesson)").font(.caption).foregroundStyle(.secondary)
                        } else {
                            Text("不屬於任何一課(會放進研發基地)").font(.caption).foregroundStyle(.orange)
                        }
                        Text("+\(c.added.count) 段 −\(c.removed) 段 · 共 \(c.clips) 段 \(String(format: "%.0f", Double(c.totalMs) / 1000)) 秒")
                            .font(.caption).foregroundStyle(.secondary)
                        Spacer()
                        switch c.publish {
                        case nil: EmptyView()
                        case "ok": Pill(text: "已發佈", color: .green, icon: "checkmark")
                        case let err?: Pill(text: "失敗", color: .red, icon: "xmark").help(err)
                        }
                    }
                }
            }
        }
    }

    @ViewBuilder private var actions: some View {
        HStack {
            if job.state == .processing {
                Button("立即檢查") { app.voice?.checkNow(job.id) }
            }
            if job.state == .queued || job.state == .failed {
                Button("取消") { app.voice?.cancel(job.id) }
            }
            Spacer()
            if job.state == .ready || job.state == .publishing {
                if !loggedIn {
                    Text("要先登入「\(target?.name ?? "發佈目的地")」才能發佈").font(.caption).foregroundStyle(.secondary)
                }
                Button {
                    confirming = true
                } label: {
                    if job.state == .publishing { ProgressView().controlSize(.small) }
                    else { Label("發佈到「\(target?.name ?? "?")」", systemImage: "paperplane.fill") }
                }
                .buttonStyle(.borderedProminent)
                .tint(isProduction ? .red : .accentColor)
                .disabled(!loggedIn || job.state == .publishing)
            }
        }
    }
}

/// 試聽 GCS 上的音檔
@MainActor
final class ClipPlayer: ObservableObject {
    @Published var playing: String?
    private var player: AVPlayer?
    private var observer: NSObjectProtocol?

    func toggle(_ clip: VoiceClip) {
        if playing == clip.file { stop(); return }
        guard let url = clip.url else { return }
        stop()
        let item = AVPlayerItem(url: url)
        observer = NotificationCenter.default.addObserver(forName: .AVPlayerItemDidPlayToEndTime, object: item, queue: .main) { [weak self] _ in
            Task { @MainActor in self?.stop() }
        }
        player = AVPlayer(playerItem: item)
        player?.play()
        playing = clip.file
    }

    func stop() {
        player?.pause()
        player = nil
        if let observer { NotificationCenter.default.removeObserver(observer) }
        observer = nil
        playing = nil
    }
}
