import SwiftUI

/// 選組織:列出我能用的組織
struct OrgPickerView: View {
    @EnvironmentObject var session: Session
    @State private var confirmLogout = false

    var body: some View {
        NavigationStack {
            List {
                if let e = session.orgsError {
                    Section { Label(e, systemImage: "exclamationmark.triangle").foregroundStyle(.orange) }
                }
                Section {
                    if session.orgs.isEmpty {
                        if session.loadingOrgs {
                            HStack { ProgressView(); Text("載入組織…").foregroundStyle(.secondary) }
                        } else {
                            Text("你還沒有加入任何組織。請 omixor 管理員把你加進組織。").foregroundStyle(.secondary)
                        }
                    }
                    ForEach(session.orgs) { o in
                        Button { session.selectOrg(o.id) } label: {
                            HStack {
                                Image(systemName: o.symbol.isEmpty ? "building.2" : o.symbol)
                                    .frame(width: 28).foregroundStyle(.tint)
                                VStack(alignment: .leading) {
                                    Text(o.name).foregroundStyle(.primary)
                                    Text(o.id + (o.isAdmin ? " · 管理員" : " · 成員")).font(.caption).foregroundStyle(.secondary)
                                }
                                Spacer()
                                Image(systemName: "chevron.right").font(.caption).foregroundStyle(.tertiary)
                            }
                        }
                    }
                } header: {
                    Text("選擇組織")
                } footer: {
                    if let u = session.user { Text("已登入 \(u.displayName)(\(session.saved?.server.host ?? ""))") }
                }
            }
            .navigationTitle("組織")
            .refreshable { await session.loadOrgs() }
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("登出") { confirmLogout = true }
                }
            }
            .confirmationDialog("登出這台手機?", isPresented: $confirmLogout, titleVisibility: .visible) {
                Button("登出", role: .destructive) { Task { await session.logout() } }
            } message: {
                Text("會撤銷這台手機的登入,之後要重新配對。")
            }
        }
    }
}
