import SwiftUI

/// 多行輸入框的外觀與列數(對應網頁的 AutoTextarea):圓角邊框、聚焦時強調色、沒有拖曳把手。
/// 短的回覆與留言用 short(1–8 列),長篇內文用 long(6–30 列);高度在範圍內隨內容長高,超過才捲動。
enum InputRows {
    static let short = 1...8
    static let long = 6...30
}

private struct InputBoxStyle: ViewModifier {
    var active: Bool
    func body(content: Content) -> some View {
        content
            .padding(.horizontal, 10).padding(.vertical, 7)
            .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 8))
            .overlay(RoundedRectangle(cornerRadius: 8)
                .strokeBorder(active ? Color.accentColor.opacity(0.6) : Color.primary.opacity(0.12)))
    }
}

extension View {
    /// 套上統一的輸入框外觀;active = 聚焦中或正在接受拖放(邊框改強調色)
    func inputBox(active: Bool = false) -> some View { modifier(InputBoxStyle(active: active)) }
}

/// 隨內容長高的多行輸入框(macOS 的 TextField 加 axis: .vertical 與 lineLimit 原生就會長高)
struct AutoGrowTextField: View {
    let placeholder: String
    @Binding var text: String
    var rows: ClosedRange<Int> = InputRows.short
    @FocusState private var focused: Bool

    var body: some View {
        TextField(placeholder, text: $text, axis: .vertical)
            .textFieldStyle(.plain)
            .lineLimit(rows)
            .focused($focused)
            .inputBox(active: focused)
    }
}
