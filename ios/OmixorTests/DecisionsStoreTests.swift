import XCTest
@testable import Omixor

/// 待決定的邏輯:清單與篩選、回答(成功、409、錯誤)、角標筆數、回答規則與文字
@MainActor
final class DecisionsStoreTests: XCTestCase {
    private func store(unauthorized: ((Error) -> Void)? = nil) -> DecisionsStore {
        let api = APIClient(baseURL: URL(string: "https://omixor.example.com")!, token: "omx_t", session: StubProtocol.session())
        return DecisionsStore(org: "acme", api: { api }, report: { unauthorized?($0); return $0.localizedDescription })
    }

    private func dec(_ id: String, kind: String = "decision", status: String = "open", issue: String = "", created: String = "2026-05-01T00:00:00Z",
                     decided: String? = nil, answer: String = "") -> String {
        let opts = kind == "help" ? "[]" : #"[{"id":"o1","label":"A","recommended":true},{"id":"o2","label":"B"}]"#
        let dd = decided.map { #","decidedAt":"\#($0)""# } ?? ""
        return #"{"id":"\#(id)","kind":"\#(kind)","title":"T \#(id)","status":"\#(status)","options":\#(opts),"sourceIssue":"\#(issue)","createdAt":"\#(created)","answer":"\#(answer)"\#(dd)}"#
    }

    private func stubList(_ items: [String], summary: String = #"{"ISS-1":{"open":1,"decided":1}}"#) {
        StubProtocol.reset { req, _ in
            if req.url?.path.hasSuffix("/decisions/summary") == true { return .init(status: 200, body: summary) }
            return .init(status: 200, body: "[" + items.joined(separator: ",") + "]")
        }
    }

    func testRefreshListFilterAndBadge() async {
        stubList([dec("DEC-1", issue: "ISS-1", created: "2026-05-01T00:00:00Z"),
                  dec("DEC-2", kind: "help", created: "2026-05-02T00:00:00Z"),
                  dec("DEC-3", status: "decided", issue: "ISS-1", decided: "2026-05-03T00:00:00Z", answer: "o1"),
                  dec("DEC-4", status: "withdrawn")])
        let s = store()
        await s.refresh()
        XCTAssertTrue(s.loaded)
        XCTAssertEqual(s.openCount, 2, "角標 = 待處理(含沒有來源 Issue 的)")
        XCTAssertEqual(DecisionsStore.rows(s.decisions, filter: .open).map(\.id), ["DEC-2", "DEC-1"], "待處理:新提出的在前")
        XCTAssertEqual(DecisionsStore.rows(s.decisions, filter: .decided).map(\.id), ["DEC-3"], "撤回的不顯示")
        XCTAssertEqual(DecisionsStore.rows(s.decisions, filter: .open, issue: "ISS-1").map(\.id), ["DEC-1"])
        XCTAssertEqual(s.count(issue: "ISS-1"), DecisionCount(open: 1, decided: 1))
        XCTAssertEqual(s.count(issue: "ISS-9"), DecisionCount())
    }

    func testRefreshErrorKeepsListAndShowsMessage() async {
        stubList([dec("DEC-1")])
        let s = store()
        await s.refresh()
        StubProtocol.reset { _, _ in .init(status: 500, body: #"{"error":"壞了"}"#) }
        await s.refresh()
        XCTAssertEqual(s.error, "壞了")
        XCTAssertEqual(s.decisions.count, 1, "讀失敗不清掉舊清單")
    }

    func testAnswerSuccessReplacesAndRefreshesSummary() async {
        let s = store()
        stubList([dec("DEC-1", issue: "ISS-1")])
        await s.refresh()
        StubProtocol.reset { req, body in
            if req.url?.path.hasSuffix("/decisions/summary") == true { return .init(status: 200, body: #"{"ISS-1":{"open":0,"decided":1}}"#) }
            XCTAssertEqual(req.httpMethod, "POST")
            XCTAssertEqual(req.url?.path, "/api/orgs/acme/decisions/DEC-1/answer")
            XCTAssertEqual(json(body)["answer"] as? String, "other")
            XCTAssertEqual(json(body)["note"] as? String, "改天再說", "note 去頭尾空白")
            XCTAssertNil(json(body)["evidence"], "decision 不帶 evidence")
            return .init(status: 200, body: #"{"result":"# + self.dec("DEC-1", status: "decided", issue: "ISS-1", decided: "2026-05-04T00:00:00Z", answer: "other") + "}")
        }
        let ok = await s.answer(s.decisions[0], DecisionDraft(choice: "other", note: "  改天再說 ", evidence: "ignored"))
        XCTAssertTrue(ok)
        XCTAssertEqual(s.decision("DEC-1")?.status, "decided")
        XCTAssertEqual(s.openCount, 0)
        XCTAssertEqual(s.count(issue: "ISS-1"), DecisionCount(open: 0, decided: 1))
        XCTAssertNil(s.answerError)
        XCTAssertNil(s.submitting)
    }

    func testHelpAnswerSendsEvidenceOnlyWhenDone() async {
        stubList([dec("DEC-2", kind: "help")])
        let s = store()
        await s.refresh()
        let help = s.decisions[0]
        XCTAssertEqual(DecisionDraft(choice: "done", evidence: " v1 ").payload(for: help).evidence, "v1")
        XCTAssertEqual(DecisionDraft(choice: "declined", note: "沒空", evidence: "x").payload(for: help).evidence, "")
    }

    func testAnswerConflictRefreshesAndExplains() async {
        let s = store()
        stubList([dec("DEC-1")])
        await s.refresh()
        StubProtocol.reset { req, _ in
            if req.url?.path.hasSuffix("/answer") == true { return .init(status: 409, body: #"{"error":"DEC-1 剛剛被別人處理了"}"#) }
            if req.url?.path.hasSuffix("/summary") == true { return .init(status: 200, body: "{}") }
            return .init(status: 200, body: "[" + self.dec("DEC-1", status: "decided", answer: "o2") + "]")
        }
        let ok = await s.answer(s.decisions[0], DecisionDraft(choice: "o1"))
        XCTAssertFalse(ok)
        XCTAssertTrue(s.answerError?.contains("別人可能剛處理過") == true)
        XCTAssertTrue(s.answerError?.contains("剛剛被別人處理了") == true, "附上伺服器的說明")
        XCTAssertEqual(s.decision("DEC-1")?.status, "decided", "已重新讀取")
        XCTAssertNil(s.submitting)
    }

    func testAnswerForbiddenAndNotFound() async {
        stubList([dec("DEC-1")])
        let s = store()
        await s.refresh()
        let d = s.decisions[0]
        StubProtocol.reset { _, _ in .init(status: 403, body: #"{"error":"只有人可以處理"}"#) }
        let r1 = await s.answer(d, DecisionDraft(choice: "o1"))
        XCTAssertFalse(r1)
        XCTAssertTrue(s.answerError?.hasPrefix("沒有權限做這個動作") == true)
        StubProtocol.reset { req, _ in
            req.url?.path.hasSuffix("/answer") == true ? .init(status: 404, body: #"{"error":"找不到 DEC-1"}"#) : .init(status: 200, body: "[]")
        }
        let r2 = await s.answer(d, DecisionDraft(choice: "o1"))
        XCTAssertFalse(r2)
        XCTAssertTrue(s.answerError?.hasPrefix("找不到這筆待決定") == true)
        XCTAssertTrue(s.decisions.isEmpty, "404 之後重新讀取,消失的不再顯示")
    }

    func testUnauthorizedIsReported() async {
        stubList([dec("DEC-1")])
        var seen: [Int] = []
        let s = store(unauthorized: { seen.append(($0 as? APIError)?.status ?? 0) })
        await s.refresh()
        StubProtocol.reset { _, _ in .init(status: 401, body: #"{"error":"登入失效"}"#) }
        _ = await s.answer(s.decisions[0], DecisionDraft(choice: "o1"))
        XCTAssertTrue(seen.contains(401), "401 交給 session 處理(登出)")
    }

    func testInvalidDraftDoesNotCallServer() async {
        stubList([dec("DEC-1")])
        let s = store()
        await s.refresh()
        StubProtocol.reset { _, _ in .init(status: 500, body: "") }
        let r3 = await s.answer(s.decisions[0], DecisionDraft(choice: "other", note: "  "))
        XCTAssertFalse(r3)
        XCTAssertEqual(s.answerError, "選「其他」要寫說明")
        XCTAssertTrue(StubProtocol.requests.isEmpty)
    }

    func testDraftRulesAndLabels() throws {
        let decision = try APIClient.decoder.decode(Decision.self, from: Data(dec("DEC-1").utf8))
        let help = try APIClient.decoder.decode(Decision.self, from: Data(dec("DEC-2", kind: "help").utf8))
        XCTAssertEqual(DecisionDraft().problem(for: decision), "請選一個選項,或選「其他」")
        XCTAssertEqual(DecisionDraft().problem(for: help), "請選擇完成或婉拒")
        XCTAssertEqual(DecisionDraft(choice: "zzz").problem(for: decision), "沒有這個選項")
        XCTAssertEqual(DecisionDraft(choice: "other").problem(for: decision), "選「其他」要寫說明")
        XCTAssertEqual(DecisionDraft(choice: "declined").problem(for: help), "婉拒要寫原因")
        XCTAssertEqual(DecisionDraft(choice: "done").problem(for: decision), "沒有這個選項", "decision 不能回 done")
        XCTAssertEqual(DecisionDraft(choice: "o2").problem(for: decision), "")
        XCTAssertEqual(DecisionDraft(choice: "done").problem(for: help), "")
        XCTAssertEqual(DecisionDraft.choices(for: decision).map(\.label), ["A(建議)", "B", "其他(要寫說明)"])
        XCTAssertEqual(DecisionDraft.choices(for: help).map(\.id), ["done", "declined"])
        XCTAssertEqual(decision.optionSummary, "A(建議)、B")
        XCTAssertEqual([decision.kindLabel, help.kindLabel], ["待決定", "需要協助"])
        XCTAssertEqual(decision.statusLabel, "待處理")
    }

    func testAnswerLabel() throws {
        let d = try APIClient.decoder.decode(Decision.self, from: Data(dec("DEC-1", status: "decided", answer: "o2").utf8))
        XCTAssertEqual(d.answerLabel, "B")
        XCTAssertEqual(d.statusLabel, "已決定")
        let h = try APIClient.decoder.decode(Decision.self, from: Data(dec("DEC-2", kind: "help", status: "decided", answer: "declined").utf8))
        XCTAssertEqual(h.answerLabel, "婉拒")
        XCTAssertEqual(h.statusLabel, "已處理")
    }
}
