import AppKit
import SwiftUI

/// 對話串的狀態:送出問題、模擬思考、逐字顯示回答。
@MainActor
final class AssistChat: ObservableObject {
    @Published private(set) var messages: [AssistMessage] = []
    @Published private(set) var busy = false

    private var task: Task<Void, Never>?

    func send(_ raw: String, app: AppState) {
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !busy else { return }
        messages.append(AssistMessage(role: .user, text: text))
        var pending = AssistMessage(role: .assistant, text: "")
        pending.thinking = true
        let id = pending.id
        messages.append(pending)
        busy = true

        task = Task { [weak self] in
            // 模擬思考時間,長一點的問題多想一下
            let think = UInt64(550 + min(text.count, 30) * 15) * 1_000_000
            try? await Task.sleep(nanoseconds: think)
            guard let self, !Task.isCancelled else { return }
            // 等思考完才讀資料,回答反映當下的狀態
            let reply = AssistResponder(app: app).reply(to: text)
            self.update(id) { $0.thinking = false; $0.streaming = true }

            let chars = Array(reply.text)
            var shown = 0
            while shown < chars.count {
                if Task.isCancelled { return }
                let step = chars.count > 240 ? 4 : 2
                shown = min(chars.count, shown + step)
                let partial = String(chars[0..<shown])
                self.update(id) { $0.text = partial }
                try? await Task.sleep(nanoseconds: 14_000_000)
            }
            self.update(id) { $0.streaming = false; $0.actions = reply.actions }
            self.busy = false
        }
    }

    /// 動作按完後的簡短回報(不走串流)
    func note(_ text: String) {
        messages.append(AssistMessage(role: .assistant, text: text))
    }

    func markUsed(_ action: AssistAction, in messageId: UUID) {
        update(messageId) { $0.usedActions.insert(action.id) }
    }

    func clear() {
        task?.cancel()
        task = nil
        busy = false
        messages.removeAll()
    }

    private func update(_ id: UUID, _ change: (inout AssistMessage) -> Void) {
        guard let i = messages.firstIndex(where: { $0.id == id }) else { return }
        change(&messages[i])
    }

    /// 執行回答裡的動作按鈕
    func perform(_ action: AssistAction, from messageId: UUID, app: AppState) {
        switch action {
        case .acknowledge(let id, let title):
            app.acknowledge(alertId: id)
            markUsed(action, in: messageId)
            note("已確認告警「\(title)」,並記錄到動態牆。")
        case .approve(let id):
            let title = app.approvals.first { $0.id == id }?.title ?? id
            app.decide(approvalId: id, approve: true)
            markUsed(action, in: messageId)
            markUsed(.reject(approvalId: id), in: messageId)
            note("已核准「\(title)」,AI 會開始執行並在完成後回報。")
        case .reject(let id):
            let title = app.approvals.first { $0.id == id }?.title ?? id
            app.decide(approvalId: id, approve: false)
            markUsed(action, in: messageId)
            markUsed(.approve(approvalId: id), in: messageId)
            note("已拒絕「\(title)」。")
        case .retry(let id):
            app.retry(runId: id)
            markUsed(action, in: messageId)
            note("已重新執行 #\(id),可以到 CI/CD 頁看進度。")
        case .go(let section, let focus, _):
            app.go(section, focus: focus)
        case .copy(let text):
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
            markUsed(action, in: messageId)
        }
    }

    /// 這個動作現在還能不能按(已處理過的告警/請求就停用)
    func isAvailable(_ action: AssistAction, in message: AssistMessage, app: AppState) -> Bool {
        if message.usedActions.contains(action.id) { return false }
        switch action {
        case .acknowledge(let id, _):
            return app.alerts.first { $0.id == id }?.state == .firing
        case .approve(let id), .reject(let id):
            return app.approvals.first { $0.id == id }?.state == .pending
        default:
            return true
        }
    }
}
