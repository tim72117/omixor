import Foundation

extension DecisionsStore {
    /// 用目前登入的 Session 建立(API 用戶端與 401 處理都來自 Session)
    convenience init(session: Session, org: String) {
        self.init(org: org, api: { session.api }, report: { session.handle($0) })
    }
}
