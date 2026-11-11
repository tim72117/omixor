import AppKit
import SwiftUI

// AI 自動化 › 粉絲專頁:連線引導、撰寫與排程(由人發佈)、貼文、成效。
// 放在 AI 自動化:草稿多半由行銷角色(虛擬角色)寫好,人在這裡把關後發佈,和「待核准」是同一種流程;
// 也不用新增側邊欄頁面(那會改到 ops_navigate 的 section 列舉與 ⌘1–⌘9)。

private enum FBTab: String, CaseIterable, Identifiable {
    case compose, posts, insights
    var id: String { rawValue }
    var label: String { ["compose": "撰寫與排程", "posts": "貼文", "insights": "成效"][rawValue]! }
}

struct AutoFacebookView: View {
    @EnvironmentObject var app: AppState
    @State private var tab: FBTab = .compose
    @State private var pageId = ""
    @State private var error: String?

    private var store: FacebookStore { app.startFacebook() }

    var body: some View {
        Group {
            if store.isConnected {
                VStack(spacing: 0) {
                    toolbar.padding(.horizontal, 20).padding(.vertical, 10)
                    Divider()
                    switch tab {
                    case .compose: FBComposeView()
                    case .posts: FBPostsView(pageId: currentPage)
                    case .insights: FBInsightsView(pageId: currentPage)
                    }
                }
            } else {
                PageScroll { FBConnectGuide() }
            }
        }
        .onAppear {
            if pageId.isEmpty { pageId = app.fbSettings.managedPages.first?.id ?? "" }
            if store.isConnected, app.facebook.fetchedAt == nil { Task { await store.refreshAll() } }
        }
    }

    private var currentPage: String {
        app.fbSettings.managedPages.contains { $0.id == pageId } ? pageId : (app.fbSettings.managedPages.first?.id ?? "")
    }

    private var toolbar: some View {
        HStack(spacing: 12) {
            Picker("分頁", selection: $tab) { ForEach(FBTab.allCases) { Text($0.label).tag($0) } }
                .pickerStyle(.segmented).labelsHidden().frame(maxWidth: 360)
            if tab != .compose {
                Picker("專頁", selection: $pageId) {
                    ForEach(app.fbSettings.managedPages) { Text($0.name).tag($0.id) }
                }
                .frame(maxWidth: 220)
            }
            Spacer()
            if app.facebook.tokenExpired {
                Pill(text: "權杖已過期,請重新連線", color: .red, icon: "exclamationmark.triangle.fill")
            }
            if let t = app.facebook.fetchedAt { Text("更新於 \(Fmt.time(t))").font(.caption).foregroundStyle(.secondary) }
            Button { Task { await store.refreshAll() } } label: {
                if app.facebook.busy { ProgressView().controlSize(.small) } else { Image(systemName: "arrow.clockwise") }
            }
            .disabled(app.facebook.busy)
            Menu {
                ForEach(app.fbSettings.pages) { p in
                    Toggle(p.name, isOn: Binding(get: { p.managed }, set: { store.setManaged(p.id, $0) }))
                }
                Divider()
                Button("中斷連線(清除權杖)", role: .destructive) { store.disconnect() }
            } label: { Image(systemName: "gearshape") }
            .menuStyle(.borderlessButton).fixedSize()
        }
    }
}

// MARK: - 連線引導

private struct FBConnectGuide: View {
    @EnvironmentObject var app: AppState
    @State private var appId = ""
    @State private var appSecret = ""
    @State private var pasted = ""
    @State private var busy = false
    @State private var error: String?

