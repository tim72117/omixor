import SwiftUI

/// 系統:開關側邊欄的主要功能與頁內分頁(組織層級,存在 core;清單也由 core 提供,見 System/SystemSettings.swift)。
/// 實驗功能(只有模擬、範例資料)標「實驗」,預設關。總覽、設定、系統永遠開著。只有組織管理員能改。
/// 只列這個版本有的功能(server 與 App 的編譯版本都要有,見 AppEdition);開發版的功能另外標「開發版」。
/// 頁面自己關掉時,底下的分頁收合不顯示(分頁的值保留,重新打開照原本的)。
struct SystemView: View {
    @EnvironmentObject var app: AppState
    @State private var saving: String?
    @State private var saveError: String?

    private var flags: FeatureFlags { app.features }
    private var pages: [CoreFeature] { flags.features.filter { $0.parent == nil } }

    var body: some View {
        PageScroll {
            PageHeader("系統", subtitle: "選擇這個組織要開啟哪些功能。關掉的功能不會出現在側邊欄、快速鍵、指令面板與 AI 助理的建議。") {
                Button { Task { await app.startSystem().reload() } } label: { Label("重新讀取", systemImage: "arrow.clockwise") }
            }
            editionNote
            status
            if let saveError {
                Card { Label(saveError, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red).textSelection(.enabled) }
            }
            ForEach(pages) { p in
                Card {
                    VStack(alignment: .leading, spacing: 10) {
                        row(p, isPage: true)
                        let kids = flags.shownTabs(of: p)
                        if flags.collapsed(p), flags.tabCount(of: p) > 0 {
                            Text("已關閉,底下 \(flags.tabCount(of: p)) 個分頁也不會顯示;重新打開時分頁照原本的設定")
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
        .task { await app.startSystem().reload() }
    }

    /// 目前的版本(App 與 server 各自編譯時決定;開發版多了實驗功能)
    private var editionNote: some View {
        let appEd = AppEdition.current
        let serverEd = flags.settings.edition
        let anyDev = appEd == AppEdition.dev || serverEd == AppEdition.dev
        return VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 6) {
                if anyDev { Pill(text: "開發版", color: .pink, icon: "hammer") }
                Text("版本:App \(AppEdition.label(appEd))、omixor \(serverEd.map(AppEdition.label) ?? "(沒有回報版本)")")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let serverEd, serverEd != appEd {
                Text("App 與 omixor 的版本不同:開發版的功能要兩邊都有才會出現在這裡。")
                    .font(.caption).foregroundStyle(.orange)
            } else if anyDev {
                Text("開發版包含只有範例資料的實驗功能與還在框架階段的功能(標「開發版」);正式版不會出現。")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    /// 讀取狀態、權限
    @ViewBuilder private var status: some View {
        if let e = flags.error {
            Card {
                VStack(alignment: .leading, spacing: 4) {
                    Label("讀不到 omixor 的系統設定", systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    Text(e).font(.callout).textSelection(.enabled)
                    Text(flags.source == .cached ? "目前照這次執行最後一次讀到的設定顯示,暫時不能修改。"
                         : "目前用預設:已實作的功能開、實驗功能關,暫時不能修改。")
                        .font(.callout).foregroundStyle(.secondary)
                }
            }
        } else if !flags.canEdit {
            Card {
                Label("只有組織管理員可以修改;你可以看目前的設定。", systemImage: "lock")
                    .foregroundStyle(.secondary)
            }
        } else if let by = flags.settings.updatedBy, let at = flags.settings.updatedAt {
            Text("最後由 \(by) 於 \(at.formatted(date: .abbreviated, time: .shortened)) 修改")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private func row(_ f: CoreFeature, isPage: Bool) -> some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(f.name).font(isPage ? .headline : .body)
                    if f.edition == AppEdition.dev { Pill(text: "開發版", color: .pink, icon: "hammer") }
                    if f.experimental { Pill(text: "實驗", color: .purple, icon: "flask") }
                    if f.required { Pill(text: "永遠開著", color: .secondary, icon: "lock.fill") }
                    if f.on && !f.enabled && !f.required {
                        Pill(text: isPage ? "分頁都關了,不顯示" : "所屬頁面關了", color: .orange)
                    }
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
            Toggle("", isOn: Binding(get: { f.on || f.required }, set: { set(f, $0) }))
                .toggleStyle(.switch)
                .labelsHidden()
                .disabled(f.required || !flags.canEdit || saving != nil)
                .help(f.required ? "這個功能永遠開著" : (flags.canEdit ? (f.on ? "關閉" : "開啟") : "只有組織管理員可以修改"))
        }
    }

    private func set(_ f: CoreFeature, _ on: Bool) {
        guard !f.required, flags.canEdit else { return }
        saving = f.key
        defer { saving = nil }
        do {
            // 改回預設值時拿掉改過的值(之後 core 改預設會跟著)
            if on == f.defaultOn {
                try app.startSystem().set([:], reset: [f.key])
            } else {
                try app.startSystem().set([f.key: on])
            }
            saveError = nil
        } catch {
            saveError = "存不了:\(error.localizedDescription)"
        }
    }
}

/// 跳到關掉的功能時,畫面上方顯示的提示(幾秒後自動消失)
struct FeatureNoticeBanner: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        if let text = app.featureNotice {
            HStack(spacing: 8) {
                Image(systemName: "switch.2")
                Text(text).font(.callout)
                Button("前往系統") { app.featureNotice = nil; app.go(.system) }.buttonStyle(.link)
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
