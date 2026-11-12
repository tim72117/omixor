import Foundation

// MCP 的訊息頻道工具(名稱與參數同核心):ops_channels、ops_channel_create、ops_channel_messages、ops_channel_post,
// 以及 ops_agent_context 的 mentions。帶 agent 時以該角色身分(角色要存在;看、發都要是頻道成員),
// 沒帶時是 app 的使用者(本機等同組織管理員;遠端是登入的人)。規則見 Channels/Channels.swift。

extension MCPHandler {
    static let channelTools: [Tool] = [
        Tool(name: "ops_channels", title: "列出對話頻道",
             description: "列出我(或 agent 角色)看得到的訊息頻道:id、名稱、主題、綁的 Issue、成員(node 與顯示名稱)、未讀數與最後一則訊息的時間。頻道把人與虛擬角色拉進同一個對話;@ 角色只通知、不排任務。給 issue 只找綁該 Issue 的頻道。",
             properties: [
                 "agent": prop("string", "以角色身分查看時填角色 id 或名稱(只列它是成員的頻道,未讀數是它的)"),
                 "issue": prop("string", "只找綁這個 Issue 的頻道,例如 ISS-3"),
             ], required: [], readOnly: true),
        Tool(name: "ops_channel_create", title: "建立對話頻道",
             description: "建立訊息頻道(角色身分不能建)。建立者自動是成員;members 可以給 agent:<角色 id 或名稱>、user:<email>、純 email、純角色 id 或名稱。給 issue 會綁該 Issue:同一個 Issue 只能有一個頻道,已有而且看得到時直接回傳那個(created = false)。",
             properties: [
                 "name": prop("string", "頻道名稱(最多 80 字)"),
                 "topic": prop("string", "主題(最多 500 字)"),
                 "issue": prop("string", "綁的 Issue id(選填)"),
                 "members": strings("成員,例如 [\"agent:marketing\", \"pm@example.com\"]"),
             ], required: ["name"]),
        Tool(name: "ops_channel_messages", title: "讀頻道訊息",
             description: "讀頻道訊息(依時間由舊到新)。給 since(訊息 id M-n 或時間)回傳之後最早的 limit 筆;沒給回傳最新的 limit 筆。hasMore 表示還有更多。讀了算已讀(標到回傳的最後一則)。帶 agent 時以該角色身分讀(要是頻道成員)。",
             properties: [
                 "channel": prop("string", "頻道 id(CH-n)或名稱"),
                 "since": prop("string", "訊息 id(M-n)或 ISO 8601 時間"),
                 "limit": prop("integer", "最多幾筆,預設 50、最多 200"),
                 "agent": prop("string", "以角色身分讀時填角色 id 或名稱"),
             ], required: ["channel"]),
        Tool(name: "ops_channel_post", title: "在頻道發訊息",
             description: "在頻道發一則訊息(Markdown,最多 8000 字;要是頻道成員)。用 @角色名稱、@角色 id 或 @email 提到頻道成員:只通知對方(角色下次執行時在 ops_agent_context 的 mentions 看得到),不會排任務。帶 agent 時以該角色署名(角色要是頻道成員)。",
             properties: [
                 "channel": prop("string", "頻道 id(CH-n)或名稱"),
                 "text": prop("string", "訊息內容(Markdown)"),
                 "agent": prop("string", "以角色身分發時填角色 id 或名稱"),
             ], required: ["channel", "text"]),
    ]

    private var channelStore: ChannelStore { app.startChannels() }

    /// agent 參數 → agent:<角色 id>;沒給 = nil(app 的使用者)
    private func channelActor(_ a: MCPArgs) throws -> String? {
        guard let name = a.string("agent"), !name.isEmpty else { return nil }
        guard let g = app.startAgents().agent(name) else { throw MCPToolError("找不到角色 \(name)") }
        return ChainNode.agent(g.id)
    }

    private func channelCall<T>(_ f: () throws -> T) throws -> T {
        do { return try f() } catch let e as MCPToolError { throw e } catch { throw MCPToolError(error.localizedDescription) }
    }

    private func channelJSON(_ s: ChannelSummary) -> [String: Any] {
        let store = channelStore
        let c = s.channel
        return ["id": c.id, "name": c.name, "topic": c.topic, "issueId": c.issueId,
                "members": c.members.map { ["node": $0, "name": store.name(of: $0)] },
                "unread": s.unread, "lastMessageAt": s.lastMessageAt.map { MCPJSON.iso($0) as Any } ?? NSNull()]
    }

    private func channelMessageJSON(_ m: ChannelMessage) -> [String: Any] {
        ["id": m.id, "author": m.author, "authorName": channelStore.name(of: m.author), "text": m.text,
         "mentions": m.mentions, "at": MCPJSON.iso(m.at)]
    }

    func channelsList(_ a: MCPArgs) throws -> Any {
        let actor = try channelActor(a)
        let list = try channelCall { try channelStore.list(issue: a.string("issue").flatMap { $0.isEmpty ? nil : $0 }, as: actor) }
        return ["channels": list.map(channelJSON)]
    }

    func channelCreate(_ a: MCPArgs) throws -> Any {
        let name = try a.require("name")
        let r = try channelCall {
            try channelStore.create(name: name, topic: a.string("topic") ?? "", issueId: a.string("issue") ?? "",
                                    members: a.strings("members") ?? [], via: .mcp)
        }
        var out = channelJSON(r.channel)
        out["created"] = r.created
        return out
    }

    func channelMessages(_ a: MCPArgs) throws -> Any {
        let ref = try a.require("channel")
        let actor = try channelActor(a)
        let store = channelStore
        return try channelCall {
            let c = try store.channel(ref, as: actor)
            let page = try store.messages(c.id, since: a.string("since").flatMap { $0.isEmpty ? nil : $0 }, limit: a.int("limit"), as: actor)
            if let last = page.messages.last { _ = try? store.markRead(c.id, upTo: last.id, as: actor) }
            return [
                "channel": ["id": c.id, "name": c.name, "topic": c.channel.topic, "issueId": c.channel.issueId],
                "messages": page.messages.map(channelMessageJSON),
                "hasMore": page.hasMore,
            ] as [String: Any]
        }
    }

    func channelPost(_ a: MCPArgs) throws -> Any {
        let ref = try a.require("channel")
        guard let text = a.raw["text"] as? String, !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw MCPToolError("訊息不能是空的")
        }
        let actor = try channelActor(a)
        let store = channelStore
        return try channelCall {
            let c = try store.channel(ref, as: actor)
            let m = try store.post(c.id, text: text, as: actor, via: actor.flatMap(ChainNode.agentId).map { Actor.ai($0) } ?? .mcp)
            var out = channelMessageJSON(m)
            out["channelId"] = m.channelId
            return out
        }
    }

    /// ops_agent_context 的 mentions:提到這個角色的未讀訊息(最多 20 則)與它所在頻道的未讀數
    func channelMentions(_ g: Agent) -> [String: Any] {
        let store = channelStore
        do {
            let b = try store.inbox(for: ChainNode.agent(g.id))
            return [
                "messages": b.mentions.map { m -> [String: Any] in
                    ["channelId": m.channelId, "channelName": m.channelName, "id": m.id, "author": m.author,
                     "authorName": store.name(of: m.author), "text": m.text, "at": MCPJSON.iso(m.at)]
                },
                "channels": b.channels.map { ["id": $0.id, "name": $0.name, "unread": $0.unread] as [String: Any] },
            ]
        } catch {
            return ["messages": [Any](), "channels": [Any](), "error": error.localizedDescription]
        }
    }
}
