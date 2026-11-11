import Foundation

// Facebook 粉絲專頁(Meta Graph API):設定、草稿與畫面上的資料。
// 權杖(使用者權杖、各專頁的 Page access token)存在鑰匙圈,帳號名稱帶組織前綴;
// 專頁清單、要管理哪些專頁與草稿存在目前組織資料夾的 facebook.json,解碼寬鬆。

/// 粉絲專頁(不含權杖)
struct FBPage: Codable, Hashable, Identifiable {
    var id: String
    var name: String
    var category = ""
    var link = ""
    var picture = ""
    var fanCount: Int?
    var followersCount: Int?
    /// 要在 Ops 管理這個專頁
    var managed = true

    init(id: String, name: String, category: String = "", link: String = "", picture: String = "",
         fanCount: Int? = nil, followersCount: Int? = nil, managed: Bool = true) {
        self.id = id; self.name = name; self.category = category; self.link = link; self.picture = picture
        self.fanCount = fanCount; self.followersCount = followersCount; self.managed = managed
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = (try? c.decode(String.self, forKey: .id)) ?? ""
        name = (try? c.decode(String.self, forKey: .name)) ?? id
        category = (try? c.decode(String.self, forKey: .category)) ?? ""
        link = (try? c.decode(String.self, forKey: .link)) ?? ""
        picture = (try? c.decode(String.self, forKey: .picture)) ?? ""
        fanCount = try? c.decode(Int.self, forKey: .fanCount)
        followersCount = try? c.decode(Int.self, forKey: .followersCount)
        managed = (try? c.decode(Bool.self, forKey: .managed)) ?? true
    }
}

enum FBDraftStatus: String, Codable, CaseIterable {
    case draft, scheduled, published
    var label: String {
        switch self {
        case .draft: return "草稿"
        case .scheduled: return "已排程"
        case .published: return "已發佈"
        }
    }
}

/// 貼文草稿。AI(角色)透過 MCP 只能新增或修改草稿;發佈與排程只能由人在 app 裡按
struct FBDraft: Codable, Hashable, Identifiable {
    var id: String                    // FD-1
    var pageId = ""
    var message = ""
    var link = ""
    var imageURL = ""
    /// 本機圖片的完整路徑(只有人在 app 裡能選)
    var localImage = ""
    /// 建議的發佈時間(角色可以提議;真正排程由人決定)
    var proposedTime: Date?
    /// 從哪裡帶入,例如 doc:DOC-3、task:T-5
    var source = ""
    var author: String                // person id、"mcp" 或 "agent:<角色 id>"
    var status: FBDraftStatus = .draft
    var postId: String?
    var scheduledFor: Date?
    var publishedAt: Date?
    var publishedBy: String?
    var createdAt = Date.now
    var updatedAt = Date.now

    var number: Int { Int(id.filter(\.isNumber)) ?? 0 }
    var isEmpty: Bool {
        [message, link, imageURL, localImage].allSatisfy { $0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    }
    var titleLine: String {
        let first = message.split(separator: "\n").first.map(String.init) ?? ""
        if !first.isEmpty { return first.count > 40 ? String(first.prefix(40)) + "…" : first }
        return link.isEmpty ? (imageURL.isEmpty && localImage.isEmpty ? "(空白草稿)" : "(圖片)") : link
    }

    init(id: String, author: String) {
        self.id = id
        self.author = author
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = (try? c.decode(String.self, forKey: .id)) ?? ""
        pageId = (try? c.decode(String.self, forKey: .pageId)) ?? ""
        message = (try? c.decode(String.self, forKey: .message)) ?? ""
        link = (try? c.decode(String.self, forKey: .link)) ?? ""
        imageURL = (try? c.decode(String.self, forKey: .imageURL)) ?? ""
        localImage = (try? c.decode(String.self, forKey: .localImage)) ?? ""
        proposedTime = try? c.decode(Date.self, forKey: .proposedTime)
        source = (try? c.decode(String.self, forKey: .source)) ?? ""
        author = (try? c.decode(String.self, forKey: .author)) ?? "tim"
        status = (try? c.decode(FBDraftStatus.self, forKey: .status)) ?? .draft
        postId = try? c.decode(String.self, forKey: .postId)
        scheduledFor = try? c.decode(Date.self, forKey: .scheduledFor)
        publishedAt = try? c.decode(Date.self, forKey: .publishedAt)
        publishedBy = try? c.decode(String.self, forKey: .publishedBy)
        createdAt = (try? c.decode(Date.self, forKey: .createdAt)) ?? .now
        updatedAt = (try? c.decode(Date.self, forKey: .updatedAt)) ?? createdAt
    }
}

/// facebook.json
struct FBSettings: Codable, Equatable {
    static let defaultVersion = "v24.0"