    private var store: FacebookStore { app.startFacebook() }
    private var conn: Connection? { app.connections.first { $0.id == FB.connectionId } }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            PageHeader("連線 Facebook 粉絲專頁", subtitle: "Ops 用 Meta Graph API(\(app.fbSettings.apiVersion))讀貼文與成效、由人發佈與排程。權杖存在這台 Mac 的鑰匙圈,依組織分開。")
            if let error { Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red).textSelection(.enabled) }
            Card("方法一:用 Meta 帳號登入(建議)", subtitle: "取得長期使用者權杖,再自動取得每個專頁的 Page access token") {
                VStack(alignment: .leading, spacing: 8) {
                    Text("1. 到 developers.facebook.com › 我的應用程式 建立應用程式(類型選「商家」),加入「Facebook 登入」產品。")
                    HStack {
                        Text("2. Facebook 登入 › 設定 › 有效的 OAuth 重新導向 URI 填:")
                        Text(FB.callbackURL).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                        Button("複製") { copy(FB.callbackURL) }.controlSize(.small)
                    }
                    Text("3. 應用程式設定 › 基本資料 抄下應用程式編號與密鑰,貼在下面。需要的權限:\(FB.scopes.replacingOccurrences(of: ",", with: "、"))(開發模式下,自己是應用程式管理員就能用)。")
                    HStack {
                        TextField(conn?.oauth.clientId.isEmpty == false ? conn!.oauth.clientId : "App ID", text: $appId).textFieldStyle(.roundedBorder)
                        SecureField(conn?.oauth.hasClientSecret == true ? "App secret(已存)" : "App secret", text: $appSecret).textFieldStyle(.roundedBorder)
                        Button("儲存") {
                            store.setApp(id: appId.isEmpty ? (conn?.oauth.clientId ?? "") : appId, secret: appSecret)
                            appSecret = ""
                        }
                        .disabled(appId.isEmpty && appSecret.isEmpty)
                    }
                    Button(busy ? "等待瀏覽器…" : "用瀏覽器登入") { run { try await store.loginWithMeta() } }
                        .buttonStyle(.borderedProminent)
                        .disabled(busy || conn?.oauth.clientId.isEmpty != false || conn?.oauth.hasClientSecret != true)
                    Text("Meta 若要求重新導向網址一定要 https,改用方法二。")
                        .font(.caption).foregroundStyle(.secondary)
                }
                .font(.callout)
            }
            Card("方法二:直接貼 Page access token", subtitle: "也可以貼使用者權杖,Ops 會讀出它管理的專頁") {
                VStack(alignment: .leading, spacing: 8) {
                    Text("在 Graph API Explorer 選你的應用程式,勾選上面的權限,「取得權杖」選你的粉絲專頁;再到存取權杖偵錯工具按「延長存取權杖」,用長期權杖產生的 Page token 不會過期。")
                    HStack {
                        SecureField("EAAG…", text: $pasted).textFieldStyle(.roundedBorder)
                        Button(busy ? "確認中…" : "加入") { run { try await store.addToken(pasted); pasted = "" } }
                            .disabled(busy || pasted.isEmpty)
                    }
                }
                .font(.callout)
            }
            if !app.fbSettings.pages.isEmpty {
                Card("專頁") {
                    ForEach(app.fbSettings.pages) { p in
                        Toggle(isOn: Binding(get: { p.managed }, set: { store.setManaged(p.id, $0) })) {
                            Text("\(p.name)(\(p.id))" + (FacebookStore.pageToken(p.id) == nil ? " · 沒有權杖" : ""))
                        }
                    }
                }
            }
        }
    }

    private func run(_ work: @escaping () async throws -> Void) {
        busy = true
        error = nil
        Task {
            do { try await work() } catch { self.error = error.localizedDescription }
            busy = false
        }
    }

    private func copy(_ s: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
    }
}

// MARK: - 撰寫與排程

private struct FBComposeView: View {
    @EnvironmentObject var app: AppState
    @State private var selection: String?
    @State private var edit = FBDraft(id: "", author: "")
    @State private var scheduleOn = false
    @State private var scheduleAt = Date().addingTimeInterval(3600)
    @State private var confirm = false
    @State private var busy = false
    @State private var message: (Bool, String)?

    private var store: FacebookStore { app.startFacebook() }
    private var drafts: [FBDraft] { app.fbSettings.drafts.sorted { $0.updatedAt > $1.updatedAt } }
    private var marketingTasks: [AgentTask] {
        app.agentTasks.filter { t in
            !t.result.isEmpty && (t.agentId == "marketing" || (app.agents.first { $0.id == t.agentId }?.name.contains("行銷") ?? false))
        }.sorted { $0.updatedAt > $1.updatedAt }
    }

