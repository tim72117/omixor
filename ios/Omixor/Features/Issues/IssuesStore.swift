import Foundation

/// Issue 清單與詳情
@MainActor
final class IssuesStore: ObservableObject {
    enum Filter: String, CaseIterable, Identifiable {
        case open, closed, all
        var id: String { rawValue }
        var label: String {
            switch self {
            case .open: return "未關閉"
            case .closed: return "已關閉"
            case .all: return "全部"
            }
        }
    }

    @Published private(set) var issues: [Issue] = []
    @Published var error: String?
    @Published private(set) var loaded = false
    let session: Session
    let org: String

    init(session: Session, org: String) { self.session = session; self.org = org }

    /// 搜尋:編號、標題、內文、標籤、留言(不分大小寫,空白分開的每個字都要符合)
    nonisolated static func filter(_ l: [Issue], query: String, status: Filter) -> [Issue] {
        let words = query.lowercased().split(whereSeparator: \.isWhitespace).map(String.init)
        return l.filter { i in
            switch status {
            case .open: if !i.isOpen { return false }
            case .closed: if i.isOpen { return false }
            case .all: break
            }
            guard !words.isEmpty else { return true }
            let hay = ([i.id, i.title, i.body] + i.labels + i.comments.map(\.text)).joined(separator: "\n").lowercased()
            return words.allSatisfy { hay.contains($0) }
        }
    }

    func refresh() async {
        guard let api = session.api else { return }
        do {
            issues = try await api.issues(org: org)
            error = nil
        } catch is CancellationError {
        } catch {
            self.error = session.handle(error)
        }
        loaded = true
    }

    func issue(_ id: String) -> Issue? { issues.first { $0.id == id } }

    func reload(_ id: String) async {
        guard let api = session.api else { return }
        do {
            replace(try await api.issue(org: org, id: id))
        } catch is CancellationError {
        } catch {
            self.error = session.handle(error)
        }
    }

    /// 留言;成功回 true
    func comment(_ id: String, text: String) async -> Bool {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty, let api = session.api else { return false }
        do {
            replace(try await api.addComment(org: org, issue: id, text: t, author: session.user?.email ?? ""))
            error = nil
            return true
        } catch {
            self.error = session.handle(error)
            return false
        }
    }

    private func replace(_ i: Issue) {
        if let k = issues.firstIndex(where: { $0.id == i.id }) { issues[k] = i } else { issues.insert(i, at: 0) }
    }
}
