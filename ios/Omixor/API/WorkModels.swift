import Foundation

// 待決定 / 需要協助(core/model/decision.go)與 Issue 的 commits 連結(core/model/commit.go、service/commits.go)。
// 還沒有畫面用到:先把 API 這一層備好。字串型的分類(kind、status…)不轉成 enum:伺服器新增值時不會解碼失敗。

struct DecisionOption: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var label: String = ""
    var description: String = ""
    var recommended: Bool = false

    enum CodingKeys: String, CodingKey { case id, label, description, recommended }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        label = c.string(.label); description = c.string(.description)
        recommended = (try? c.decodeIfPresent(Bool.self, forKey: .recommended)) ?? false
    }
}

struct Decision: Codable, Identifiable, Hashable, Sendable {
    /// kind
    static let kindDecision = "decision", kindHelp = "help"
    /// 回答 decision 選了選項以外的內容(要附 note);help 的回答是 done 或 declined
    static let answerOther = "other", answerDone = "done", answerDeclined = "declined"

    var id: String
    /// decision(從選項裡挑)| help(需要人協助,回答 done / declined)
    var kind: String = Decision.kindDecision
    var title: String = ""
    var body: String = ""
    var options: [DecisionOption] = []
    /// open | decided | withdrawn(未知的值照樣保留)
    var status: String = "open"
    var answer: String = ""
    var answerNote: String = ""
    var evidence: String = ""
    /// 來源 Issue 的顯示 key(沒有是空字串)
    var sourceIssue: String = ""
    var blocks: [String] = []
    var createdBy: String = ""
    var createdByActor: ActorView?
    var decidedBy: String = ""
    var decidedByActor: ActorView?
    var createdAt: Date?
    var decidedAt: Date?
    var updatedAt: Date?

    var isOpen: Bool { status == "open" }
    var isHelp: Bool { kind == Decision.kindHelp }

    enum CodingKeys: String, CodingKey {
        case id, kind, title, body, options, status, answer, answerNote, evidence, sourceIssue, blocks
        case createdBy, createdByActor, decidedBy, decidedByActor, createdAt, decidedAt, updatedAt
    }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        kind = c.string(.kind).nonEmpty ?? Decision.kindDecision
        title = c.string(.title); body = c.string(.body)
        options = c.list(.options)
        status = c.string(.status).nonEmpty ?? "open"
        answer = c.string(.answer); answerNote = c.string(.answerNote); evidence = c.string(.evidence)
        sourceIssue = c.string(.sourceIssue)
        blocks = c.list(.blocks)
        createdBy = c.string(.createdBy); decidedBy = c.string(.decidedBy)
        createdByActor = try? c.decodeIfPresent(ActorView.self, forKey: .createdByActor)
        decidedByActor = try? c.decodeIfPresent(ActorView.self, forKey: .decidedByActor)
        createdAt = try? c.decodeIfPresent(Date.self, forKey: .createdAt)
        decidedAt = try? c.decodeIfPresent(Date.self, forKey: .decidedAt)
        updatedAt = try? c.decodeIfPresent(Date.self, forKey: .updatedAt)
    }
}

/// GET …/decisions/summary:來源 Issue key → 筆數
struct DecisionCount: Codable, Hashable, Sendable {
    var open: Int = 0
    var decided: Int = 0

    enum CodingKeys: String, CodingKey { case open, decided }
    init(open: Int = 0, decided: Int = 0) { self.open = open; self.decided = decided }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        open = (try? c.decodeIfPresent(Int.self, forKey: .open)) ?? 0
        decided = (try? c.decodeIfPresent(Int.self, forKey: .decided)) ?? 0
    }
}

/// POST …/decisions/{id}/to-issue 的回應
struct DecisionIssueResult: Decodable, Sendable {
    var issue: Issue
    var decision: Decision
}

// MARK: commits

/// Issue 連結的一筆 commit(model.IssueCommitView)
struct IssueCommit: Codable, Identifiable, Hashable, Sendable {
    var id: String { repo + "@" + sha }
    /// Issue 顯示 key
    var issue: String = ""
    var repo: String = ""
    var sha: String = ""
    var sha7: String = ""
    var subject: String = ""
    /// mentions | fixes | found_in | manual
    var kind: String = ""
    /// auto | manual…
    var origin: String = ""
    var url: String = ""
    var authorName: String = ""
    var authoredAt: Date?
    var linkedBy: String = ""
    var linkedAt: Date?
    var note: String = ""
    /// 回報過這個 sha(false = 只有裸參照,沒有主旨)
    var verified: Bool = false

    enum CodingKeys: String, CodingKey {
        case issue, repo, sha, sha7, subject, kind, origin, url, authorName, authoredAt, linkedBy, linkedAt, note, verified
    }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        issue = c.string(.issue); repo = c.string(.repo); sha = c.string(.sha)
        sha7 = c.string(.sha7).nonEmpty ?? String(sha.prefix(7))
        subject = c.string(.subject); kind = c.string(.kind); origin = c.string(.origin)
        url = c.string(.url); authorName = c.string(.authorName); linkedBy = c.string(.linkedBy); note = c.string(.note)
        authoredAt = try? c.decodeIfPresent(Date.self, forKey: .authoredAt)
        linkedAt = try? c.decodeIfPresent(Date.self, forKey: .linkedAt)
        verified = (try? c.decodeIfPresent(Bool.self, forKey: .verified)) ?? false
    }
}

/// GET …/issues/{id}/commits:total 是全部連結數,commits 只有最近的 limit 筆
struct IssueCommits: Decodable, Sendable {
    var issue: String = ""
    var total: Int = 0
    var commits: [IssueCommit] = []

    enum CodingKeys: String, CodingKey { case issue, total, commits }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        issue = c.string(.issue)
        total = (try? c.decodeIfPresent(Int.self, forKey: .total)) ?? 0
        commits = c.list(.commits)
    }
}

extension String {
    var nonEmpty: String? { isEmpty ? nil : self }
}