    var body: some View {
        HSplitView {
            VStack(spacing: 0) {
                HStack {
                    Button { newDraft() } label: { Label("新草稿", systemImage: "square.and.pencil") }
                    Menu("帶入") {
                        Menu("文件櫃") {
                            ForEach(app.cabinetDocs.sorted { $0.updatedAt > $1.updatedAt }.prefix(30)) { d in
                                Button("\(d.id) \(d.title)") { pick(store.draftFromDoc(d)) }
                            }
                        }
                        Menu("行銷角色的任務結果") {
                            ForEach(marketingTasks.prefix(30)) { t in
                                Button("\(t.id) \(t.title)") { pick(store.draftFromTask(t)) }
                            }
                        }
                    }
                    .fixedSize()
                    Spacer()
                }
                .padding(10)
                List(selection: $selection) {
                    ForEach(drafts) { d in
                        VStack(alignment: .leading, spacing: 2) {
                            HStack {
                                Text(d.titleLine).lineLimit(1)
                                Spacer()
                                Pill(text: d.status.label, color: d.status == .draft ? .secondary : d.status == .scheduled ? .orange : .green)
                            }
                            Text("\(d.id) · \(store.authorName(d.author)) · \(Fmt.relative(d.updatedAt))")
                                .font(.caption).foregroundStyle(.secondary)
                        }
                        .tag(d.id)
                    }
                }
            }
            .frame(minWidth: 260, idealWidth: 300, maxWidth: 380)
            editor.frame(minWidth: 480, maxWidth: .infinity, maxHeight: .infinity)
        }
        .onChange(of: selection) { _, id in load(id) }
        .onAppear { if selection == nil { selection = drafts.first?.id }; load(selection) }
        .confirmationDialog(confirmTitle, isPresented: $confirm) {
            Button(scheduleOn ? "排程" : "發佈") { publish() }
            Button("取消", role: .cancel) {}
        } message: {
            Text("接著會要求 Touch ID 或電腦密碼確認。發佈後會出現在粉絲專頁上,動態牆會以你的名字記錄。")
        }
    }

    private var confirmTitle: String {
        let page = store.page(edit.pageId)?.name ?? "?"
        return scheduleOn ? "排程到「\(page)」:\(FB.taipeiString(scheduleAt))?" : "立即發佈到「\(page)」?"
    }

    private func newDraft() {
        var d = FBDraft(id: store.newDraftId(), author: app.me)
        d.pageId = app.fbSettings.managedPages.first?.id ?? ""
        pick(store.saveDraft(d, by: app.me))
    }

    private func pick(_ d: FBDraft) {
        selection = d.id
        load(d.id)
    }

    private func load(_ id: String?) {
        guard let id, let d = store.draft(id) else { edit = FBDraft(id: "", author: ""); return }
        edit = d
        scheduleOn = false
        if let p = d.proposedTime, p.timeIntervalSinceNow > FB.minLead { scheduleAt = p }
        message = nil
    }

    private var editable: Bool { !edit.id.isEmpty && edit.status == .draft }

    @ViewBuilder private var editor: some View {
        if edit.id.isEmpty {
            EmptyStateView(title: "選一份草稿", icon: "square.and.pencil", message: "或按「新草稿」。行銷角色也可以用 MCP 的 ops_fb_draft_save 寫好草稿放這裡。")
        } else {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    if let (ok, text) = message {
                        Label(text, systemImage: ok ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
                            .foregroundStyle(ok ? .green : .red).textSelection(.enabled)
                    }
                    Form {
                        Picker("粉絲專頁", selection: $edit.pageId) {
                            Text("未選擇").tag("")
                            ForEach(app.fbSettings.managedPages) { Text($0.name).tag($0.id) }
                        }
                        LabeledContent("內容") {
                            TextEditor(text: $edit.message).font(.body).frame(minHeight: 140)
                        }
                        TextField("連結", text: $edit.link)
                        TextField("圖片網址", text: $edit.imageURL)
                        LabeledContent("本機圖片") {
                            HStack {
                                Text(edit.localImage.isEmpty ? "無" : URL(fileURLWithPath: edit.localImage).lastPathComponent)
                                Button("選擇…") { chooseImage() }
                                if !edit.localImage.isEmpty { Button("移除") { edit.localImage = "" } }
                            }
                        }
                        if let p = edit.proposedTime {
                            LabeledContent("建議時間", value: FB.taipeiString(p))
                        }
                        if !edit.source.isEmpty { LabeledContent("來源", value: edit.source) }
                        LabeledContent("撰寫", value: store.authorName(edit.author))
                    }
                    .formStyle(.grouped)
                    .disabled(!editable)

                    Text("預覽").font(.headline)
                    FBPreview(draft: edit, page: store.page(edit.pageId))

                    if edit.status == .draft {
                        HStack {
                            Toggle("排程", isOn: $scheduleOn)
                            if scheduleOn {
                                DatePicker("時間", selection: $scheduleAt,
                                           in: Date().addingTimeInterval(FB.minLead + 60)...Date().addingTimeInterval(FB.maxLead))
                                    .environment(\.timeZone, FB.taipei)
                                    .labelsHidden()
                                Text("台北時間").font(.caption).foregroundStyle(.secondary)
                            }
                        }
                        HStack {
                            Button("儲存草稿") { save(); message = (true, "已儲存") }
                            Button("刪除", role: .destructive) { store.deleteDraft(edit.id); selection = drafts.first?.id }
                            Spacer()
                            Button(scheduleOn ? "排程…" : "立即發佈…") { save(); confirm = true }
                                .buttonStyle(.borderedProminent)
                                .disabled(busy || edit.isEmpty || store.page(edit.pageId) == nil)
                        }
                    } else {
                        Text(edit.status == .scheduled
                             ? "已排程:\(edit.scheduledFor.map(FB.taipeiString) ?? "")(到「貼文」分頁可以取消排程)"
                             : "已發佈:\(edit.publishedAt.map { $0.formatted() } ?? "")")
                            .foregroundStyle(.secondary)
                    }
                }
                .padding(20)
            }
        }
    }

    private func save() {
        guard editable else { return }
        store.saveDraft(edit, by: app.me)
    }

    private func chooseImage() {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [.png, .jpeg, .gif, .heic]
        panel.allowsMultipleSelection = false
        if panel.runModal() == .OK, let url = panel.url { edit.localImage = url.path }
    }

    private func publish() {
        busy = true
        message = nil
        let id = edit.id
        let when = scheduleOn ? scheduleAt : nil
        Task {
            do {
                let post = try await store.publish(id, schedule: when)
                message = (true, when == nil ? "已發佈(\(post))" : "已排程:\(FB.taipeiString(when!))")
                load(id)
            } catch {
                message = (false, error.localizedDescription)
            }
            busy = false
        }
    }
}

