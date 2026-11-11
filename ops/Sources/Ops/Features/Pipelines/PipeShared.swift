import AppKit
import SwiftUI

// CI/CD 畫面共用的小工具與元件。

// MARK: - 模型輔助

extension PipelineRun {
    var cicdJobs: [PipelineJob] { stages.flatMap(\.jobs) }

    /// 已結束的 job 比例(0...1)
    var cicdProgress: Double {
        let all = cicdJobs
        guard !all.isEmpty else { return status == .succeeded ? 1 : 0 }
        let done = all.filter { [.succeeded, .failed, .canceled].contains($0.status) }.count
        return Double(done) / Double(all.count)
    }

    var cicdIsActive: Bool { status == .running || status == .queued }

    var cicdIsAI: Bool {
        if case .ai = triggeredBy { return true }
        return false
    }

    func cicdElapsed(_ now: Date) -> TimeInterval {
        duration ?? max(0, now.timeIntervalSince(startedAt))
    }

    var cicdShortSHA: String { String(commitSHA.prefix(7)) }

    /// 最值得先看的 job:失敗 > 執行中 > 等待核准 > 最後一個有日誌的
    var cicdFocusJob: PipelineJob? {
        let all = cicdJobs
        return all.first { $0.status == .failed }
            ?? all.first { $0.status == .running }
            ?? all.first { $0.status == .waitingApproval }
            ?? all.last { !$0.log.isEmpty }
            ?? all.first
    }
}

extension Deployment {
    var cicdIsActive: Bool { status == .running || status == .queued || status == .waitingApproval }
    var cicdIsRollback: Bool { changes.contains { $0.contains("回滾") } }
}

extension CheckStatus {
    var cicdLabel: String {
        switch self {
        case .pending: return "等待中"
        case .running: return "檢查中"
        case .passed: return "通過"
        case .failed: return "未通過"
        }
    }
}

extension AppState {
    func cicdServiceName(_ id: String) -> String { service(id)?.name ?? id }
    func cicdRepoName(_ id: String) -> String { repos.first { $0.id == id }?.name ?? id }

    /// 負責修 CI 的 AI 自動化(找不到就用通用 id)
    var cicdFixerActor: Actor {
        let hit = automations.first {
            let s = ($0.id + $0.name + $0.trigger).lowercased()
            return s.contains("ci") || s.contains("build") || s.contains("修復")
        } ?? automations.first
        return .ai(hit?.id ?? "ci-fixer")
    }

    /// 以既有 run 為範本開一個新的 run(用於 AI 修復後的驗證)
    @discardableResult
    func cicdStartRun(from run: PipelineRun, trigger: String, actor: Actor, branch: String? = nil, message: String? = nil) -> Int {
        let newId = (pipelineRuns.map(\.id).max() ?? 0) + 1
        var stages = run.stages
        for s in stages.indices {
            stages[s].status = s == 0 ? .running : .queued
            for j in stages[s].jobs.indices {
                stages[s].jobs[j].status = (s == 0 && j == 0) ? .running : .queued
                stages[s].jobs[j].duration = nil
                stages[s].jobs[j].log = []
            }
        }
        let new = PipelineRun(id: newId, pipeline: run.pipeline, repo: run.repo, branch: branch ?? run.branch,
                              commitSHA: run.commitSHA, commitMessage: message ?? run.commitMessage, trigger: trigger,
                              triggeredBy: actor, status: .running, stages: stages, startedAt: .now,
                              duration: nil, aiDiagnosis: nil, targetEnv: run.targetEnv)
        pipelineRuns.insert(new, at: 0)
        return newId
    }

    /// 取消後把還沒跑完的階段與 job 也標成已取消,畫面才一致
    func cicdCancel(runId: Int) {
        cancel(runId: runId)
        guard let i = pipelineRuns.firstIndex(where: { $0.id == runId }) else { return }
        var r = pipelineRuns[i]
        for s in r.stages.indices {
            for j in r.stages[s].jobs.indices where [.running, .queued, .waitingApproval].contains(r.stages[s].jobs[j].status) {
                r.stages[s].jobs[j].status = .canceled
            }
            if [.running, .queued, .waitingApproval].contains(r.stages[s].status) { r.stages[s].status = .canceled }
        }
        r.duration = Date().timeIntervalSince(r.startedAt)
        pipelineRuns[i] = r
    }

