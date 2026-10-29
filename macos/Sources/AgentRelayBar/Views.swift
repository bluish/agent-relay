import SwiftUI

// The menu window already sits on system glass, so the content stays flat: sections
// split by hairlines, glass only on controls.

// MARK: - Panel

struct PanelView: View {
    @Environment(Store.self) private var store
    @Environment(\.openURL) private var openURL

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
                .padding(.bottom, 10)
            if let error = store.error {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .padding(.bottom, 10)
            }
            if store.tools.isEmpty && store.error == nil {
                ProgressView().controlSize(.small).frame(maxWidth: .infinity, minHeight: 80)
            }
            ForEach(Array(store.tools.enumerated()), id: \.element.id) { i, tool in
                if i > 0 { Divider().padding(.vertical, 12) }
                ToolSection(tool: tool, history: store.history[tool.tool])
            }
            Divider().padding(.top, 12).padding(.bottom, 8)
            footer
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 14)
        .frame(width: 340)
        .onAppear { store.refresh() }
    }

    private var header: some View {
        HStack(spacing: 8) {
            Text("Agents").font(.headline)
            Spacer()
            Button {
                store.refresh(force: true)
            } label: {
                Image(systemName: "arrow.clockwise")
                    .font(.system(size: 12, weight: .semibold))
                    .rotationEffect(.degrees(store.loading ? 360 : 0))
                    .animation(store.loading ? .linear(duration: 1).repeatForever(autoreverses: false) : .default, value: store.loading)
                    .frame(width: 18, height: 18)
            }
            .glassButton()
            .buttonBorderShape(.circle)
            .help("Refresh")
        }
    }

    private var footer: some View {
        HStack(spacing: 10) {
            if let hub = store.hubURL {
                Button {
                    openURL(hub)
                } label: {
                    Label("Dashboard", systemImage: "chart.bar.xaxis")
                }
                .buttonStyle(.borderless)
            }
            Spacer()
            if let at = store.updatedAt {
                Text(at, format: .relative(presentation: .named))
                    .foregroundStyle(.tertiary)
            }
            Menu {
                Toggle("Launch at Login", isOn: Binding(get: { store.launchAtLogin }, set: { store.launchAtLogin = $0 }))
                Divider()
                Button("Quit Agent Relay") { NSApplication.shared.terminate(nil) }
            } label: {
                Image(systemName: "ellipsis.circle")
            }
            .menuStyle(.button)
            .buttonStyle(.borderless)
            .menuIndicator(.hidden)
            .fixedSize()
        }
        .font(.caption)
        .foregroundStyle(.secondary)
    }
}

// MARK: - Tool

struct ToolSection: View {
    let tool: ToolUsage
    let history: HistoryDoc?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .center, spacing: 10) {
                ProviderMark(tool: tool.tool)
                VStack(alignment: .leading, spacing: 1) {
                    Text(tool.title).font(.system(.body, weight: .semibold))
                    if let f = tool.forecast, f.capacityPct > 0 {
                        Outlook(forecast: f)
                    }
                }
                Spacer(minLength: 8)
                if let f = tool.forecast, f.capacityPct > 0 {
                    VStack(alignment: .trailing, spacing: 0) {
                        Text("\(Int(f.poolRemainingPct.rounded()))%")
                            .font(.system(.title3, design: .rounded, weight: .semibold).monospacedDigit())
                            .foregroundStyle(Palette.left(f.leftFraction))
                        Text("left this week")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            VStack(spacing: 12) {
                ForEach(tool.accounts) { account in
                    AccountRow(tool: tool, account: account, days: history?.accounts[account.name], dayNames: history?.days ?? [])
                }
            }
            .padding(.leading, 34)
        }
    }
}

struct ProviderMark: View {
    let tool: String

    var body: some View {
        Group {
            if tool == "claude" {
                Image(nsImage: Logos.claude).resizable()
            } else if tool == "codex" {
                Image(nsImage: Logos.codex).resizable().foregroundStyle(.primary)
            } else {
                Image(systemName: "sparkles").resizable()
            }
        }
        .aspectRatio(contentMode: .fit)
        .frame(width: 22, height: 22)
        .frame(width: 24)
    }
}

struct Outlook: View {
    let forecast: Forecast

    var body: some View {
        Group {
            if let dry = forecast.poolEmptyAt {
                HStack(spacing: 4) {
                    Text("Runs out \(Fmt.when(dry))").foregroundStyle(.orange)
                    if let back = forecast.refillAt {
                        Text("· back \(Fmt.day(back))").foregroundStyle(.secondary)
                    }
                }
            } else if let reset = forecast.nextResetAt {
                Text("Lasts until the reset \(Fmt.day(reset))").foregroundStyle(.secondary)
            } else {
                Text("Plenty left").foregroundStyle(.secondary)
            }
        }
        .font(.caption)
        .lineLimit(1)
        .help(String(format: "Burning %.1f%% of a weekly quota per hour", forecast.burnPctPerHour))
    }
}

// MARK: - Account

