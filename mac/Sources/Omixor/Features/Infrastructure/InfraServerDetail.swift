import AppKit
import Charts
import SwiftUI

// 單一主機的詳細面板:即時資源圖、規格、跑在上面的服務、容器、標籤與動作。

struct InfraServerDetail: View {
    @EnvironmentObject var app: AppState
    let server: Server
    @Binding var drained: Set<String>
    var onClose: () -> Void
    var onOpenService: (String) -> Void

    @StateObject private var toast = InfraToastModel()
    @State private var confirmReboot = false

    private var isDrained: Bool { drained.contains(server.id) }
    private var sshCommand: String { "ssh ops@\(server.ip)" }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                header
                actions
                liveCard
                specsCard
                servicesCard
                containersCard
                if !server.tags.isEmpty { tagsCard }
            }
            .padding(16)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .overlay(alignment: .top) {
            if let m = toast.message { InfraToast(text: m).padding(.top, 10) }
        }
        .confirmationDialog("要重新開機 \(server.name) 嗎?", isPresented: $confirmReboot) {
            Button("重新開機", role: .destructive, action: reboot)
            Button("取消", role: .cancel) {}
        } message: {
            Text("主機上 \(server.containers.count) 個容器會一起中斷,約需 1–2 分鐘恢復。\(server.env == .production ? "這是正式環境主機,建議先排空流量。" : "")")
        }
    }

    // MARK: 頁首與動作

    private var header: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(server.name).font(.title3.weight(.semibold)).lineLimit(1).textSelection(.enabled)
                Spacer(minLength: 4)
                Button(action: onClose) { Image(systemName: "xmark") }
                    .buttonStyle(.borderless)
                    .keyboardShortcut(.cancelAction)
                    .help("關閉")
            }
            HStack(spacing: 6) {
                HealthBadge(health: server.health)
                EnvBadge(env: server.env)
                if isDrained { Pill(text: "已排空", color: .blue, icon: "drop.triangle") }
            }
            Text(server.role).foregroundStyle(.secondary)
        }
    }

    private var actions: some View {
        InfraFlowLayout(spacing: 8) {
            Button {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(sshCommand, forType: .string)
                toast.show("已複製「\(sshCommand)」")
            } label: { Label("複製 SSH 指令", systemImage: "terminal") }
            .help(sshCommand)

            Button {
                if isDrained {
                    drained.remove(server.id)
                    app.log(.person(app.me), "arrow.uturn.left", "恢復 \(server.name) 接收流量", .infrastructure)
                    toast.show("\(server.name) 已恢復接收流量")
                } else {
                    drained.insert(server.id)
                    app.log(.person(app.me), "drop.triangle", "排空 \(server.name)(停止分配新流量)", .infrastructure)
                    toast.show("\(server.name) 已排空,不再分配新流量")
                }
            } label: {
                Label(isDrained ? "恢復流量" : "排空", systemImage: isDrained ? "arrow.uturn.left" : "drop.triangle")
            }

            Button(role: .destructive) { confirmReboot = true } label: {
                Label("重新開機", systemImage: "restart")
            }
            .disabled(server.health == .maintenance)
        }
        .buttonStyle(.bordered)
    }

    private func reboot() {
        let id = server.id
        let name = server.name
        guard let i = app.servers.firstIndex(where: { $0.id == id }) else { return }
        app.servers[i].health = .maintenance
        app.servers[i].uptimeDays = 0
        app.log(.person(app.me), "restart", "重新開機 \(name)", .infrastructure)
        toast.show("\(name) 重新開機中…")
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(6))
            if let j = app.servers.firstIndex(where: { $0.id == id }), app.servers[j].health == .maintenance {
                app.servers[j].health = .healthy
                app.log(.system, "checkmark.circle", "\(name) 重新開機完成", .infrastructure)
            }
        }
    }

    // MARK: 即時資源

    private var liveCard: some View {
        Card {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    Text("即時資源").font(.headline)
                    Spacer()
                    HStack(spacing: 5) {
                        StatusDot(color: .green, pulsing: true)
                        Text("每 3 秒更新").font(.caption).foregroundStyle(.secondary)
                    }
                }
                HStack(spacing: 18) {
                    liveStat("CPU", "\(Int(server.cpu))%", .blue)
                    liveStat("記憶體", "\(Int(server.memory))%", .purple)
                    liveStat("網路", "\(Int(server.networkMbps)) Mbps", .teal)
                    Spacer(minLength: 0)
                }
                if server.cpuSeries.count > 1 || server.memorySeries.count > 1 {
                    InfraLiveChart(cpu: server.cpuSeries, memory: server.memorySeries)
                        .frame(height: 160)
                } else {
                    Text("等待資料…").font(.callout).foregroundStyle(.tertiary)
                        .frame(maxWidth: .infinity, minHeight: 80)
                }
                UsageBar(value: server.disk, label: "磁碟")
            }
        }
    }

    private func liveStat(_ title: String, _ value: String, _ color: Color) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(spacing: 4) {
                Circle().fill(color).frame(width: 6, height: 6)
                Text(title).font(.caption).foregroundStyle(.secondary)
            }
            Text(value).font(.title3.monospacedDigit().weight(.medium))
        }
    }

    // MARK: 規格

    private var specsCard: some View {
        Card("規格") {
            Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 6) {
                InfraSpecRow(label: "供應商", value: server.provider)
                InfraSpecRow(label: "區域", value: server.region)
                InfraSpecRow(label: "作業系統", value: server.os)
                InfraSpecRow(label: "IP", value: server.ip, mono: true)
                InfraSpecRow(label: "CPU", value: "\(server.cores) 核心")
                InfraSpecRow(label: "記憶體", value: "\(server.memoryGB) GB")
                InfraSpecRow(label: "運作時間", value: server.uptimeDays == 0 ? "剛重新開機" : "\(server.uptimeDays) 天")
            }
            .font(.callout)
        }
    }

    // MARK: 服務

    private var servicesCard: some View {
        let list = app.services.filter { $0.hostIds.contains(server.id) }
        return Card {
            VStack(alignment: .leading, spacing: 8) {
                InfraSectionHeader(title: "執行的服務", trailing: "\(list.count) 個")
                if list.isEmpty {
                    Text("沒有登記在這台主機上的服務").font(.callout).foregroundStyle(.tertiary)
                } else {
                    InfraFlowLayout {
                        ForEach(list) { s in
                            InfraChip(text: s.name, color: s.health.color, icon: s.health.icon) { onOpenService(s.id) }
                        }
                    }
                }
            }
        }
    }

    // MARK: 容器

    private var containersCard: some View {
        Card {
            VStack(alignment: .leading, spacing: 8) {
                InfraSectionHeader(title: "容器", trailing: "\(server.containers.count) 個")
                if server.containers.isEmpty {
                    Text("沒有執行中的容器").font(.callout).foregroundStyle(.tertiary)
                } else {
                    Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 7) {
                        GridRow {
                            Text("名稱")
                            Text("狀態")
                            Text("重啟").gridColumnAlignment(.trailing)
                            Text("CPU").gridColumnAlignment(.trailing)
                            Text("記憶體").gridColumnAlignment(.trailing)
                        }
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        Divider().gridCellUnsizedAxes(.horizontal)
                        ForEach(server.containers) { c in
                            GridRow {
                                VStack(alignment: .leading, spacing: 1) {
                                    Text(c.name).font(.callout.weight(.medium)).lineLimit(1)
                                    Text(c.image).font(.caption2.monospaced()).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                                }
                                HealthBadge(health: c.status, compact: true)
                                Text("\(c.restarts)")
                                    .foregroundStyle(c.restarts > 3 ? .orange : .primary)
                                    .fontWeight(c.restarts > 3 ? .semibold : .regular)
                                Text(String(format: "%.0f%%", c.cpu))
                                Text(c.memoryMB >= 1024 ? String(format: "%.1f GB", c.memoryMB / 1024) : String(format: "%.0f MB", c.memoryMB))
                            }
                            .font(.callout.monospacedDigit())
                        }
                    }
                }
            }
        }
    }

    private var tagsCard: some View {
        Card("標籤") {
            InfraFlowLayout {
                ForEach(server.tags, id: \.self) { t in
                    Text(t).font(.caption.monospaced())
                        .padding(.horizontal, 7).padding(.vertical, 3)
                        .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 5))
                }
            }
        }
    }
}

