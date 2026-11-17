import XCTest
@testable import Omixor

/// 組織樣板(家庭助理)透過暫存的本機守護服務:開關關閉時不列、套用 403;打開後套用、再套用全部略過
final class OrgTemplateTests: XCTestCase {
    override func setUp() async throws {
        TokenVault.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        try TestCore.start(self)
    }

    func testFamilyTemplateFlag() throws {
        let r = IssueRemote(config: IssueServerConfig.current)
        XCTAssertEqual(try r.listOrgTemplates(org: "default").count, 0, "開關預設關,不列")
        // 系統層預設不開放家庭助理:組織開不了(400)、套用 404
        XCTAssertThrowsError(try r.applyOrgTemplate(org: "default", id: "family", scenarios: false)) { e in
            XCTAssertEqual((e as? IssueRemoteError)?.status, 404)
        }
        XCTAssertThrowsError(try r.sendJSON("POST", r.orgURL("default", ["system"]), ["features": ["familyAssistant": true]])) { e in
            XCTAssertEqual((e as? IssueRemoteError)?.status, 400)
        }
        // 伺服器管理員(本機使用者)在系統層開放後:組織層還是預設關(套用 403)
        _ = try r.saveServerFeatures(features: ["familyAssistant": true])
        defer { _ = try? r.saveServerFeatures(features: [:], reset: ["familyAssistant"]) }
        XCTAssertEqual(try r.listOrgTemplates(org: "default").count, 0)
        XCTAssertThrowsError(try r.applyOrgTemplate(org: "default", id: "family", scenarios: false)) { e in
            XCTAssertEqual((e as? IssueRemoteError)?.status, 403)
        }
        _ = try r.sendJSON("POST", r.orgURL("default", ["system"]), ["features": ["familyAssistant": true]])
        let list = try r.listOrgTemplates(org: "default")
        XCTAssertEqual(list.map(\.id), ["family"])
        let pv = try r.previewOrgTemplate(org: "default", id: "family", scenarios: true)
        XCTAssertTrue(pv.canApply)
        XCTAssertTrue(pv.items.allSatisfy { $0.status == "create" })
        let res = try r.applyOrgTemplate(org: "default", id: "family", scenarios: true)
        XCTAssertEqual(res.failed, 0, "\(res.items.filter { $0.status == "failed" })")
        XCTAssertGreaterThan(res.created, 0)
        XCTAssertEqual(res.suggestAutoReply, ["butler"])
        let again = try r.applyOrgTemplate(org: "default", id: "family", scenarios: true)
        XCTAssertEqual(again.created, 0)
        XCTAssertEqual(again.skipped, again.items.count)
    }
}
