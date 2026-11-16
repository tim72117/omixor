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

    /// 全部 Issue(詳情、留言用)
    @Published private(set) var issues: [Issue] = []
    /// 清單畫面顯示的:core 依目前的搜尋與狀態篩選、排序好的(規則只在 core 的 model/issue_query.go)
    @Published private(set) var rows: [Issue] = []
    @Published private(set) var rowsLoaded = false
    @Published var error: String?
    @Published private(set) var loaded = false
    let session: Session
    let org: String
    /// 目前清單的搜尋與狀態
    private(set) var query = ""
    private(set) var status: Filter = .open
    private var rowsSeq = 0

    init(session: Session, org: String) { self.session = session; self.org = org }

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
        await loadRows()
    }

    /// 換搜尋或狀態:向 core 重新查清單
    func search(query: String, status: Filter) async {
        self.query = query
        self.status = status
        await loadRows()
    }

    /// 向 core 查清單(未解決的在前 → 優先度 → 最近更新 → 編號);晚回來的舊查詢丟掉
    func loadRows() async {
        guard let api = session.api else { return }
        rowsSeq += 1
        let seq = rowsSeq
        do {
            let l = try await api.issues(org: org, query: query, status: status.rawValue, sort: "status")
            guard seq == rowsSeq else { return }
            rows = l
            error = nil
        } catch is CancellationError {
            return
        } catch {
            guard seq == rowsSeq else { return }
            self.error = session.handle(error)
        }
        rowsLoaded = true
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
        if let k = rows.firstIndex(where: { $0.id == i.id }) { rows[k] = i }
    }
}