    /// 核准 run 裡等待核准的關卡(例如 Deploy production)
    func cicdApproveGate(runId: Int) {
        guard let i = pipelineRuns.firstIndex(where: { $0.id == runId }) else { return }
        var r = pipelineRuns[i]
        r.status = .running
        for s in r.stages.indices where r.stages[s].status == .waitingApproval {
            r.stages[s].status = .running
            for j in r.stages[s].jobs.indices where r.stages[s].jobs[j].status == .waitingApproval {
                r.stages[s].jobs[j].status = .queued
            }
        }
        pipelineRuns[i] = r
        let target = r.targetEnv?.label ?? "Production"
        log(.person(me), "checkmark.seal", "核准 \(r.pipeline) #\(r.id) 部署到 \(target)", .pipelines)
    }

    /// 由 AI 產生失敗診斷(模擬)
    func cicdDiagnose(runId: Int) {
        guard let i = pipelineRuns.firstIndex(where: { $0.id == runId }) else { return }
        let r = pipelineRuns[i]
        let failedStage = r.stages.first { $0.status == .failed }
        let failedJob = r.cicdJobs.first { $0.status == .failed }
        let errLine = failedJob?.log.last { l in
            let s = l.lowercased()
            return s.contains("error") || s.contains("fail") || s.contains("錯誤")
        }
        var text = "失敗發生在「\(failedStage?.name ?? "未知階段")」的 \(failedJob?.name ?? "job")。"
        if let errLine { text += "關鍵錯誤:\(errLine.trimmingCharacters(in: .whitespaces))。" }
        text += "這個錯誤與本次 commit 的變更有關的機率較高,建議先檢查相關測試與相依版本;若為暫時性問題,重新執行即可。"
        pipelineRuns[i].aiDiagnosis = text
        log(cicdFixerActor, "sparkles", "完成 \(r.pipeline) #\(r.id) 的失敗分析", .pipelines)
    }
}

/// 本次啟動期間由 CI/CD 畫面發起的部署(完成後要回寫服務版本)
@MainActor
enum CICDSession {
    static var trackedDeployments: Set<String> = []
}

// MARK: - 元件

/// 狀態圖示,執行中會旋轉
struct PipeStatusIcon: View {
    let status: RunStatus
    var body: some View {
        if status == .running {
            PipeSpinner(color: status.color)
        } else {
            Image(systemName: status.icon)
                .foregroundStyle(status.color)
                .symbolEffect(.pulse, isActive: status == .waitingApproval)
                .help(status.label)
        }
    }
}

struct PipeSpinner: View {
    var color: Color = .blue
    @State private var angle: Double = 0
    var body: some View {
        Image(systemName: "arrow.triangle.2.circlepath")
            .foregroundStyle(color)
            .rotationEffect(.degrees(angle))
            .onAppear {
                withAnimation(.linear(duration: 1.6).repeatForever(autoreverses: false)) { angle = 360 }
            }
            .help(RunStatus.running.label)
    }
}

/// 進度條(含百分比)
struct PipeProgressBar: View {
    let value: Double
    var color: Color = .blue
    var showLabel = true
    var body: some View {
        HStack(spacing: 6) {
            GeometryReader { g in
                ZStack(alignment: .leading) {
                    Capsule().fill(Color.primary.opacity(0.08))
                    Capsule().fill(color.gradient)
                        .frame(width: max(4, g.size.width * min(1, max(0, value))))
                        .animation(.easeInOut(duration: 0.6), value: value)
                }
            }
            .frame(height: 6)
            if showLabel {
                Text("\(Int((value * 100).rounded()))%")
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .frame(width: 32, alignment: .trailing)
            }
        }
    }
}

/// 即時更新的經過時間
struct PipeElapsedText: View {
    let run: PipelineRun
    var body: some View {
        if run.cicdIsActive || run.status == .waitingApproval {
            TimelineView(.periodic(from: .now, by: 1)) { ctx in
                Text(Fmt.duration(run.cicdElapsed(ctx.date))).monospacedDigit()
            }
        } else {
            Text(Fmt.duration(run.duration)).monospacedDigit()
        }
    }
}

enum PipeClipboard {
    static func copy(_ s: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
    }
}