/// 簡單模擬 Facebook 貼文的樣子
private struct FBPreview: View {
    let draft: FBDraft
    let page: FBPage?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Circle().fill(Color.blue.opacity(0.2)).frame(width: 32, height: 32)
                    .overlay(Text(String((page?.name ?? "?").prefix(1))).font(.headline))
                VStack(alignment: .leading) {
                    Text(page?.name ?? "未選擇專頁").font(.subheadline.weight(.semibold))
                    Text("剛剛 · 公開").font(.caption2).foregroundStyle(.secondary)
                }
            }
            if !draft.message.isEmpty { Text(draft.message).textSelection(.enabled) }
            if !draft.imageURL.isEmpty, let u = URL(string: draft.imageURL) {
                AsyncImage(url: u) { $0.resizable().scaledToFit() } placeholder: { Color.secondary.opacity(0.1).frame(height: 160) }
                    .frame(maxHeight: 260)
            } else if !draft.localImage.isEmpty, let img = NSImage(contentsOfFile: draft.localImage) {
                Image(nsImage: img).resizable().scaledToFit().frame(maxHeight: 260)
            }
            if !draft.link.isEmpty {
                Label(draft.link, systemImage: "link").font(.caption).foregroundStyle(.blue).lineLimit(1)
            }
        }
        .padding(12)
        .frame(maxWidth: 500, alignment: .leading)
        .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.primary.opacity(0.1)))
    }
}

// MARK: - 貼文

private struct FBPostsView: View {
    @EnvironmentObject var app: AppState
    let pageId: String
    @State private var cancelId: String?
    @State private var error: String?

    private var store: FacebookStore { app.startFacebook() }

    var body: some View {
        PageScroll {
            if let e = app.facebook.errors[pageId] { Label(e, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red).textSelection(.enabled) }
            if let error { Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red) }
            Card("已排程", subtitle: "時間為台北時間") {
                let list = app.facebook.scheduled[pageId] ?? []
                if list.isEmpty { Text("沒有排程中的貼文").foregroundStyle(.secondary) }
                ForEach(list) { p in
                    HStack {
                        VStack(alignment: .leading) {
                            Text(p.message.isEmpty ? "(沒有文字)" : p.message).lineLimit(2)
                            Text(p.scheduledTime.map(FB.taipeiString) ?? "—").font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("取消排程…", role: .destructive) { cancelId = p.id }
                    }
                }
            }
            Card("最近貼文") {
                let list = app.facebook.posts[pageId] ?? []
                if list.isEmpty { Text(app.facebook.busy ? "讀取中…" : "沒有貼文").foregroundStyle(.secondary) }
                ForEach(list) { p in FBPostRow(post: p) }
            }
        }
        .confirmationDialog("取消這篇排程貼文?", isPresented: Binding(get: { cancelId != nil }, set: { if !$0 { cancelId = nil } })) {
            Button("取消排程", role: .destructive) {
                guard let id = cancelId else { return }
                Task {
                    do { try await store.cancelScheduled(id, pageId: pageId) } catch { self.error = error.localizedDescription }
                }
            }
            Button("保留", role: .cancel) {}
        } message: { Text("貼文會從 Facebook 刪除,草稿會回到「草稿」狀態。") }
    }
}

