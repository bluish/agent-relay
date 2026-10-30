import SwiftUI

// Cards on the menu window's own material: a hero figure per tool, then its
// accounts. Glass only on the refresh control.

// MARK: - Panel

struct PanelView: View {
    @Environment(Store.self) private var store
    @Environment(\.openURL) private var openURL

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            header
            if let error = store.error {
                Card {
                    Label(error, systemImage: "exclamationmark.triangle.fill")
                        .font(.system(size: 11))
                        .foregroundStyle(Theme.accent)
                }
            }
            if store.tools.isEmpty && store.error == nil {
                Card { ProgressView().controlSize(.small).frame(maxWidth: .infinity, minHeight: 80) }
            }
            ForEach(store.tools) { tool in
                ToolCard(tool: tool, history: store.history[tool.tool])
            }
            footer
        }
        .padding(12)
        .frame(width: Theme.width)
        .onAppear { store.refresh() }
    }

    private var header: some View {
        PanelHeader(icon: "gauge.with.dots.needle.50percent", title: "Agents") {
            if let at = store.updatedAt {
                Text(at, format: .relative(presentation: .named))
                    .font(.system(size: 11))
                    .foregroundStyle(.tertiary)
            }
            Button {
                store.refresh(force: true)
            } label: {
                Image(systemName: "arrow.clockwise")
                    .font(.system(size: 11, weight: .semibold))
                    .rotationEffect(.degrees(store.loading ? 360 : 0))
                    .animation(store.loading ? .linear(duration: 1).repeatForever(autoreverses: false) : .default, value: store.loading)
                    .frame(width: 16, height: 16)
            }
            .glassButton()
            .buttonBorderShape(.circle)
            .help("Refresh")
        }
    }

    private var footer: some View {
        HStack(spacing: 14) {
            if let hub = store.hubURL {
                Button {
                    openURL(hub)
                } label: {
                    Label("Dashboard", systemImage: "chart.bar.xaxis")
                }
                .buttonStyle(FooterButtonStyle())
            }
            Spacer()
            Menu {
                Toggle("Launch at Login", isOn: Binding(get: { store.launchAtLogin }, set: { store.launchAtLogin = $0 }))
                Divider()
                Button("Quit Agent Relay") { NSApplication.shared.terminate(nil) }
            } label: {
                Image(systemName: "gearshape")
            }
            .menuStyle(.button)
            .buttonStyle(.borderless)
            .menuIndicator(.hidden)
            .fixedSize()
            .foregroundStyle(.secondary)
        }
        .font(.system(size: 12))
        .padding(.horizontal, 4)
        .padding(.top, 2)
    }
}

// MARK: - Tool

struct ToolCard: View {
    let tool: ToolUsage
    let history: HistoryDoc?

    var body: some View {
        Card {
            HStack(spacing: 8) {
                ProviderMark(tool: tool.tool)
                Text(tool.title).font(.system(size: 13, weight: .semibold))
                Spacer()
                if let active = tool.active {
                    Text(active).font(.system(size: 11)).foregroundStyle(.secondary)
                }
            }
            if let f = tool.forecast, f.capacityPct > 0 {
                HeroFigure(value: "\(Int(f.poolRemainingPct.rounded()))%", unit: "left this week")
                CapsuleMeter(fraction: f.leftFraction, color: Palette.leftBar(f.leftFraction))
                Outlook(forecast: f)
            }
            Divider().padding(.vertical, 2)
            VStack(spacing: 2) {
                ForEach(tool.accounts) { account in
                    AccountRow(tool: tool, account: account, days: history?.accounts[account.name], dayNames: history?.days ?? [])
                }
            }
            .padding(.horizontal, -8)
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
        .frame(width: 18, height: 18)
    }
}

struct Outlook: View {
    let forecast: Forecast

    var body: some View {
        Group {
            if let dry = forecast.poolEmptyAt {
                HStack(spacing: 4) {
                    Text("Runs out \(Fmt.when(dry))").foregroundStyle(Theme.accent)
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
    @State private var hovering = false

    private var switching: Bool { store.switching == "\(tool.tool):\(account.name)" }

    var body: some View {
        // Like the Wi-Fi menu: an inactive account is a row you click to switch to.
        Button {
            if !account.active && store.switching == nil {
                store.use(account.name, for: tool.tool)
            }
        } label: {
            content
                .padding(.horizontal, 8)
                .padding(.vertical, 6)
                .background {
                    RoundedRectangle(cornerRadius: 8, style: .continuous)
                        .fill(.primary.opacity(hovering && !account.active ? 0.07 : 0))
                }
                .contentShape(.rect(cornerRadius: 8))
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .animation(.easeOut(duration: 0.12), value: hovering)
        .help(account.active ? "\(account.name) is the active \(tool.title) account" : "Switch \(tool.title) to \(account.name)")
    }

    private var content: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 6) {
                Text(account.name).font(.callout.weight(.medium))
                if let plan = account.plan, !plan.isEmpty {
                    Text(plan).font(.caption).foregroundStyle(.tertiary)
                }
                Spacer()
                trailing
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

    @ViewBuilder
    private var trailing: some View {
        if account.active {
            Image(systemName: "checkmark.circle.fill")
                .symbolRenderingMode(.palette)
                .foregroundStyle(.white, Theme.accent)
                .font(.system(size: 14))
        } else if switching {
            ProgressView().controlSize(.mini)
        } else if hovering {
            Label("Switch", systemImage: "arrow.left.arrow.right")
                .font(.caption.weight(.medium))
                .foregroundStyle(Theme.accent)
                .transition(.opacity)
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
    static func used(_ u: Double) -> Color { u >= 0.95 ? Theme.bad : u >= 0.8 ? .orange : Theme.accent }
    static func leftBar(_ f: Double) -> Color { f < 0.1 ? Theme.bad : f < 0.25 ? .orange : Theme.accent }
    static func pace(_ v: Double) -> Color { v > 2.0 / 7 ? Theme.bad : v > 1.0 / 7 ? .orange : Theme.accent }
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
