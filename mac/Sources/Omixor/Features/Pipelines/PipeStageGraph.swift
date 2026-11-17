import SwiftUI

/// 橫向階段圖:每個階段一個方塊,中間用線連起來
struct PipeStageGraph: View {
    let run: PipelineRun
    @Binding var selectedJobId: String?

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(alignment: .top, spacing: 0) {
                ForEach(Array(run.stages.enumerated()), id: \.element.id) { i, stage in
                    if i > 0 {
                        PipeConnector(from: run.stages[i - 1].status, to: stage.status)
                            .padding(.top, 19)
                    }
                    PipeStageNode(stage: stage, selectedJobId: $selectedJobId)
                }
            }
            .padding(.vertical, 4)
            .padding(.horizontal, 2)
        }
    }
}

struct PipeConnector: View {
    let from: RunStatus
    let to: RunStatus

    private var color: Color {
        switch from {
        case .succeeded: return to == .queued ? Color.secondary.opacity(0.4) : .green.opacity(0.7)
        case .failed: return .red.opacity(0.5)
        default: return Color.secondary.opacity(0.35)
        }
    }

    var body: some View {
        HStack(spacing: 0) {
            Rectangle()
                .fill(color)
                .frame(width: 26, height: 2)
                .overlay {
                    if from == .succeeded && to == .running { PipeFlowDots() }
                }
            Image(systemName: "chevron.right")
                .font(.system(size: 8, weight: .bold))
                .foregroundStyle(color)
        }
        .frame(width: 34)
    }
}

/// 連線上流動的點,表示資料正往下一個階段走
private struct PipeFlowDots: View {
    @State private var x: CGFloat = -12
    var body: some View {
        Circle().fill(Color.blue).frame(width: 5, height: 5)
            .offset(x: x)
            .onAppear {
                withAnimation(.linear(duration: 1).repeatForever(autoreverses: false)) { x = 12 }
            }
    }
}

struct PipeStageNode: View {
    let stage: PipelineStage
    @Binding var selectedJobId: String?
    @State private var pulse = false

    private var tint: Color { stage.status == .canceled ? .gray : stage.status.color }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                PipeStatusIcon(status: stage.status).font(.callout)
                Text(stage.name).font(.callout.weight(.semibold)).lineLimit(1)
                Spacer(minLength: 2)
            }
            HStack(spacing: 4) {
                Text(stage.status.label)
                Spacer()
                Text(Fmt.duration(stage.duration)).monospacedDigit()
            }
            .font(.caption2).foregroundStyle(.secondary)

            if !stage.jobs.isEmpty {
                Divider().padding(.vertical, 1)
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(stage.jobs) { job in
                        Button { selectedJobId = job.id } label: {
                            HStack(spacing: 5) {
                                Circle().fill(job.status == .canceled ? Color.gray : job.status.color)
                                    .frame(width: 6, height: 6)
                                Text(job.name).font(.caption).lineLimit(1)
                                Spacer(minLength: 0)
                            }
                            .padding(.horizontal, 4).padding(.vertical, 2)
                            .background(RoundedRectangle(cornerRadius: 4)
                                .fill(selectedJobId == job.id ? Color.accentColor.opacity(0.2) : .clear))
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .help("\(job.name) · \(job.status.label) · \(Fmt.duration(job.duration))")
                    }
                }
            }
        }
        .padding(10)
        .frame(width: 168, alignment: .leading)
        .background(tint.opacity(stage.status == .queued ? 0.04 : 0.09), in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .strokeBorder(tint.opacity(stage.status == .running ? (pulse ? 0.9 : 0.35) : 0.35),
                              lineWidth: stage.status == .running ? 2 : 1)
        )
        .onAppear { startPulse() }
        .onChange(of: stage.status) { startPulse() }
    }

    private func startPulse() {
        guard stage.status == .running else { pulse = false; return }
        withAnimation(.easeInOut(duration: 0.9).repeatForever(autoreverses: true)) { pulse = true }
    }
}
