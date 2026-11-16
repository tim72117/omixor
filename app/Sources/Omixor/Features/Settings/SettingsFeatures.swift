import SwiftUI

/// 設定 › 功能:這個組織要開啟哪些功能(組織層,存在 core;清單也由 core 提供,見 System/SystemSettings.swift)。
/// 只列這台伺服器(系統層)有開放的功能;要開放別的功能請伺服器管理員到「系統」頁。只有組織管理員能改。
/// 實驗功能(只有模擬、範例資料)標「實驗」,預設關。總覽、設定、系統永遠開著。
/// 頁面自己關掉時,底下的分頁收合不顯示(分頁的值保留,重新打開照原本的)。對應 web 的 sections/settings/SettingsFeatures.tsx。
struct SettingsFeaturesPane: View {
    @EnvironmentObject var app: AppState
    @State private var saving: String?
    @State private var saveError: String?

    private var flags: FeatureFlags { app.features }

    var body: some View {
        PageScroll {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("功能").font(.title3.weight(.semibold))
                    Text("選擇這個組織要開啟哪些功能。關掉的功能不會出現在側邊欄、快速鍵、指令面板與 AI 助理的建議。")
                        .font(.callout).foregroundStyle(.secondary)
                }
                Spacer()
                Button { Task { await app.startSystem().reload() } } label: { Label("重新讀取", systemImage: "arrow.clockwise") }
            }
            status
            if flags.serverOffCount > 0 {
                HStack(spacing: 6) {
                    Image(systemName: "info.circle")
                    Text("這台伺服器還有 \(flags.serverOffCount) 項功能沒有開放,不會列在這裡;要開放請伺服器管理員到")
                    Button("系統") { app.go(.system) }.buttonStyle(.link)
                }
                .font(.caption).foregroundStyle(.secondary)
            }
            if let saveError {
                Card { Label(saveError, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red).textSelection(.enabled) }
            }
            FeatureCardList(items: flags.features.map(FeatureItem.init(org:)), canEdit: flags.canEdit, saving: saving,
                            readOnlyHelp: "只有組織管理員可以修改") { set($0, $1) }
        }
        .frame(maxWidth: 860)
        .frame(maxWidth: .infinity)
        .task { await app.startSystem().reload() }
    }

    /// 讀取狀態、權限
    @ViewBuilder private var status: some View {
        if let e = flags.error {
            Card {
                VStack(alignment: .leading, spacing: 4) {
                    Label("讀不到 omixor 的功能設定", systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
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
        } else {
            Text("這個組織還沒改過,全部用預設(已實作的開、實驗的關)。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private func set(_ it: FeatureItem, _ on: Bool) {
        guard let f = flags.features.first(where: { $0.key == it.key }), !f.required, flags.canEdit else { return }
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
