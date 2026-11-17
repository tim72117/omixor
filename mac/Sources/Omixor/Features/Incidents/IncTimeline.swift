import SwiftUI

/// 事件時間軸:人和 AI 分開標示,AI 發現特別醒目;底部可新增備註
struct IncTimelineCard: View {
    @EnvironmentObject var app: AppState
    let incident: Incident
    @State private var filter: Filter = .all
    @State private var newestFirst = false
    @State private var draft = ""
    @FocusState private var composerFocused: Bool

    enum Filter: String, CaseIterable, Identifiable {
        case all, people, ai, key
        var id: String { rawValue }
        var label: String {
            switch self {
            case .all: return "全部"
            case .people: return "人員"
            case .ai: return "AI"
            case .key: return "重要節點"
            }
        }
    }

    private var events: [IncidentEvent] {
        let base = incident.incSortedTimeline.filter { e in
            switch filter {
            case .all: return true
            case .people: return e.actor.incIsPerson
            case .ai: return e.actor.incIsAI || e.kind == .aiFinding
            case .key: return e.kind.incIsKey
            }
        }
        return newestFirst ? base.reversed() : base
    }

    var body: some View {
        Card {
            VStack(alignment: .leading, spacing: 12) {
                HStack(spacing: 8) {
                    Text("時間軸").font(.headline)
                    Text("\(incident.timeline.count) 筆").font(.caption).foregroundStyle(.secondary)
                    Spacer()
                    Picker("篩選", selection: $filter) {
                        ForEach(Filter.allCases) { Text($0.label).tag($0) }
                    }
                    .labelsHidden()
                    .fixedSize()
                    Button { withAnimation { newestFirst.toggle() } } label: {
                        Image(systemName: newestFirst ? "arrow.up" : "arrow.down")
                    }
                    .buttonStyle(.borderless)
                    .help(newestFirst ? "目前最新在上" : "目前依時間先後")
                }
                legend

                if events.isEmpty {
                    Text(incident.timeline.isEmpty ? "時間軸還沒有任何紀錄。" : "這個篩選條件下沒有紀錄。")
                        .font(.callout).foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .center)
                        .padding(.vertical, 16)
                } else {
                    VStack(alignment: .leading, spacing: 0) {
                        ForEach(Array(events.enumerated()), id: \.element.id) { idx, e in
                            IncTimelineRow(event: e, incidentStart: incident.startedAt,
                                           isFirst: idx == 0, isLast: idx == events.count - 1,
                                           showDay: idx == 0 || !Calendar.current.isDate(e.date, inSameDayAs: events[idx - 1].date))
                        }
                    }
                }

                composer
            }
        }
    }

    private var legend: some View {
        HStack(spacing: 12) {
            HStack(spacing: 4) {
                Circle().fill(Color.accentColor).frame(width: 7, height: 7)
                Text("人員")
            }
            HStack(spacing: 4) {
                Circle().fill(Color.purple).frame(width: 7, height: 7)
                Text("AI")
            }
            HStack(spacing: 4) {
                Circle().fill(Color.gray).frame(width: 7, height: 7)
                Text("系統")
            }
        }
        .font(.caption2)
        .foregroundStyle(.secondary)
    }

    private var composer: some View {
        HStack(alignment: .bottom, spacing: 8) {
            ActorAvatar(actor: .person(app.me), size: 24)
            TextField("新增備註,例如目前處置或觀察…(⌘↩ 送出)", text: $draft, axis: .vertical)
                .textFieldStyle(.plain)
                .lineLimit(1...5)
                .focused($composerFocused)
                .padding(.horizontal, 10).padding(.vertical, 7)
                .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 8))
                .overlay(RoundedRectangle(cornerRadius: 8)
                    .strokeBorder(composerFocused ? Color.accentColor.opacity(0.6) : Color.primary.opacity(0.12)))
            Button(action: submit) {
                Label("送出", systemImage: "paperplane.fill")
            }
            .buttonStyle(.borderedProminent)
            .keyboardShortcut(.return, modifiers: .command)
            .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }
        .padding(.top, 4)
    }

    private func submit() {
        let t = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { return }
        withAnimation { app.addIncidentNote(id: incident.id, text: t) }
        draft = ""
    }
}

struct IncTimelineRow: View {
    @EnvironmentObject var app: AppState
    let event: IncidentEvent
    let incidentStart: Date
    let isFirst: Bool
    let isLast: Bool
    let showDay: Bool

    private var isAI: Bool { event.actor.incIsAI || event.kind == .aiFinding }
    private var nodeColor: Color { isAI ? .purple : event.kind.incColor }

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            VStack(alignment: .trailing, spacing: 1) {
                if showDay {
                    Text(IncFmt.day(event.date)).font(.caption2).foregroundStyle(.tertiary)
                }
                Text(Fmt.time(event.date)).font(.caption.monospacedDigit().weight(.medium))
                Text(offset).font(.caption2.monospacedDigit()).foregroundStyle(.tertiary)
            }
            .frame(width: 54, alignment: .trailing)
            .padding(.top, 2)

            // 節點與連線
            ZStack(alignment: .top) {
                VStack(spacing: 0) {
                    Rectangle().fill(isFirst ? Color.clear : Color.primary.opacity(0.12)).frame(width: 2, height: 6)
                    Rectangle().fill(isLast ? Color.clear : Color.primary.opacity(0.12)).frame(width: 2)
                }
                Circle()
                    .fill(nodeColor.gradient)
                    .frame(width: 24, height: 24)
                    .overlay(Image(systemName: event.kind.incIcon).font(.system(size: 10, weight: .semibold)).foregroundStyle(.white))
                    .padding(.top, 2)
            }
            .frame(width: 24)
            .frame(maxHeight: .infinity, alignment: .top)

            content
                .padding(.bottom, isLast ? 0 : 12)
        }
        .fixedSize(horizontal: false, vertical: true)
    }

    private var offset: String {
        let d = event.date.timeIntervalSince(incidentStart)
        return d >= 0 ? "+\(IncFmt.short(d))" : "−\(IncFmt.short(-d))"
    }

    @ViewBuilder private var content: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                ActorAvatar(actor: event.actor, size: 16)
                Text(app.name(of: event.actor))
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(isAI ? Color.purple : Color.primary)
                    .lineLimit(1)
                if event.kind == .aiFinding {
                    AIBadge(text: "AI 發現")
                } else {
                    Text(event.kind.incLabel)
                        .font(.caption2.weight(.medium))
                        .foregroundStyle(event.kind.incColor)
                        .padding(.horizontal, 5).padding(.vertical, 1)
                        .background(event.kind.incColor.opacity(0.12), in: Capsule())
                    if event.actor.incIsAI { AIBadge() }
                }
                Spacer(minLength: 0)
            }
            Text(event.text)
                .font(event.kind == .status ? .callout.weight(.medium) : .callout)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(isAI ? 10 : 2)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background {
            if isAI {
                RoundedRectangle(cornerRadius: 8)
                    .fill(Color.purple.opacity(event.kind == .aiFinding ? 0.12 : 0.06))
                    .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.purple.opacity(event.kind == .aiFinding ? 0.35 : 0.15)))
            }
        }
    }
}
