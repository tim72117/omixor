import Foundation

/// 派發:角色任務清單(拿全部,篩選在本機做)
@MainActor
final class DispatchStore: ObservableObject {
    enum Scope: String, CaseIterable, Identifiable {
        case open, all
        var id: String { rawValue }
        var label: String { self == .open ? "未完成" : "全部" }
    }

    @Published private(set) var tasks: [AgentTask] = []
    @Published var error: String?
    @Published private(set) var loaded = false
    @Published var busy: String?
    let session: Session
    let org: String

    init(session: Session, org: String) { self.session = session; self.org = org }

    /// 符合篩選的任務:卡住的最前面,再來未完成的,同一組裡最近更新的在前
    nonisolated static func rows(_ tasks: [AgentTask], scope: Scope, agent: String, now: Date = Date()) -> [AgentTask] {
        func rank(_ t: AgentTask) -> Int { t.stuckReason(now: now) != nil ? 0 : t.status.isOpen ? 1 : 2 }
        return tasks.filter { (scope == .all || $0.status.isOpen) && (agent.isEmpty || $0.agentId == agent) }
            .sorted {
                let (a, b) = (rank($0), rank($1))
                if a != b { return a < b }
                return ($0.updatedAt ?? .distantPast) > ($1.updatedAt ?? .distantPast)
            }
    }

    func stuckCount(now: Date = Date()) -> Int { tasks.filter { $0.stuckReason(now: now) != nil }.count }

    func task(_ id: String) -> AgentTask? { tasks.first { $0.id == id } }

    func refresh() async {
        guard let api = session.api else { return }
        do {
            tasks = try await api.tasks(org: org, status: "all")
            error = nil
        } catch is CancellationError {
        } catch {
            self.error = session.handle(error)
        }
        loaded = true
    }

    func requeue(_ id: String) async { await act(id) { try await $0.requeue(org: self.org, task: id) } }
    func cancel(_ id: String) async { await act(id) { try await $0.cancel(org: self.org, task: id) } }

    private func act(_ id: String, _ f: (APIClient) async throws -> AgentTask) async {
        guard let api = session.api else { return }
        busy = id
        defer { busy = nil }
        do {
            let t = try await f(api)
            if let k = tasks.firstIndex(where: { $0.id == t.id }) { tasks[k] = t }
            error = nil
        } catch {
            self.error = session.handle(error)
        }
    }
}
