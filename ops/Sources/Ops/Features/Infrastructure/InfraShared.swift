import SwiftUI

// 伺服器與服務頁面內部共用的小元件與工具。

/// 穩定的字串雜湊(String.hashValue 每次啟動都不同,不能拿來產生固定的圖)
func infraHash(_ s: String) -> UInt64 {
    var h: UInt64 = 1_469_598_103_934_665_603
    for b in s.utf8 {
        h ^= UInt64(b)
        h &*= 1_099_511_628_211
    }
    return h
}

/// 由目前數值往回推一段看起來合理的歷史曲線。同一個 seed 每次產生一樣的形狀,最後一點等於 current。
func infraSynthSeries(seed: String, current: Double, span: TimeInterval, count: Int = 48,
                      volatility: Double = 0.15, floor: Double = 0) -> [MetricPoint] {
    guard count > 1 else { return [] }
    var state = infraHash(seed) | 1
    func next() -> Double {
        state = state &* 6_364_136_223_846_793_005 &+ 1_442_695_040_888_963_407
        return Double(state >> 11) / Double(1 << 53)
    }
    // 對齊到整分鐘,避免每次重畫都位移
    let end = Date(timeIntervalSinceReferenceDate: (Date().timeIntervalSinceReferenceDate / 60).rounded(.down) * 60)
    let step = span / Double(count - 1)
    let phase = next() * .pi * 2
    let base = max(abs(current), 0.0001)
    var points: [MetricPoint] = []
    points.reserveCapacity(count)
    for i in 0..<count {
        let t = Double(i) / Double(count - 1)
        let wave = sin(t * .pi * 4 + phase) * 0.5 + sin(t * .pi * 11 + phase * 2) * 0.25
        let noise = (next() - 0.5)
        // 越接近現在越貼近 current
        let weight = 1 - t * t
        let v = i == count - 1 ? current : current + base * volatility * (wave + noise) * (0.3 + weight)
        points.append(MetricPoint(date: end.addingTimeInterval(-span + step * Double(i)), value: max(floor, v)))
    }
    return points
}

/// 依使用率決定顏色(與 UsageBar 一致)
func infraUsageColor(_ v: Double) -> Color { v > 85 ? .red : v > 70 ? .orange : .green }

func infraBudgetColor(_ remaining: Double) -> Color { remaining < 0.2 ? .red : remaining < 0.5 ? .orange : .green }

/// 服務關鍵等級標籤
struct InfraTierPill: View {
    let tier: Int
    var body: some View {
        Pill(text: "T\(tier)", color: tier <= 1 ? .purple : tier == 2 ? .blue : .gray)
            .help("關鍵等級 \(tier)(1 = 最關鍵)")
    }
}

/// 錯誤預算剩餘的細長條
struct InfraBudgetBar: View {
    let remaining: Double   // 0...1
    var height: CGFloat = 6
    var body: some View {
        GeometryReader { g in
            ZStack(alignment: .leading) {
                Capsule().fill(Color.primary.opacity(0.08))
                Capsule().fill(infraBudgetColor(remaining).gradient)
                    .frame(width: g.size.width * min(1, max(0, remaining)))
            }
        }
        .frame(height: height)
    }
}

/// 可點的小膠囊(相依服務、主機、標籤)
struct InfraChip: View {
    let text: String
    var color: Color = .secondary
    var icon: String?
    var action: (() -> Void)?

    var body: some View {
        if let action {
            Button(action: action) { label }
                .buttonStyle(.plain)
                .contentShape(Capsule())
        } else {
            label
        }
    }

    private var label: some View {
        HStack(spacing: 5) {
            if let icon { Image(systemName: icon).foregroundStyle(color) }
            Text(text).lineLimit(1)
            if action != nil { Image(systemName: "chevron.right").font(.caption2).foregroundStyle(.tertiary) }
        }
        .font(.callout)
        .padding(.horizontal, 9).padding(.vertical, 4)
        .background(Color.primary.opacity(0.05), in: Capsule())
        .overlay(Capsule().strokeBorder(Color.primary.opacity(0.08)))
    }
}

/// 自動換行的水平排列
struct InfraFlowLayout: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let maxWidth = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, rowH: CGFloat = 0, widest: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x > 0 && x + s.width > maxWidth {
                y += rowH + spacing
                x = 0
                rowH = 0
            }
            x += s.width + spacing
            rowH = max(rowH, s.height)
            widest = max(widest, x - spacing)
        }
        return CGSize(width: min(widest, maxWidth), height: y + rowH)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, rowH: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x > bounds.minX && x + s.width > bounds.maxX {
                y += rowH + spacing
                x = bounds.minX
                rowH = 0
            }
            v.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(s))
            x += s.width + spacing
            rowH = max(rowH, s.height)
        }
    }
}

/// 動作完成後的短暫提示
struct InfraToast: View {
    let text: String
    var icon = "checkmark.circle.fill"
    var color: Color = .green

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: icon).foregroundStyle(color)
            Text(text).lineLimit(2)
        }
        .font(.callout)
        .padding(.horizontal, 14).padding(.vertical, 9)
        .background(.regularMaterial, in: Capsule())
        .overlay(Capsule().strokeBorder(Color.primary.opacity(0.1)))
        .shadow(color: .black.opacity(0.12), radius: 8, y: 3)
        .transition(.move(edge: .top).combined(with: .opacity))
    }
}

/// 管理提示顯示/消失
@MainActor
final class InfraToastModel: ObservableObject {
    @Published var message: String?
    private var task: Task<Void, Never>?

    func show(_ text: String) {
        task?.cancel()
        withAnimation(.snappy) { message = text }
        task = Task { [weak self] in
            try? await Task.sleep(for: .seconds(2.6))
            guard !Task.isCancelled else { return }
            withAnimation(.easeOut) { self?.message = nil }
        }
    }
}

/// 規格表的一列
struct InfraSpecRow: View {
    let label: String
    let value: String
    var mono = false
    var body: some View {
        GridRow {
            Text(label).foregroundStyle(.secondary).gridColumnAlignment(.leading)
            Text(value)
                .font(mono ? .body.monospaced() : .body)
                .textSelection(.enabled)
                .lineLimit(1)
                .gridColumnAlignment(.leading)
        }
    }
}

/// 卡片內的區塊標題列(標題 + 右側小字)
struct InfraSectionHeader: View {
    let title: String
    var trailing: String?
    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(title).font(.headline)
            Spacer()
            if let trailing { Text(trailing).font(.caption).foregroundStyle(.secondary) }
        }
    }
}
