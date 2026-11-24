import XCTest
@testable import Omixor

/// tools/call 結果的唯一組裝處 MCPResult(規則同 core/mcp/result.go)
final class MCPResultTests: XCTestCase {
    private func parsedText(_ r: [String: Any]) throws -> [String: Any] {
        let t = try XCTUnwrap((r["content"] as? [[String: Any]])?.first?["text"] as? String)
        return try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(t.utf8)) as? [String: Any])
    }

    func testOkHasBothFieldsAndTheyAreEqual() throws {
        let r = MCPResult.ok(["a": 1, "b": ["c": "d"], "list": [1, 2]])
        XCTAssertEqual(r["isError"] as? Bool, false)
        let structured = try XCTUnwrap(r["structuredContent"] as? [String: Any])
        XCTAssertEqual(NSDictionary(dictionary: structured), NSDictionary(dictionary: try parsedText(r)))
        XCTAssertEqual(structured["a"] as? Int, 1)
        XCTAssertEqual((r["content"] as? [[String: Any]])?.first?["type"] as? String, "text")
    }

    func testNonObjectsAreWrappedAndNilIsEmptyObject() throws {
        for v: Any in [[1, 2, 3], "text", 42, true] {
            let r = MCPResult.ok(v)
            let s = try XCTUnwrap(r["structuredContent"] as? [String: Any], "\(v)")
            XCTAssertEqual(Set(s.keys), ["result"])
            XCTAssertEqual(NSDictionary(dictionary: s), NSDictionary(dictionary: try parsedText(r)))
        }
        XCTAssertEqual((MCPResult.ok([1, 2])["structuredContent"] as? [String: Any])?["result"] as? [Int], [1, 2])
        for r in [MCPResult.ok(), MCPResult.ok(nil), MCPResult.ok(NSNull())] {
            XCTAssertEqual((r["structuredContent"] as? [String: Any])?.count, 0)
            XCTAssertEqual((r["content"] as? [[String: Any]])?.first?["text"] as? String, "{}")
        }
    }

    func testErrorShape() throws {
        let r = MCPResult.error(code: "conflict", message: "版本衝突", status: 409, data: ["currentVersion": 3])
        XCTAssertEqual(r["isError"] as? Bool, true)
        let s = try XCTUnwrap(r["structuredContent"] as? [String: Any])
        XCTAssertEqual(Set(s.keys), ["error"])
        let e = try XCTUnwrap(s["error"] as? [String: Any])
        XCTAssertEqual(e["code"] as? String, "conflict")
        XCTAssertEqual(e["message"] as? String, "版本衝突")
        XCTAssertEqual(e["status"] as? Int, 409)
        XCTAssertEqual((e["data"] as? [String: Any])?["currentVersion"] as? Int, 3)
        XCTAssertEqual(NSDictionary(dictionary: s), NSDictionary(dictionary: try parsedText(r)))
        // data 空就不放;code 沒給依狀態碼取
        let plain = try XCTUnwrap(MCPResult.error(message: "找不到", status: 404)["structuredContent"] as? [String: Any])
        let pe = try XCTUnwrap(plain["error"] as? [String: Any])
        XCTAssertEqual(pe["code"] as? String, "not_found")
        XCTAssertNil(pe["data"])
    }

    private func errorBodies(_ r: [String: Any]) throws -> (text: [String: Any], structured: [String: Any]) {
        let s = try XCTUnwrap((r["structuredContent"] as? [String: Any])?["error"] as? [String: Any])
        return (try XCTUnwrap(try parsedText(r)["error"] as? [String: Any]), s)
    }

    func testLargeErrDataOnlyInStructuredContent() throws {
        let current: [String: Any] = ["body": String(repeating: "x", count: 500)]
        let r = MCPResult.error(code: "conflict", message: "版本衝突", status: 409,
                                data: ["currentVersion": 7, "current": current, "big": String(repeating: "y", count: 400)])
        let (text, structured) = try errorBodies(r)
        let sd = try XCTUnwrap(structured["data"] as? [String: Any])
        XCTAssertEqual(Set(sd.keys), ["currentVersion", "current", "big"])
        XCTAssertNil(sd["truncated"])
        let td = try XCTUnwrap(text["data"] as? [String: Any])
        XCTAssertEqual(td as NSDictionary, ["currentVersion": 7, "truncated": true] as NSDictionary)
        XCTAssertEqual(text["code"] as? String, "conflict")
        XCTAssertEqual(text["status"] as? Int, 409)
        // current 就算很小也只放 structuredContent
        let small = try errorBodies(MCPResult.error(message: "x", status: 409, data: ["current": ["a": 1]]))
        XCTAssertEqual((small.text["data"] as? NSDictionary), ["truncated": true])
        XCTAssertNotNil((small.structured["data"] as? [String: Any])?["current"])
    }

    func testSmallErrDataIdenticalAndBoundary() throws {
        let r = MCPResult.error(message: "x", status: 409, data: ["currentVersion": 3, "note": "ok"])
        let (text, structured) = try errorBodies(r)
        XCTAssertEqual(text as NSDictionary, structured as NSDictionary)
        XCTAssertNil((text["data"] as? [String: Any])?["truncated"])
        // 剛好 300 位元組(含引號)不算大,301 才算
        let at = String(repeating: "a", count: MCPResult.errDataValueMax - 2)
        XCTAssertEqual(try errorBodies(MCPResult.error(message: "x", status: 400, data: ["v": at])).text["data"] as? NSDictionary, ["v": at])
        XCTAssertEqual(try errorBodies(MCPResult.error(message: "x", status: 400, data: ["v": at + "a"])).text["data"] as? NSDictionary, ["truncated": true])
    }

    func testUnserializableErrDataFallsBackToBare() throws {
        let r = MCPResult.error(code: "x", message: "壞資料", status: 500, data: ["bad": Double.nan])
        let (text, structured) = try errorBodies(r)
        XCTAssertEqual(text as NSDictionary, structured as NSDictionary)
        XCTAssertEqual(Set(text.keys), ["code", "message", "status"])
        XCTAssertEqual(r["isError"] as? Bool, true)
    }

    func testStatusToCodeTable() {
        let table: [Int: String] = [400: "invalid_argument", 422: "invalid_argument", 401: "unauthorized", 403: "forbidden",
                                    404: "not_found", 409: "conflict", 413: "payload_too_large", 429: "rate_limited",
                                    500: "internal", 418: "request_failed", 405: "request_failed", 502: "internal", 503: "internal"]
        for (status, code) in table { XCTAssertEqual(MCPResult.code(forStatus: status), code, "\(status)") }
    }

    func testSwiftErrorMapping() throws {
        func err(_ e: Error) throws -> [String: Any] {
            try XCTUnwrap(try XCTUnwrap(MCPResult.error(e)["structuredContent"] as? [String: Any])["error"] as? [String: Any])
        }
        let tool = try err(MCPToolError("參數不對"))
        XCTAssertEqual(tool["status"] as? Int, 400)
        XCTAssertEqual(tool["code"] as? String, "invalid_argument")
        XCTAssertEqual(try err(MCPToolError.notFound("找不到 x"))["code"] as? String, "not_found")
        XCTAssertEqual(try err(MCPToolError("x", status: 409, code: "stale", data: ["k": 1]))["code"] as? String, "stale")
        let other = try err(URLError(.timedOut))
        XCTAssertEqual(other["status"] as? Int, 500)
        XCTAssertEqual(other["code"] as? String, "internal")
    }

    /// 唯一組裝處:除了 MCPResult.swift,Sources 裡沒有別處自己拼 structuredContent(外部伺服器的結果原樣放行,不在此列)
    func testNoOtherResultBuilders() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("Sources/Omixor")
        guard let files = FileManager.default.enumerator(at: root, includingPropertiesForKeys: nil) else { throw XCTSkip("沒有 Sources") }
        for case let f as URL in files where f.pathExtension == "swift" && f.lastPathComponent != "MCPResult.swift" {
            let src = try String(contentsOf: f, encoding: .utf8)
            XCTAssertFalse(src.contains(#""structuredContent": "#), "\(f.lastPathComponent) 自己組 structuredContent,請改用 MCPResult")
            XCTAssertFalse(src.contains(#""isError": true"#), "\(f.lastPathComponent) 自己組失敗結果,請改用 MCPResult")
        }
    }
}
