import Foundation

// 旁白語音批次:送出(npm run voice -- --batch)→ 定時查(--collect)→ 收完 → 人按發佈(PUT /api/admin/scripts)。
// 這裡只放資料模型;流程在 VoicePipeline.swift。

enum VoiceJobState: String, Codable, CaseIterable {
    case queued, submitting, processing, collecting, ready, publishing, published, nothingToDo, failed, canceled

    var label: String {
        switch self {
        case .queued: return "排隊中"
        case .submitting: return "送出中"
        case .processing: return "產生中"
        case .collecting: return "收結果中"
        case .ready: return "待發佈"
        case .publishing: return "發佈中"
        case .published: return "已發佈"
        case .nothingToDo: return "沒有缺的旁白"
        case .failed: return "失敗"
        case .canceled: return "已取消"
        }
    }

    var icon: String {
        switch self {
        case .queued: return "clock"
        case .submitting, .collecting, .publishing: return "arrow.triangle.2.circlepath"
        case .processing: return "waveform"
        case .ready: return "hand.raised.fill"
        case .published: return "checkmark.seal.fill"
        case .nothingToDo: return "checkmark.circle"
        case .failed: return "xmark.circle.fill"
        case .canceled: return "slash.circle"
        }
    }

    /// 已經結束,不會再變
    var isFinal: Bool { [.published, .nothingToDo, .failed, .canceled].contains(self) }
    /// 占著「一次只能有一批」的位置
    var holdsBatch: Bool { [.submitting, .processing, .collecting].contains(self) }
}

struct VoiceClip: Codable, Hashable {
    let text: String
    let file: String         // GCS 物件路徑,例如 launch/ab12cd34ef56.mp3
    let ms: Int

    var url: URL? { URL(string: "https://storage.googleapis.com/shuttle-voice/" + file) }
}

/// 收結果後有變動的一份旁白清單(.voice.json)
struct VoiceFileChange: Codable, Hashable {
    let path: String         // web/src/content/live/ 底下的路徑,例如 launch/1.1.voice.json
    let isNew: Bool          // 送出前本機沒有這個檔
    let clips: Int
    let totalMs: Int
    let added: [VoiceClip]   // 這次新增或換掉的段落
    let removed: Int         // 這次被拿掉的段落數
    let course: String?      // 從 courses_seed.json 查到這個腳本屬於哪一課;發佈時掛在那一課底下
    let lesson: String?
    let sha: String          // 收結果當下的內容雜湊;發佈前比對,避免送出之後又被改過的檔
    var publish: String?     // 發佈結果:nil 還沒發佈、"ok"、或錯誤訊息
}

struct VoiceLogLine: Codable, Hashable {
    let date: Date
    let text: String
}

struct VoiceBaseline: Codable, Hashable {
    let sha: String
    let files: [String]
}

struct VoiceJob: Identifiable, Codable, Hashable {
    let id: String                  // VJ-1
    var scripts: [String]           // live/ 底下的腳本路徑;all 時為空
    var all: Bool
    var limit: Int?
    var state: VoiceJobState
    let createdAt: Date
    var updatedAt: Date
    var by: String                  // 誰送出的:person id、"mcp" 或 "external"(Ops 以外送出的批次)
    var batchName: String?
    var clipCount: Int?             // 送出的段落數
    var nextCheckAt: Date?
    var lastCheckAt: Date?
    var checks = 0
    var batchState: String?         // Gemini 回報的批次狀態,例如 JOB_STATE_RUNNING
    var error: String?
    var log: [VoiceLogLine] = []
    var baseline: [String: VoiceBaseline] = [:]
    var changes: [VoiceFileChange] = []
    var failedClips: [String] = []
    var publishedAt: Date?
    var publishedTo: String?

    var title: String {
        if all { return "全部腳本" + (limit.map { "(每份前 \($0) 段)" } ?? "") }
        let head = scripts.prefix(3).joined(separator: "、")
        return head + (scripts.count > 3 ? " 等 \(scripts.count) 份" : "") + (limit.map { "(每份前 \($0) 段)" } ?? "")
    }

    var totalAddedMs: Int { changes.reduce(0) { $0 + $1.added.reduce(0) { $0 + $1.ms } } }

    mutating func note(_ text: String) {
        log.append(VoiceLogLine(date: .now, text: text))
        if log.count > 200 { log.removeFirst(log.count - 200) }
        updatedAt = .now
    }
}
