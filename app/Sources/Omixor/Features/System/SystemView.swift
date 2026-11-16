import SwiftUI

/// 系統:這台伺服器開放哪些功能(系統層,整台伺服器共用,存在 core 的資料庫;清單也由 core 提供,見 System/SystemSettings.swift)。
/// 只有伺服器管理員能改(本機模式的本機使用者就是),其他人唯讀。沒開放的功能就像沒有這個功能:
/// 各組織的「設定 › 功能」不列、不能開。原本只有開發版的功能(實驗、範例資料、家庭助理、外部 MCP 閘道等)預設不開放;
/// 總覽、設定、系統永遠開放。組織要用哪些功能(組織層)在 設定 › 功能(Features/Settings/SettingsFeatures.swift)。
/// 頁面自己不開放時,底下的分頁收合不顯示(分頁的值保留,重新開放時照原本的)。對應 web 的 sections/system/SystemView.tsx。
struct SystemView: View {
    @EnvironmentObject var app: AppState
    @State private var saving: String?
    @State private var saveError: String?

    private var flags: FeatureFlags { app.features }

    var body: some View {
        PageScroll {
            PageHeader("系統", subtitle: "選擇這台伺服器開放哪些功能(所有組織共用)。沒開放的功能各組織都看不到、也不能開;各組織要用哪些,由組織管理員在 設定 › 功能 決定。") {
                HStack(spacing: 8) {
                    Button { app.go(.settings, focus: SettingsPane.features.rawValue) } label: { Label("設定 › 功能", systemImage: "switch.2") }
                    Button { Task { await app.startSystem().reload() } } label: { Label("重新讀取", systemImage: "arrow.clockwise") }
                }
            }
            status
            if let saveError {
                Card { Label(saveError, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red).textSelection(.enabled) }
            }
            FeatureCardList(items: flags.server.features.map(FeatureItem.init(server:)), canEdit: flags.serverCanEdit, saving: saving,
                            readOnlyHelp: "只有伺服器管理員可以修改", offWord: "不開放") { set($0, $1) }
        }
        .task { await app.startSystem().reload() }
    }