/// CPU 與記憶體即時折線圖
struct InfraLiveChart: View {
    let cpu: [MetricPoint]
    let memory: [MetricPoint]

    var body: some View {
        Chart {
            ForEach(cpu) { p in
                LineMark(x: .value("時間", p.date), y: .value("使用率", p.value), series: .value("指標", "CPU"))
                    .foregroundStyle(by: .value("指標", "CPU"))
                    .interpolationMethod(.monotone)
            }
            ForEach(memory) { p in
                LineMark(x: .value("時間", p.date), y: .value("使用率", p.value), series: .value("指標", "記憶體"))
                    .foregroundStyle(by: .value("指標", "記憶體"))
                    .interpolationMethod(.monotone)
            }
            RuleMark(y: .value("警戒", 85))
                .foregroundStyle(.red.opacity(0.5))
                .lineStyle(StrokeStyle(lineWidth: 1, dash: [4, 3]))
        }
        .chartForegroundStyleScale(["CPU": Color.blue, "記憶體": Color.purple])
        .chartYScale(domain: 0...100)
        .chartYAxis {
            AxisMarks(values: [0, 25, 50, 75, 100]) { v in
                AxisGridLine()
                AxisValueLabel { if let d = v.as(Int.self) { Text("\(d)%") } }
            }
        }
        .chartXAxis {
            AxisMarks(values: .automatic(desiredCount: 4)) { _ in
                AxisGridLine()
                AxisValueLabel(format: .dateTime.hour().minute())
            }
        }
        .chartLegend(position: .top, alignment: .leading)
    }
}
