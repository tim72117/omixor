import Foundation

// 遠端模式(OmixorMode.swift)時訊息頻道不讀寫本機的 channels.json,改透過 omixor server(core)的
// /api/orgs/<組織>/channels 讀寫(規則由伺服器強制,見 core/api/channel.go)。
// 網址、token 與重新登入都和 Issue 共用 IssueRemote(Issues/IssueRemote.swift)。
//
// 以角色身分(MCP 的 agent 參數)發訊息、標已讀時,另外帶 author / member = agent:<id>;
// 核心的 REST 契約沒有這兩個欄位時,伺服器會忽略它們,改用登入的人。

extension IssueRemote {
    private func decodeChannel<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do { return try Self.decoder().decode(type, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的頻道格式看不懂:\(error.localizedDescription)")
        }
    }

    private func channelsURL(_ org: String, _ rest: [String] = [], query: [String: String?] = [:]) -> URL {
        let u = orgURL(org, ["channels"] + rest)
        let items = query.compactMap { k, v in v.flatMap { $0.isEmpty ? nil : URLQueryItem(name: k, value: $0) } }
        guard !items.isEmpty else { return u }
        var c = URLComponents(url: u, resolvingAgainstBaseURL: false)!
        c.queryItems = items.sorted { $0.name < $1.name }
        return c.url!
    }

    /// GET /channels(?issue=):我看得到的頻道
    func listChannels(org: String, issue: String?) throws -> [ChannelSummary] {
        try decodeChannel([ChannelSummary].self, try send("GET", channelsURL(org, query: ["issue": issue]), nil))
    }

    /// POST /channels:建立(綁的 Issue 已有我看得到的頻道時,伺服器回傳那個)
    func createChannel(org: String, name: String, topic: String, issueId: String, members: [String]) throws -> ChannelSummary {
        var b: [String: Any] = ["name": name]
        if !topic.isEmpty { b["topic"] = topic }
        if !issueId.isEmpty { b["issueId"] = issueId }
        if !members.isEmpty { b["members"] = members }
        return try decodeChannel(ChannelSummary.self, try send("POST", channelsURL(org), try json(b)))
    }

    func getChannel(org: String, id: String) throws -> ChannelSummary {
        try decodeChannel(ChannelSummary.self, try send("GET", channelsURL(org, [id]), nil))
    }

    /// POST /channels/{id}:改名稱、主題(只送有給的)
    func updateChannel(org: String, id: String, name: String?, topic: String?) throws -> ChannelSummary {
        var b: [String: Any] = [:]
        if let name { b["name"] = name }
        if let topic { b["topic"] = topic }
        return try decodeChannel(ChannelSummary.self, try send("POST", channelsURL(org, [id]), try json(b)))
    }

    /// POST /channels/{id}/members
    func changeChannelMembers(org: String, id: String, add: [String], remove: [String]) throws -> ChannelSummary {
        var b: [String: Any] = [:]
        if !add.isEmpty { b["add"] = add }
        if !remove.isEmpty { b["remove"] = remove }
        return try decodeChannel(ChannelSummary.self, try send("POST", channelsURL(org, [id, "members"]), try json(b)))
    }

    /// GET /channels/{id}/messages?since=&before=&limit=
    func channelMessages(org: String, id: String, since: String?, before: String?, limit: Int?) throws -> ChannelPage {
        let url = channelsURL(org, [id, "messages"], query: ["since": since, "before": before, "limit": limit.map(String.init)])
        return try decodeChannel(ChannelPage.self, try send("GET", url, nil))
    }

    /// POST /channels/{id}/messages;author 是以角色身分發的(agent:<id>),nil = 登入的人
    func postChannelMessage(org: String, id: String, text: String, author: String?) throws -> ChannelMessage {
        var b: [String: Any] = ["text": text]
        if let author, !author.isEmpty { b["author"] = author }
        return try decodeChannel(ChannelMessage.self, try send("POST", channelsURL(org, [id, "messages"]), try json(b)))
    }

    /// POST /channels/{id}/read;upTo 沒給 = 最新;member 是以角色身分標的(agent:<id>),nil = 登入的人
    func markChannelRead(org: String, id: String, upTo: String?, member: String?) throws -> ChannelRead {
        var b: [String: Any] = [:]
        if let upTo, !upTo.isEmpty { b["upTo"] = upTo }
        if let member, !member.isEmpty { b["member"] = member }
        return try decodeChannel(ChannelRead.self, try send("POST", channelsURL(org, [id, "read"]), try json(b)))
    }

    /// GET /channels/inbox?for=agent:<id>:提到它的未讀訊息與它所在頻道的未讀數
    func channelInbox(org: String, for node: String) throws -> ChannelInbox {
        try decodeChannel(ChannelInbox.self, try send("GET", channelsURL(org, ["inbox"], query: ["for": node]), nil))
    }
}
