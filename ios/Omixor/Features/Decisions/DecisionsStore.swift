import Foundation

/// 待決定 / 需要協助:清單、各 Issue 的筆數、回答。清單拿全部(撤回的不顯示),篩選在本機做;回答後以伺服器回的那筆取代。
@MainActor
final class DecisionsStore: ObservableObject {
    enum Filter: String, CaseIterable, Identifiable {
        case open, decided
        var id: String { rawValue }
        var label: String { self == .open ? "待處理" : "已處理" }
    }

    @Published private(set) var decisions: [Decision] = []
    /// 來源 Issue key → 筆數
    @Published private(set) var summary: [String: DecisionCount] = [:]
    @Published var error: String?
    /// 回答表單裡顯示的錯誤(和清單的 error 分開,表單不會被關掉)
    @Published var answerError: String?
    @Published private(set) var loaded = false
    /// 正在送出回答的決定 id
    @Published private(set) var submitting: String?
    let org: String
    private let apiProvider: @MainActor () -> APIClient?
    private let report: @MainActor (Error) -> String

    /// api、report 由 Session 提供(report 處理 401 登出並回訊息);測試直接給假的
    init(org: String, api: @escaping @MainActor () -> APIClient?, report: @escaping @MainActor (Error) -> String) {
        self.org = org; self.apiProvider = api; self.report = report
    }

    /// 待處理的筆數(分頁角標;沒有來源 Issue 的也算,所以不用 summary)
    var openCount: Int { decisions.filter(\.isOpen).count }

    /// 某張 Issue 的筆數(沒有 = 0)
    func count(issue key: String) -> DecisionCount { summary[key] ?? DecisionCount() }

    /// 符合篩選的決定:待處理照提出時間新的在前;已處理照處理時間新的在前;撤回的不顯示。issue 非空 = 只看那張 Issue 的
    nonisolated static func rows(_ list: [Decision], filter: Filter, issue: String = "") -> [Decision] {
        func num(_ d: Decision) -> Int { Int(d.id.drop { !$0.isNumber }) ?? 0 }
        let want = filter == .open ? "open" : "decided"
        let hits = list.filter { $0.status == want && (issue.isEmpty || $0.sourceIssue == issue) }
        return hits.sorted {
            let (a, b) = filter == .open ? ($0.createdAt, $1.createdAt) : ($0.decidedAt, $1.decidedAt)
            if a != b { return (a ?? .distantPast) > (b ?? .distantPast) }
            return num($0) > num($1)
        }
    }

    func decision(_ id: String) -> Decision? { decisions.first { $0.id == id } }

    /// 重新讀清單與筆數(筆數讀不到不影響清單)
    func refresh() async {
        guard let api = apiProvider() else { return }
        do {
            decisions = try await api.decisions(org: org)
            error = nil
        } catch is CancellationError {
            return
        } catch {
            self.error = report(error)
        }
        if let s = try? await api.decisionSummary(org: org) { summary = s }
        loaded = true
    }

    /// 回答;成功回 true(已把伺服器回的那筆放進清單並更新筆數)。失敗的原因在 answerError;409(別人先處理了)、404 會重新讀取
    func answer(_ d: Decision, _ draft: DecisionDraft) async -> Bool {
        let problem = draft.problem(for: d)
        guard problem.isEmpty else { answerError = problem; return false }
        guard let api = apiProvider() else { return false }
        let (note, evidence) = draft.payload(for: d)
        submitting = d.id
        defer { submitting = nil }
        do {
            let saved = try await api.answerDecision(org: org, id: d.id, answer: draft.choice, note: note, evidence: evidence)
            replace(saved)
            answerError = nil
            if let s = try? await api.decisionSummary(org: org) { summary = s }
            return true
        } catch is CancellationError {
            return false
        } catch {
            answerError = DecisionErrorText.message(error)
            _ = report(error)  // 401 要登出
            if let status = (error as? APIError)?.status, status == 409 || status == 404 { await refresh() }
            return false
        }
    }

    private func replace(_ d: Decision) {
        if let k = decisions.firstIndex(where: { $0.id == d.id }) { decisions[k] = d } else { decisions.insert(d, at: 0) }
    }
}
