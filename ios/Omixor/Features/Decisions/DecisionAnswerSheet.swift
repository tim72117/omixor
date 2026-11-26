import SwiftUI

/// 回答表單:decision 選一個選項或「其他」(要寫說明);help 選完成(可附證據)或婉拒(要寫原因)。驗證與送出邏輯在 DecisionsStore / DecisionDraft
struct DecisionAnswerSheet: View {
    @ObservedObject var store: DecisionsStore
    let decision: Decision
    @Environment(\.dismiss) private var dismiss
    @State private var draft = DecisionDraft()

    private var busy: Bool { store.submitting == decision.id }
    /// 別人先處理了(409)後清單會重新讀取:表單跟著最新的狀態,不能再送出,錯誤訊息留著讓人看到
    private var stillOpen: Bool { store.decision(decision.id)?.isOpen ?? false }
    private var problem: String { stillOpen ? draft.problem(for: decision) : "已經處理過了" }

    var body: some View {
        NavigationStack {
            Form {
                Section(decision.isHelp ? "處理結果" : "選擇答案") {
                    ForEach(DecisionDraft.choices(for: decision), id: \.id) { c in choiceRow(c.id, c.label) }
                }
                Section {
                    TextField(draft.needsNote ? "說明(必填)" : "補充說明(選填)", text: $draft.note, axis: .vertical)
                        .lineLimit(2...8)
                    if decision.isHelp && draft.choice == Decision.answerDone {
                        TextField("證據(連結、輸出、版本號;選填)", text: $draft.evidence)
                            .textInputAutocapitalization(.never).autocorrectionDisabled()
                    }
                } footer: {
                    Text(decision.sourceIssue.isEmpty ? "" : "送出後結論會留言到來源 Issue \(decision.sourceIssue)")
                }
                if let e = store.answerError { Section { Label(e, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange).font(.callout) } }
            }
            .navigationTitle(decision.id)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() }.disabled(busy) }
                ToolbarItem(placement: .confirmationAction) {
                    if busy { ProgressView() } else { Button("送出") { Task { await submit() } }.disabled(!problem.isEmpty) }
                }
            }
            .interactiveDismissDisabled(busy)
            .onAppear { store.answerError = nil }
        }
    }

    private func choiceRow(_ id: String, _ label: String) -> some View {
        Button { draft.choice = id } label: {
            HStack {
                Text(label).foregroundStyle(.primary)
                Spacer()
                if draft.choice == id { Image(systemName: "checkmark").foregroundStyle(.tint) }
            }
        }
    }

    private func submit() async {
        if await store.answer(decision, draft) { dismiss() }
    }
}
