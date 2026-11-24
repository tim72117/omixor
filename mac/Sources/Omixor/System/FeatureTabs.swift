import Foundation

// 各頁的分頁對應到功能清單(core/model/features.go)的「頁面.分頁」key;關掉的分頁不顯示(見 AppState.visibleTabs)

extension DevTab: FeatureTab {
    static var section: AppSection { .development }
    var featureTitle: String { label }
}

extension CICDTab: FeatureTab {
    static var section: AppSection { .pipelines }
    var featureTitle: String { label }
}

extension InfraTab: FeatureTab {
    static var section: AppSection { .infrastructure }
    var featureTitle: String { label }
}

extension MonTab: FeatureTab {
    static var section: AppSection { .monitoring }
    var featureTitle: String { title }
}

extension IncidentsView.IncTab: FeatureTab {
    static var section: AppSection { .incidents }
    /// 事件列表的 key 是 incidents.list(不是 incidents.incidents)
    var featureKey: String { self == .incidents ? "incidents.list" : "incidents.\(rawValue)" }
    var featureTitle: String { label }
}

extension AutoTab: FeatureTab {
    static var section: AppSection { .automation }
    /// 需要處理、執行日誌沒有自己的功能開關,跟派發(同 console)
    var featureKey: String { "automation.\(self == .help || self == .log ? AutoTab.dispatch.rawValue : rawValue)" }
    var featureTitle: String { label }
}