    /// Graph API 版本(Meta 每個版本約支援兩年)
    var apiVersion = FBSettings.defaultVersion
    /// 權杖怎麼來的:oauth(Meta 登入)或 page_token(直接貼)
    var mode = ""
    var pages: [FBPage] = []
    var drafts: [FBDraft] = []

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let v = (try? c.decode(String.self, forKey: .apiVersion)) ?? ""
        apiVersion = FB.isVersion(v) ? v : FBSettings.defaultVersion
        mode = (try? c.decode(String.self, forKey: .mode)) ?? ""
        pages = ((try? c.decode([FBPage].self, forKey: .pages)) ?? []).filter { !$0.id.isEmpty }
        drafts = ((try? c.decode([FBDraft].self, forKey: .drafts)) ?? []).filter { !$0.id.isEmpty }
    }

    var managedPages: [FBPage] { pages.filter(\.managed) }
}

// MARK: - 從 Graph API 讀到的資料(畫面與 MCP 用,不存檔)

/// 一個成效指標。Meta 會淘汰指標,取不到時 status 是 unavailable(沒有資料)或 deprecated(已淘汰 / 不再支援)
struct FBMetric: Hashable, Encodable, Identifiable {
    enum Status: String, Encodable { case ok, unavailable, deprecated }
    struct Point: Hashable, Encodable {
        var date: Date
        var value: Double
    }

    var key: String                   // reach、engagement…
    var label: String
    var metric: String?               // 實際用到的 Graph 指標名稱
    var status: Status
    var total: Double?
    var values: [Point] = []
    var breakdown: [String: Double]?  // 例如心情:like、love…
    var note: String?

    var id: String { key }
    var statusLabel: String {
        switch status {
        case .ok: return ""
        case .unavailable: return "無資料"
        case .deprecated: return "已淘汰"
        }
    }
}

struct FBPost: Hashable, Encodable, Identifiable {
    var id: String
    var pageId: String
    var message: String
    var createdTime: Date?
    var scheduledTime: Date?
    var permalink: String
    var picture: String
    var isPublished: Bool
    var statusType: String
    var reactions: Int?
    var comments: Int?
    var shares: Int?
    var insights: [FBMetric] = []

    /// 互動 = 心情 + 留言 + 分享(貼文欄位,不受 insights 淘汰影響)
    var engagement: Int? {
        guard reactions != nil || comments != nil || shares != nil else { return nil }
        return (reactions ?? 0) + (comments ?? 0) + (shares ?? 0)
    }
}

struct FBPageInsights: Hashable, Encodable {
    var pageId: String
    var period: String
    var since: Date
    var until: Date
    var metrics: [FBMetric]
}

/// 畫面上的 Facebook 狀態
struct FBSnapshot: Equatable {
    var posts: [String: [FBPost]] = [:]
    var scheduled: [String: [FBPost]] = [:]
    var insights: [String: FBPageInsights] = [:]
    var errors: [String: String] = [:]
    var error: String?
    var busy = false
    var fetchedAt: Date?
    /// 權杖過期(code 190):要請人重新連線
    var tokenExpired = false
}

struct FBError: LocalizedError, Equatable {
    var message: String
    var code: Int = 0
    var subcode: Int = 0
    var status: Int = 0

    init(_ message: String, code: Int = 0, subcode: Int = 0, status: Int = 0) {
        self.message = message; self.code = code; self.subcode = subcode; self.status = status
    }

    var isTokenExpired: Bool { code == 190 || code == 102 }
    /// 指標不存在或已淘汰:(#100) The value must be a valid insights metric
    var isInvalidMetric: Bool { code == 100 && (message.localizedCaseInsensitiveContains("metric") || subcode == 0) }

    var errorDescription: String? {
        if isTokenExpired { return "Facebook 權杖已過期或被撤銷(code \(code)),請到 Ops 的 AI 自動化 › 粉絲專頁 重新連線。\(message)" }
        return code > 0 ? "Facebook:\(message)(code \(code))" : message
    }
}
