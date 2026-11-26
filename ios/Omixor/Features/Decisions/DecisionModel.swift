import Foundation

// 待決定的顯示文字與回答規則(對應 console 的 decisionModel.ts;伺服器端規則在 core/service/decision.go)。
// 只依賴 API 的型別,沒有畫面,可以直接單元測試。

extension Decision {
    /// 種類標籤:待決定 / 需要協助
    var kindLabel: String { isHelp ? "需要協助" : "待決定" }

    /// 狀態標籤:待處理 / 已決定(help 是已處理)/ 已撤回
    var statusLabel: String {
        switch status {
        case "open": return "待處理"
        case "withdrawn": return "已撤回"
        default: return isHelp ? "已處理" : "已決定"
        }
    }

    /// 回答的顯示文字:選項 id → 選項標籤(other → 其他);help 的 done / declined → 已完成 / 婉拒
    var answerLabel: String {
        if answer.isEmpty { return "" }
        if isHelp { return answer == Decision.answerDone ? "已完成" : answer == Decision.answerDeclined ? "婉拒" : answer }
        if answer == Decision.answerOther { return "其他" }
        return options.first { $0.id == answer }?.label ?? answer
    }

    /// 清單列上的選項摘要:最多 3 個,建議的加註
    var optionSummary: String {
        let shown = options.prefix(3).map { $0.label + ($0.recommended ? "(建議)" : "") }.joined(separator: "、")
        return options.count > 3 ? shown + "…等 \(options.count) 項" : shown
    }

    /// 提出者、處理人的顯示名稱:actor 物件優先,其次相容字串
    static func who(_ actor: ActorView?, _ compat: String) -> String {
        actor.map(\.displayName).flatMap(\.nonEmpty) ?? compat.nonEmpty ?? "—"
    }
    var createdByName: String { Decision.who(createdByActor, createdBy) }
    var decidedByName: String { Decision.who(decidedByActor, decidedBy) }
}

/// 回答表單的內容(送出前的草稿)。choice:decision 是選項 id 或 other;help 是 done 或 declined
struct DecisionDraft: Equatable {
    static let maxText = 20_000
    var choice = ""
    var note = ""
    var evidence = ""

    /// 這個選擇要不要寫說明(other、declined 必填)
    var needsNote: Bool { choice == Decision.answerOther || choice == Decision.answerDeclined }

    /// 可以選的答案:help 是完成 / 婉拒;decision 是各選項加上「其他」
    static func choices(for d: Decision) -> [(id: String, label: String)] {
        if d.isHelp { return [(Decision.answerDone, "已完成"), (Decision.answerDeclined, "婉拒")] }
        return d.options.map { ($0.id, $0.label + ($0.recommended ? "(建議)" : "")) } + [(Decision.answerOther, "其他(要寫說明)")]
    }

    /// 不能送出的原因;空字串 = 可以送出
    func problem(for d: Decision) -> String {
        if choice.isEmpty { return d.isHelp ? "請選擇完成或婉拒" : "請選一個選項,或選「其他」" }
        if !d.isHelp, choice != Decision.answerOther, !d.options.contains(where: { $0.id == choice }) { return "沒有這個選項" }
        if d.isHelp, choice != Decision.answerDone, choice != Decision.answerDeclined { return "回答只能是完成或婉拒" }
        if needsNote, note.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return choice == Decision.answerOther ? "選「其他」要寫說明" : "婉拒要寫原因"
        }
        if note.count > Self.maxText || evidence.count > Self.maxText { return "文字最多 \(Self.maxText) 字" }
        return ""
    }

    /// 送給 API 的 note、evidence:去頭尾空白;evidence 只有 help 完成時才帶(伺服器對 decision 帶 evidence 回 400、declined 會清掉)
    func payload(for d: Decision) -> (note: String, evidence: String) {
        let ev = d.isHelp && choice == Decision.answerDone ? evidence.trimmingCharacters(in: .whitespacesAndNewlines) : ""
        return (note.trimmingCharacters(in: .whitespacesAndNewlines), ev)
    }
}

/// 把答案錯誤整理成人看得懂的訊息(同 console 的 errorMessage)
enum DecisionErrorText {
    static func message(_ e: Error) -> String {
        let status = (e as? APIError)?.status ?? 0
        let msg = e.localizedDescription.trimmingCharacters(in: .whitespacesAndNewlines)
        let tail = msg.isEmpty ? "" : "(\(msg))"
        switch status {
        case 403: return "沒有權限做這個動作" + tail
        case 404: return "找不到這筆待決定,可能已被移除或你看不到它" + tail
        case 409: return "狀態已經變了(別人可能剛處理過),已重新讀取" + tail
        default: return msg.isEmpty ? "操作失敗" : msg
        }
    }
}
