import Foundation

/// 導覽歷史(對應網頁的「← 返回」):記下離開的頁面與選中的項目,返回時退一步。
/// 只存 (頁面, focus);頁面內部的分頁、篩選不記。純資料,方便測試。
struct NavHistory: Equatable {
    struct Point: Equatable {
        var section: AppSection
        var focus: String?
    }

    static let limit = 50
    private(set) var stack: [Point] = []

    var canGoBack: Bool { !stack.isEmpty }

    /// 離開 from、前往 to:位置沒變就不記;超過上限丟掉最舊的
    mutating func record(from: Point, to: Point) {
        guard from != to else { return }
        stack.append(from)
        if stack.count > Self.limit { stack.removeFirst(stack.count - Self.limit) }
    }

    /// 退一步,回傳要回到的位置(沒有歷史是 nil)
    mutating func pop() -> Point? { stack.popLast() }

    /// 組織切換或登出後舊的位置沒有意義
    mutating func clear() { stack.removeAll() }
}
