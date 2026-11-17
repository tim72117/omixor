import Foundation

// 寬鬆解碼:讀 core server(Go)的回應時,單一欄位異常不要讓整份列表解不開。
//   陣列:null 或缺 → [];陣列裡壞掉的那一筆略過(NSLog),其他照讀
//   字串、數字、布林:缺、null 或型別不對 → 給的預設值
//   列舉(String rawValue):不認得的值 → 給的預設值(Issue 的 status → open、priority → normal,同 web 的 normIssue)
//   必填 id 缺:該筆的 init(from:) 照樣丟錯,放在列表裡時由 LossyList 略過那一筆
// Go 的 nil slice 會送 null、omitempty 的欄位會省略,所以各型別的 init(from:) 都用這裡的 helper。

/// 解碼成功的元素;失敗的那筆略過並記到 NSLog。值本身是 null(Go 的 nil slice)時是空陣列
struct LossyList<Element: Decodable>: Decodable {
    var elements: [Element]

    init(_ elements: [Element] = []) { self.elements = elements }

    /// 略過一個元素用(什麼都不讀,解碼一定成功,讓 unkeyed container 前進到下一筆)
    private struct Skip: Decodable { init(from decoder: Decoder) throws {} }

    init(from decoder: Decoder) throws {
        if let s = try? decoder.singleValueContainer(), s.decodeNil() { elements = []; return }
        var c = try decoder.unkeyedContainer()
        var out: [Element] = []
        if let n = c.count { out.reserveCapacity(n) }
        while !c.isAtEnd {
            let index = c.currentIndex
            do {
                out.append(try c.decode(Element.self))
            } catch {
                NSLog("寬鬆解碼:略過第 %d 筆 %@:%@", index, String(describing: Element.self), Self.describe(error))
                // 解碼失敗時 index 不會前進,用 Skip 跳過這一筆
                if (try? c.decode(Skip.self)) == nil { break }
            }
        }
        elements = out
    }

    static func describe(_ error: Error) -> String {
        switch error {
        case DecodingError.keyNotFound(let k, _): return "缺少 \(k.stringValue)"
        case DecodingError.valueNotFound(_, let ctx), DecodingError.typeMismatch(_, let ctx), DecodingError.dataCorrupted(let ctx):
            let path = ctx.codingPath.map(\.stringValue).joined(separator: ".")
            return path.isEmpty ? ctx.debugDescription : "\(path):\(ctx.debugDescription)"
        default: return error.localizedDescription
        }
    }
}

extension LossyList: Equatable where Element: Equatable {}
extension LossyList: Hashable where Element: Hashable {}

extension JSONDecoder {
    /// 解碼一份列表:null → [],壞掉的那筆略過(見 LossyList)
    func decodeLossyList<T: Decodable>(_ type: T.Type, from data: Data) throws -> [T] {
        try decode(LossyList<T>.self, from: data).elements
    }
}

extension KeyedDecodingContainer {
    /// 缺、null 或型別不對 → def
    func lossy<T: Decodable>(_ key: Key, default def: @autoclosure () -> T) -> T {
        ((try? decodeIfPresent(T.self, forKey: key)) ?? nil) ?? def()
    }

    /// 選填:缺、null 或型別不對 → nil
    func lossyOptional<T: Decodable>(_ type: T.Type, _ key: Key) -> T? {
        (try? decodeIfPresent(T.self, forKey: key)) ?? nil
    }

    /// 陣列:缺、null 或不是陣列 → [];壞掉的元素略過
    func lossyArray<T: Decodable>(_ type: T.Type, _ key: Key) -> [T] {
        ((try? decodeIfPresent(LossyList<T>.self, forKey: key)) ?? nil)?.elements ?? []
    }

    /// 字典(值是 T):缺、null → [:];解不開的值略過(例如不認得的列舉)
    func lossyDictionary<T: Decodable>(_ type: T.Type, _ key: Key) -> [String: T] {
        guard let raw = (try? decodeIfPresent([String: LossyValue<T>].self, forKey: key)) ?? nil else { return [:] }
        return raw.compactMapValues(\.value)
    }

    /// 列舉(String rawValue):缺、null、型別不對或不認得的值 → def
    func lossyEnum<E: RawRepresentable>(_ key: Key, default def: E) -> E where E.RawValue == String {
        lossyOptional(String.self, key).flatMap(E.init(rawValue:)) ?? def
    }
}

/// 解得開就有值,解不開是 nil(字典的值用)
struct LossyValue<T: Decodable>: Decodable {
    var value: T?
    init(from decoder: Decoder) throws { value = try? T(from: decoder) }
}