private struct FBPostRow: View {
    let post: FBPost

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .top) {
                Text(post.message.isEmpty ? "(沒有文字)" : post.message).lineLimit(3)
                Spacer()
                if let u = URL(string: post.permalink), !post.permalink.isEmpty { Link("開啟", destination: u).font(.caption) }
            }
            HStack(spacing: 14) {
                stat("心情", post.reactions)
                stat("留言", post.comments)
                stat("分享", post.shares)
                stat("互動", post.engagement)
                ForEach(post.insights.filter { $0.key != "reactions" }) { m in metric(m) }
                Spacer()
                Text(post.createdTime.map(FB.taipeiString) ?? "").font(.caption).foregroundStyle(.secondary)
            }
            if let r = post.insights.first(where: { $0.key == "reactions" }) {
                if let b = r.breakdown, !b.isEmpty {
                    Text(b.sorted { $0.value > $1.value }.map { "\(reactionLabel($0.key)) \(Int($0.value))" }.joined(separator: " · "))
                        .font(.caption).foregroundStyle(.secondary)
                } else if r.status != .ok {
                    Text("心情分類:\(r.statusLabel)").font(.caption).foregroundStyle(.secondary).help(r.note ?? "")
                }
            }
        }
        .padding(.vertical, 4)
    }

    private func stat(_ label: String, _ v: Int?) -> some View {
        VStack(alignment: .leading) {
            Text(label).font(.caption2).foregroundStyle(.secondary)
            Text(v.map(String.init) ?? "無資料").monospacedDigit()
        }
    }

    private func metric(_ m: FBMetric) -> some View {
        VStack(alignment: .leading) {
            Text(m.label).font(.caption2).foregroundStyle(.secondary)
            Text(m.status == .ok ? (m.total.map { Fmt.number($0) } ?? "無資料") : m.statusLabel)
                .monospacedDigit().foregroundStyle(m.status == .ok ? .primary : .secondary)
        }
        .help(m.note ?? m.metric ?? "")
    }

    private func reactionLabel(_ k: String) -> String {
        ["like": "讚", "love": "大心", "wow": "哇", "haha": "哈", "sorry": "嗚", "anger": "怒", "care": "加油"][k] ?? k
    }
}

// MARK: - 成效

private struct FBInsightsView: View {
    @EnvironmentObject var app: AppState
    let pageId: String
    @State private var period = "day"
    @State private var days = 28
    @State private var error: String?

    private var store: FacebookStore { app.startFacebook() }
    private var page: FBPage? { store.page(pageId) }

    var body: some View {
        PageScroll {
            HStack {
                Picker("期間", selection: $period) {
                    Text("每天").tag("day"); Text("每週").tag("week"); Text("每 28 天").tag("days_28")
                }
                .fixedSize()
                Picker("範圍", selection: $days) {
                    Text("7 天").tag(7); Text("28 天").tag(28); Text("90 天").tag(90)
                }
                .fixedSize()
                Spacer()
            }
            if let e = error ?? app.facebook.errors[pageId] {
                Label(e, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red).textSelection(.enabled)
            }
            HStack(spacing: 12) {
                MetricTile(title: "追蹤數(專頁欄位)", value: page?.followersCount.map { Fmt.number(Double($0)) } ?? "無資料", icon: "person.2")
                MetricTile(title: "按讚數", value: page?.fanCount.map { Fmt.number(Double($0)) } ?? "無資料", icon: "hand.thumbsup")
            }
            let metrics = app.facebook.insights[pageId]?.metrics ?? []
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 220), spacing: 12)], spacing: 12) {
                ForEach(metrics) { m in
                    if m.status == .ok {
                        MetricTile(title: m.label, value: m.total.map { Fmt.number($0) } ?? "無資料", icon: "chart.xyaxis.line",
                                   series: m.values.map(\.value))
                            .help(m.metric ?? "")
                    } else {
                        VStack(alignment: .leading, spacing: 6) {
                            Text(m.label).font(.subheadline).foregroundStyle(.secondary)
                            Text(m.statusLabel).font(.title3.weight(.semibold)).foregroundStyle(.secondary)
                            Text(m.note ?? "").font(.caption2).foregroundStyle(.tertiary).lineLimit(3)
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 12))
                    }
                }
            }
            Text("Meta 在 2025 年起陸續淘汰部分 insights 指標(例如 impressions 類);取不到的指標會顯示「已淘汰」或「無資料」,其他指標照常顯示。")
                .font(.caption).foregroundStyle(.secondary)
        }
        .task(id: "\(pageId)-\(period)-\(days)") {
            guard !pageId.isEmpty else { return }
            do { _ = try await store.pageInsights(pageId, period: period, days: days); error = nil } catch { self.error = error.localizedDescription }
        }
    }
}
