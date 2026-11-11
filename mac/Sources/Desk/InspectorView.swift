import AppKit
import SwiftUI

/// 右側面板:環境、context 使用率、未 commit 的變更。
struct InspectorView: View {
    @EnvironmentObject var model: AppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                section("Environment") {
                    row("laptopcomputer", "This computer")
                    if let folder = model.currentFolder {
                        Button {
                            NSWorkspace.shared.open(URL(fileURLWithPath: folder))
                        } label: {
                            row("folder", (folder as NSString).lastPathComponent,
                                detail: (folder as NSString).abbreviatingWithTildeInPath)
                        }
                        .buttonStyle(.plain)
                        .help("在 Finder 開啟")
                    }
                    if let branch = model.git.branch {
                        row("arrow.triangle.branch", branch)
                    }
                }

                Divider()

                section("Context") {
                    HStack {
                        Text(contextLabel).monospacedDigit()
                        Spacer()
                        ProgressView(value: contextFraction).frame(width: 60)
                    }
                    .foregroundStyle(.secondary)
                    .help("最後一次回覆用掉的輸入 token;超過 200k 時以 1M 計")
                }

                Divider()

                section("Changes") {
                    if model.git.isRepo {
                        HStack(spacing: 6) {
                            Image(systemName: "plus.forwardslash.minus").foregroundStyle(.secondary)
                            Text("+\(model.git.added)").foregroundStyle(.green)
                            Text("−\(model.git.removed)").foregroundStyle(.red)
                            Spacer()
                            Button("Refresh", action: model.refreshGit).controlSize(.small)
                        }
                        .monospacedDigit()
                    } else {
                        Text("不是 git 儲存庫").foregroundStyle(.secondary)
                    }
                }
            }
            .padding(16)
        }
    }

    private var contextFraction: Double {
        guard let t = model.contextTokens else { return 0 }
        return min(1, Double(t) / Double(AppModel.contextWindow(for: t)))
    }

    private var contextLabel: String {
        guard model.contextTokens != nil else { return "—" }
        return "\(Int((contextFraction * 100).rounded()))%"
    }

    private func section<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(title).font(.subheadline.weight(.semibold))
            content()
        }
    }

    private func row(_ icon: String, _ title: String, detail: String? = nil) -> some View {
        HStack(spacing: 10) {
            Image(systemName: icon).frame(width: 18).foregroundStyle(.secondary)
            Text(title).lineLimit(1)
            if let detail {
                Text(detail).foregroundStyle(.secondary).lineLimit(1).truncationMode(.head)
            }
            Spacer(minLength: 0)
        }
        .contentShape(Rectangle())
    }
}
