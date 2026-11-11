import SwiftUI

struct ComposerView: View {
    @EnvironmentObject var model: AppModel
    @FocusState private var focused: Bool

    var body: some View {
        VStack(spacing: 8) {
            HStack(alignment: .bottom, spacing: 10) {
                Button { model.newChat() } label: {
                    Image(systemName: "plus").font(.body.weight(.medium))
                }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
                .help("在其他資料夾開新對話")
                .disabled(model.isRunning)

                TextField("Reply…", text: $model.draft, axis: .vertical)
                    .textFieldStyle(.plain)
                    .lineLimit(1...8)
                    .focused($focused)
                    .onSubmit(model.send)   // Enter 送出,Option+Enter 換行

                if model.isRunning {
                    Button(action: model.stop) {
                        Image(systemName: "stop.circle.fill").font(.title2)
                    }
                    .buttonStyle(.plain)
                    .help("停止")
                } else {
                    Button(action: model.send) {
                        Image(systemName: "arrow.up.circle.fill").font(.title2)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(canSend ? Color.accentColor : Color.secondary)
                    .disabled(!canSend)
                    .keyboardShortcut(.return, modifiers: .command)
                    .help("送出(Enter)")
                }
            }
            .padding(.horizontal, 14).padding(.vertical, 11)
            .background(.background, in: RoundedRectangle(cornerRadius: 16))
            .overlay(RoundedRectangle(cornerRadius: 16).strokeBorder(.separator))
            .shadow(color: .black.opacity(0.06), radius: 6, y: 2)

            HStack(spacing: 14) {
                Spacer()
                Picker("Model", selection: $model.model) {
                    ForEach(ModelChoice.allCases) { Text($0.label).tag($0) }
                }
                .help("模型")
                Picker("Mode", selection: $model.permission) {
                    ForEach(PermissionMode.allCases) { Text($0.label).tag($0) }
                }
                .help("Ask:需要許可的工具會被拒絕並回報;Accept edits:自動允許編輯檔案;Plan:只規劃不修改")
            }
            .pickerStyle(.menu)
            .labelsHidden()
            .fixedSize()
            .font(.callout)
            .disabled(model.isRunning)
        }
        .onAppear { focused = true }
    }

    private var canSend: Bool {
        !model.draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }
}
