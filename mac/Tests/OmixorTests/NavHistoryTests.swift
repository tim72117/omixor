import XCTest
@testable import Omixor

final class NavHistoryTests: XCTestCase {
    private typealias P = NavHistory.Point

    func testRecordAndPop() {
        var h = NavHistory()
        XCTAssertFalse(h.canGoBack)
        h.record(from: P(section: .overview, focus: nil), to: P(section: .development, focus: "ISS-1"))
        h.record(from: P(section: .development, focus: "ISS-1"), to: P(section: .automation, focus: "t1"))
        XCTAssertTrue(h.canGoBack)
        XCTAssertEqual(h.pop(), P(section: .development, focus: "ISS-1"))
        XCTAssertEqual(h.pop(), P(section: .overview, focus: nil))
        XCTAssertNil(h.pop())
        XCTAssertFalse(h.canGoBack)
    }

    func testSamePositionNotRecorded() {
        var h = NavHistory()
        let p = P(section: .cabinet, focus: "doc")
        h.record(from: p, to: p)
        XCTAssertFalse(h.canGoBack)
    }

    func testLimitDropsOldest() {
        var h = NavHistory()
        for i in 0..<(NavHistory.limit + 5) {
            h.record(from: P(section: .overview, focus: "\(i)"), to: P(section: .overview, focus: "next"))
        }
        XCTAssertEqual(h.stack.count, NavHistory.limit)
        XCTAssertEqual(h.stack.first?.focus, "5")
    }
}