struct AccountRow: View {
    @Environment(Store.self) private var store
    let tool: ToolUsage
    let account: Account
    let days: [Double]?
    let dayNames: [String]

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 6) {
                Circle()
                    .fill(account.active ? Color.green : Color.secondary.opacity(0.35))
                    .frame(width: 6, height: 6)
                Text(account.name).font(.callout.weight(.medium))
                if let plan = account.plan, !plan.isEmpty {
                    Text(plan).font(.caption).foregroundStyle(.tertiary)
                }
                Spacer()
                if account.active {
                    Text("In use").font(.caption).foregroundStyle(.secondary)
                } else if store.switching == "\(tool.tool):\(account.name)" {
                    ProgressView().controlSize(.mini)
                } else {
                    Button("Use") { store.use(account.name, for: tool.tool) }
                        .glassButton()
                        .controlSize(.mini)
                        .help("Make \(account.name) the active \(tool.title) account")
                }
            }
            ForEach(account.limits) { limit in
                Meter(limit: limit)
            }
            if account.limits.isEmpty {
                Text("No reading yet").font(.caption2).foregroundStyle(.tertiary)
            }
            if let days, days.filter({ $0 >= 0.005 }).count >= 2 {
                DayBars(values: days, dayNames: dayNames)
            }
            if let error = account.error {
                Text(error).font(.caption2).foregroundStyle(.orange).lineLimit(2)
            }
        }
    }
}

/// One window on one line: name, bar, percent used, time to reset.
struct Meter: View {
    let limit: Limit

    var body: some View {
        HStack(spacing: 8) {
            Text(limit.key == "five_hour" ? "5h" : "Week")
                .font(.caption2)
                .foregroundStyle(.secondary)
                .frame(width: 30, alignment: .leading)
            GeometryReader { geo in
                ZStack(alignment: .leading) {
                    Capsule().fill(.primary.opacity(0.08))
                    Capsule()
                        .fill(Palette.used(limit.used))
                        .frame(width: max(3, geo.size.width * min(1, limit.used)))
                }
            }
            .frame(height: 4)
            Text("\(Int((limit.used * 100).rounded()))%")
                .font(.caption2.monospacedDigit().weight(.medium))
                .frame(width: 30, alignment: .trailing)
            Text(resetText)
                .font(.caption2.monospacedDigit())
                .foregroundStyle(.tertiary)
                .frame(width: 44, alignment: .trailing)
        }
        .help(limit.resetsAt.map { "\(limit.title) resets \(Fmt.when($0))" } ?? "\(limit.title) window not started")
    }

    private var resetText: String {
        guard let r = limit.resetsAt else { return "—" }
        return Fmt.duration(r.timeIntervalSinceNow)
    }
}

/// Share of a weekly quota used per day; a seventh a day is on pace.
struct DayBars: View {
    let values: [Double]
    let dayNames: [String]

    var body: some View {
        let peak = max(2.0 / 7, values.max() ?? 0)
        HStack(alignment: .bottom, spacing: 3) {
            Text("7d").font(.caption2).foregroundStyle(.secondary).frame(width: 30, alignment: .leading)
            ForEach(Array(values.enumerated()), id: \.offset) { i, v in
                RoundedRectangle(cornerRadius: 1.5)
                    .fill(v < 0.005 ? Color.primary.opacity(0.08) : Palette.pace(v))
                    .frame(height: max(2, 14 * v / peak))
                    .frame(maxWidth: .infinity)
                    .help("\(Fmt.weekday(dayNames, i)): \(Int((v * 100).rounded()))% of a weekly quota")
            }
            Spacer().frame(width: 82)
        }
        .frame(height: 14, alignment: .bottom)
    }
}

// MARK: - Menu bar label

struct MenuBarLabel: View {
    @Environment(Store.self) private var store

    var body: some View {
        HStack(spacing: 3) {
            Image(systemName: store.needsAttention ? "gauge.with.dots.needle.100percent" : "gauge.with.dots.needle.50percent")
            if let pct = store.menuBarPercent {
                Text("\(pct)%").monospacedDigit()
            }
        }
    }
}

// MARK: - Helpers

enum Palette {
    static func used(_ u: Double) -> Color { u >= 0.95 ? .red : u >= 0.8 ? .orange : .accentColor }
    static func left(_ f: Double) -> Color { f < 0.1 ? .red : f < 0.25 ? .orange : .primary }
    static func pace(_ v: Double) -> Color { v > 2.0 / 7 ? .red : v > 1.0 / 7 ? .orange : .accentColor }
}

enum Fmt {
    static func duration(_ seconds: TimeInterval) -> String {
        let m = max(0, Int((seconds / 60).rounded()))
        let d = m / 1440, h = (m % 1440) / 60, mm = m % 60
        if d > 0 { return "\(d)d \(h)h" }
        if h > 0 { return "\(h)h \(mm)m" }
        return mm > 0 ? "\(mm)m" : "<1m"
    }

    static func when(_ date: Date) -> String {
        let style: Date.FormatStyle = Calendar.current.isDateInToday(date)
            ? .dateTime.hour().minute()
            : .dateTime.weekday(.abbreviated).hour().minute()
        return date.formatted(style)
    }

    /// "today", "tomorrow" or the weekday, for coarse dates such as refills.
    static func day(_ date: Date) -> String {
        if Calendar.current.isDateInToday(date) { return "today" }
        if Calendar.current.isDateInTomorrow(date) { return "tomorrow" }
        return date.formatted(.dateTime.weekday(.abbreviated))
    }

    static func weekday(_ days: [String], _ i: Int) -> String {
        guard i < days.count, let d = ArlDate.parse(days[i] + "T12:00:00Z") else { return "" }
        return d.formatted(.dateTime.weekday(.wide))
    }
}

// MARK: - Glass

/// Liquid Glass on controls, except in `--snapshot` renders: ImageRenderer draws no
/// glass, so snapshots fall back to bordered buttons to show the layout.
enum GlassMode {
    @MainActor static var plain = false
}

extension View {
    @ViewBuilder
    func glassButton() -> some View {
        if GlassMode.plain { buttonStyle(.bordered) } else { buttonStyle(.glass) }
    }
}
