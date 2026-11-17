import SwiftUI
import AppKit
import UniformTypeIdentifiers

/// 支援圖片的 Markdown 輸入框(同 Go 版的 MdTextarea):
/// 貼上(⌘V)、拖曳圖片,或按「插入圖片」選檔,存到目前組織的附件後插入 ![檔名](/api/attachments/…)。
/// SwiftUI 的 TextEditor 拿不到游標位置,圖片一律接在內文最後面。
struct MarkdownEditor: View {
    @EnvironmentObject var app: AppState
    @Binding var text: String
    var placeholder = ""
    var minHeight: CGFloat = 180
    var monospaced = true

    @FocusState private var focused: Bool
    @State private var over = false
    @State private var error: String?
    @State private var monitor: Any?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            TextEditor(text: $text)
                .font(monospaced ? .system(size: 13, design: .monospaced) : .body)
                .focused($focused)
                .frame(minHeight: minHeight)
                .overlay(alignment: .topLeading) {
                    if text.isEmpty && !placeholder.isEmpty {
                        Text(placeholder).foregroundStyle(.tertiary).padding(.leading, 5).allowsHitTesting(false)
                    }
                }
                .overlay(RoundedRectangle(cornerRadius: 6)
                    .stroke(over ? Color.accentColor : Color.secondary.opacity(0.25), style: StrokeStyle(lineWidth: over ? 2 : 1, dash: over ? [5] : [])))
                .onDrop(of: [.fileURL, .image], isTargeted: $over) { providers in
                    dropped(providers)
                }
            HStack(spacing: 8) {
                Button { pickFiles() } label: { Label("插入圖片", systemImage: "photo") }
                    .buttonStyle(.link)
                Text("也可以直接貼上或拖曳圖片").foregroundStyle(.secondary)
                if let error { Text(error).foregroundStyle(.orange) }
            }
            .font(.caption)
        }
        .onAppear(perform: installPasteMonitor)
        .onDisappear {
            if let monitor { NSEvent.removeMonitor(monitor) }
            monitor = nil
        }
    }

    // MARK: - 加入圖片

    private func add(_ data: Data, name: String) {
        guard let img = Attachments.normalized(data) else {
            error = "只能放 PNG、JPEG、GIF、WebP 圖片"
            return
        }
        do {
            let id = try Attachments.save(img, in: app.dataDir)
            let md = Attachments.markdown(name: name, id: id)
            text += (text.isEmpty || text.hasSuffix("\n") ? "" : "\n") + md + "\n"
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func add(fileURL url: URL) {
        guard let data = try? Data(contentsOf: url) else { error = "讀不到檔案"; return }
        add(data, name: url.lastPathComponent)
    }

    private func pickFiles() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = false
        panel.allowedContentTypes = [.png, .jpeg, .gif, .webP]
        guard panel.runModal() == .OK else { return }
        for url in panel.urls { add(fileURL: url) }
    }

    private func dropped(_ providers: [NSItemProvider]) -> Bool {
        var handled = false
        for p in providers {
            if p.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier) {
                handled = true
                _ = p.loadObject(ofClass: URL.self) { url, _ in
                    guard let url else { return }
                    Task { @MainActor in add(fileURL: url) }
                }
            } else if p.hasItemConformingToTypeIdentifier(UTType.image.identifier) {
                handled = true
                p.loadDataRepresentation(forTypeIdentifier: UTType.image.identifier) { data, _ in
                    guard let data else { return }
                    Task { @MainActor in add(data, name: "圖片") }
                }
            }
        }
        return handled
    }

    /// ⌘V:剪貼簿裡是圖片(而且沒有文字)時,由這裡接手;其他情況照常貼文字
    private func installPasteMonitor() {
        guard monitor == nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { event in
            guard focused, event.modifierFlags.intersection(.deviceIndependentFlagsMask) == .command,
                  event.charactersIgnoringModifiers == "v" else { return event }
            let pb = NSPasteboard.general
            if let urls = pb.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL],
               !urls.isEmpty, urls.allSatisfy({ ["png", "jpg", "jpeg", "gif", "webp", "heic", "tiff"].contains($0.pathExtension.lowercased()) }) {
                urls.forEach(add(fileURL:))
                return nil
            }
            guard pb.string(forType: .string) == nil else { return event }
            for type in [NSPasteboard.PasteboardType.png, .tiff] {
                if let data = pb.data(forType: type) {
                    add(data, name: "圖片")
                    return nil
                }
            }
            return event
        }
    }
}

/// Markdown 裡的圖片:本機附件(/api/attachments/…)或 https 圖片;點一下開原圖
struct MarkdownImage: View {
    @EnvironmentObject var app: AppState
    let alt: String
    let src: String

    var body: some View {
        if let id = Attachments.id(fromURL: src) {
            if let data = Attachments.read(id, in: app.dataDir), let img = NSImage(data: data) {
                boxed(Image(nsImage: img).resizable().scaledToFit(), size: img.size)
                    .onTapGesture { NSWorkspace.shared.open(Attachments.fileURL(id, in: app.dataDir)) }
            } else {
                missing
            }
        } else if src.hasPrefix("https://"), let url = URL(string: src) {
            AsyncImage(url: url) { phase in
                if let image = phase.image {
                    boxed(image.resizable().scaledToFit(), size: nil)
                } else if phase.error != nil {
                    missing
                } else {
                    ProgressView().controlSize(.small)
                }
            }
            .onTapGesture { NSWorkspace.shared.open(url) }
        } else {
            Text(alt.isEmpty ? src : alt)
        }
    }

    private func boxed(_ v: some View, size: CGSize?) -> some View {
        v.frame(maxWidth: min(size?.width ?? 640, 640), maxHeight: min(size?.height ?? 420, 420), alignment: .leading)
            .clipShape(RoundedRectangle(cornerRadius: 6))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(.quaternary))
            .help(alt.isEmpty ? "點一下看原圖" : "\(alt)(點一下看原圖)")
    }

    private var missing: some View {
        Label(alt.isEmpty ? "圖片不在" : "\(alt)(圖片不在)", systemImage: "photo.badge.exclamationmark")
            .font(.caption).foregroundStyle(.secondary)
    }
}
