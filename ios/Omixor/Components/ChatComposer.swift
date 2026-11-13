import SwiftUI
import UIKit

/// 多行輸入框(UITextView):Return 送出;注音等輸入法還在選字(有 marked text)時 Return 交給輸入法確認,不會送出。
/// 高度隨內容長到 maxHeight,再多就捲動。
struct ChatComposer: UIViewRepresentable {
    @Binding var text: String
    var placeholder = "訊息"
    var maxHeight: CGFloat = 120
    var onSubmit: () -> Void

    func makeUIView(context: Context) -> UITextView {
        let v = UITextView()
        v.delegate = context.coordinator
        v.font = .preferredFont(forTextStyle: .body)
        v.adjustsFontForContentSizeCategory = true
        v.backgroundColor = .clear
        v.isScrollEnabled = false
        v.returnKeyType = .send
        v.textContainerInset = UIEdgeInsets(top: 8, left: 4, bottom: 8, right: 4)
        v.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        v.accessibilityLabel = placeholder
        return v
    }

    func updateUIView(_ v: UITextView, context: Context) {
        context.coordinator.parent = self
        // 選字中不要蓋掉(會打斷輸入法)
        if v.text != text, v.markedTextRange == nil { v.text = text }
    }

    func sizeThatFits(_ proposal: ProposedViewSize, uiView v: UITextView, context: Context) -> CGSize? {
        let w = proposal.width ?? 300
        let fit = v.sizeThatFits(CGSize(width: w, height: .greatestFiniteMagnitude)).height
        let h = min(max(fit, 36), maxHeight)
        let scroll = fit > maxHeight
        if v.isScrollEnabled != scroll { DispatchQueue.main.async { v.isScrollEnabled = scroll } }
        return CGSize(width: w, height: h)
    }

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    final class Coordinator: NSObject, UITextViewDelegate {
        var parent: ChatComposer
        init(_ p: ChatComposer) { parent = p }

        func textViewDidChange(_ v: UITextView) {
            parent.text = v.text
            v.invalidateIntrinsicContentSize()
        }

        func textView(_ v: UITextView, shouldChangeTextIn range: NSRange, replacementText t: String) -> Bool {
            ChatComposer.shouldSubmit(replacement: t, composing: v.markedTextRange != nil) ? { parent.onSubmit(); return false }() : true
        }
    }

    /// 這次輸入要不要送出:只有「沒有在選字」時按 Return 才送出
    static func shouldSubmit(replacement: String, composing: Bool) -> Bool {
        replacement == "\n" && !composing
    }
}

/// 在游標處(這裡簡化成文字結尾)插入 @提及
enum Mention {
    /// 插入「@名稱 」:前面不是空白就先補一個空白;結尾已經打了 @ 就接在後面
    static func insert(_ handle: String, into text: String) -> String {
        var t = text
        if t.hasSuffix("@") { t.removeLast() }
        if let last = t.last, !last.isWhitespace { t += " " }
        return t + "@" + handle + " "
    }

    /// 頻道成員可以 @ 的名稱:角色用名稱(伺服器認名稱與 id),人用 email
    @MainActor static func handle(for node: String, directory: Directory) -> String {
        if let id = node.stripping("agent:") { return directory.agent(id)?.name ?? id }
        return node.stripping("user:") ?? node
    }
}