    /// 讀取狀態、權限
    @ViewBuilder private var status: some View {
        if let e = flags.serverError {
            Card {
                VStack(alignment: .leading, spacing: 4) {
                    Label("讀不到這台伺服器開放的功能", systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    Text(e).font(.callout).textSelection(.enabled)
                    Text(flags.serverSource == .cached ? "目前照這次執行最後一次讀到的設定顯示,暫時不能修改。"
                         : "目前顯示預設:原本正式版的功能開放、其他不開放,暫時不能修改。")
                        .font(.callout).foregroundStyle(.secondary)
                }
            }
        } else if !flags.serverCanEdit {
            Card {
                Label("只有伺服器管理員可以修改這台伺服器開放的功能;你可以看目前的設定。組織要用哪些功能請到 設定 › 功能。", systemImage: "lock")
                    .foregroundStyle(.secondary)
            }
        } else if let by = flags.server.updatedBy, let at = flags.server.updatedAt {
            Text("最後由 \(by) 於 \(at.formatted(date: .abbreviated, time: .shortened)) 修改")
                .font(.caption).foregroundStyle(.secondary)
        } else {
            Text("這台伺服器還沒改過,全部用預設(原本正式版的功能開放、實驗與還在實作中的功能不開放)。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private func set(_ it: FeatureItem, _ on: Bool) {
        guard let f = flags.server.features.first(where: { $0.key == it.key }), !f.required, flags.serverCanEdit else { return }
        saving = f.key
        defer { saving = nil }
        do {
            // 改回預設值時拿掉改過的值(之後 core 改預設會跟著)
            if on == f.systemDefault {
                try app.startSystem().setServer([:], reset: [f.key])
            } else {
                try app.startSystem().setServer([f.key: on])
            }
            saveError = nil
        } catch {
            saveError = "存不了:\(error.localizedDescription)"
        }
    }
}

/// 功能開關的卡片清單:系統頁(系統層)與 設定 › 功能(組織層)共用。
/// 每個頁面(大項目)一張卡片,分頁列在卡片裡;頁面關掉時分頁收合不顯示(分頁的值保留,打開頁面時再展開,規則見 FeatureItems)。
/// 實驗功能標「實驗」;必要功能標「永遠開著」、不能關。
struct FeatureCardList: View {
    let items: [FeatureItem]
    let canEdit: Bool
    let saving: String?
    /// 不能改時開關的說明
    let readOnlyHelp: String
    /// 頁面關掉時的說法(系統層是「不開放」)
    var offWord = "關閉"
    let onSet: (FeatureItem, Bool) -> Void

    var body: some View {
        ForEach(FeatureItems.pages(items)) { p in
            Card {
                VStack(alignment: .leading, spacing: 10) {
                    row(p, isPage: true)
                    let all = FeatureItems.tabs(items, of: p)
                    let kids = FeatureItems.shownTabs(items, of: p)
                    if FeatureItems.collapsed(p), !all.isEmpty {
                        Label("已\(offWord),底下 \(all.count) 個分頁也不會顯示;重新打開時分頁照原本的設定", systemImage: "chevron.right")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    if !kids.isEmpty {
                        Divider()
                        Text("分頁").font(.caption).foregroundStyle(.secondary)
                        ForEach(kids) { row($0, isPage: false).padding(.leading, 16) }
                    }
                }
            }
        }
    }

    private func row(_ f: FeatureItem, isPage: Bool) -> some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(f.name).font(isPage ? .headline : .body)
                    if f.experimental { Pill(text: "實驗", color: .purple, icon: "flask") }
                    if f.required { Pill(text: "永遠開著", color: .secondary, icon: "lock.fill") }
                    ForEach(f.tags, id: \.text) { t in Pill(text: t.text, color: t.color, icon: t.icon) }
                    if let w = f.warn { Pill(text: w, color: .orange) }
                    if f.changed { Text("已改過").font(.caption).foregroundStyle(.secondary) }
                }
                if !f.description.isEmpty {
                    Text(f.description).font(.callout).foregroundStyle(.secondary)
                }
                if f.experimental, let note = f.experimentalNote {
                    Text(note).font(.caption).foregroundStyle(.purple)
                }
            }
            Spacer(minLength: 12)
            if saving == f.key { ProgressView().controlSize(.small) }
            Toggle("", isOn: Binding(get: { f.on || f.required }, set: { onSet(f, $0) }))
                .toggleStyle(.switch)
                .labelsHidden()
                .disabled(f.required || !canEdit || saving != nil)
                .help(f.required ? "這個功能永遠開著" : (canEdit ? (f.on ? "關閉" : "開啟") : readOnlyHelp))
        }
    }
}

/// 跳到關掉的功能時,畫面上方顯示的提示(幾秒後自動消失);
/// 系統層沒開放的「前往系統」,組織層關掉的「前往 設定 › 功能」
struct FeatureNoticeBanner: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        if let text = app.featureNotice {
            HStack(spacing: 8) {
                Image(systemName: "switch.2")
                Text(text).font(.callout)
                if let k = app.featureNoticeKey, !app.features.serverHas(k) {
                    Button("前往系統") { app.featureNotice = nil; app.go(.system) }.buttonStyle(.link)
                } else {
                    Button("前往 設定 › 功能") { app.featureNotice = nil; app.go(.settings, focus: SettingsPane.features.rawValue) }.buttonStyle(.link)
                }
                Button { app.featureNotice = nil } label: { Image(systemName: "xmark") }.buttonStyle(.borderless)
            }
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(.regularMaterial, in: Capsule())
            .shadow(radius: 4)
            .padding(.top, 10)
            .task(id: text) {
                try? await Task.sleep(nanoseconds: 5_000_000_000)
                if app.featureNotice == text { app.featureNotice = nil }
            }
        }
    }
}

/// 動作失敗時,畫面上方顯示的提示(errorNotice;按掉或 10 秒後消失)
struct ErrorNoticeBanner: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        if let text = app.errorNotice {
            HStack(spacing: 8) {
                Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                Text(text).font(.callout).textSelection(.enabled).lineLimit(3)
                Button { app.errorNotice = nil } label: { Image(systemName: "xmark") }.buttonStyle(.borderless)
            }
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(.regularMaterial, in: Capsule())
            .shadow(radius: 4)
            .padding(.top, 10)
            .frame(maxWidth: 720)
            .task(id: text) {
                try? await Task.sleep(nanoseconds: 10_000_000_000)
                if app.errorNotice == text { app.errorNotice = nil }
            }
        }
    }
}
