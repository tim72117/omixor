import SwiftUI

/// App 在前景時每隔一段時間執行一次(畫面消失或進背景就停)
struct ForegroundPolling: ViewModifier {
    @Environment(\.scenePhase) private var phase
    let interval: Duration
    let action: @MainActor () async -> Void

    func body(content: Content) -> some View {
        content.task(id: phase) {
            guard phase == .active else { return }
            while !Task.isCancelled {
                try? await Task.sleep(for: interval)
                if Task.isCancelled { break }
                await action()
            }
        }
    }
}

extension View {
    /// 前景時定時更新(第一次載入另外在 .task 做)
    func foregroundPolling(every seconds: Double, _ action: @escaping @MainActor () async -> Void) -> some View {
        modifier(ForegroundPolling(interval: .seconds(seconds), action: action))
    }
}

/// 列表上方的錯誤列
struct ErrorBanner: View {
    let message: String
    var body: some View {
        Label(message, systemImage: "exclamationmark.triangle.fill")
            .font(.footnote)
            .foregroundStyle(.white)
            .padding(.horizontal, 12).padding(.vertical, 8)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color.orange.gradient)
    }
}

/// 小標籤
struct Pill: View {
    let text: String
    var color: Color = .secondary
    var icon: String?
    var body: some View {
        HStack(spacing: 3) {
            if let icon { Image(systemName: icon) }
            Text(text)
        }
        .font(.caption2.weight(.semibold))
        .padding(.horizontal, 6).padding(.vertical, 2)
        .foregroundStyle(color)
        .background(color.opacity(0.14), in: Capsule())
    }
}

/// 組織裡的人與角色:把節點(user:<email>、agent:<id>)或 email 換成顯示名稱
@MainActor
final class Directory: ObservableObject {
    @Published private(set) var members: [Member] = []
    @Published private(set) var agents: [Agent] = []

    func load(api: APIClient, org: String) async {
        async let m = try? api.members(org: org)
        async let a = try? api.agents(org: org)
        let (mm, aa) = await (m, a)
        if let mm { members = mm }
        if let aa { agents = aa }
    }

    func agent(_ id: String) -> Agent? { agents.first { $0.id == id } }

    /// 節點或 email → 名稱
    func name(_ node: String) -> String {
        if let id = node.stripping("agent:") { return agent(id)?.name ?? id }
        let email = node.stripping("user:") ?? node
        if let m = members.first(where: { $0.email.caseInsensitiveCompare(email) == .orderedSame }), !m.name.isEmpty { return m.name }
        return email
    }

    func isAgent(_ node: String) -> Bool { node.hasPrefix("agent:") }
}

/// 開發用:`-OmixorOpen <id>`(CH-1、ISS-1、T-2…)載入後直接打開那一筆(截圖、自動化測試用)
enum DebugLaunch {
    static var openId: String? {
        #if DEBUG
        return UserDefaults.standard.string(forKey: "OmixorOpen")
        #else
        return nil
        #endif
    }
}

extension String {
    func stripping(_ prefix: String) -> String? { hasPrefix(prefix) ? String(dropFirst(prefix.count)) : nil }
}

extension Date {
    /// 今天只顯示時間,其他顯示月日時間
    var shortLabel: String {
        Calendar.current.isDateInToday(self)
            ? formatted(date: .omitted, time: .shortened)
            : formatted(.dateTime.month().day().hour().minute())
    }
}
