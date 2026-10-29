import SwiftUI

// MARK: - Panel

struct PanelView: View {
    @Environment(Store.self) private var store
    @Environment(\.openURL) private var openURL

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            header
            if let error = store.error {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout)
                    .foregroundStyle(.orange)
                    .padding(10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .glass(.regular.tint(.orange.opacity(0.15)), in: .rect(cornerRadius: 14))
            }
            if store.tools.isEmpty && store.error == nil {
                ProgressView().frame(maxWidth: .infinity, minHeight: 120)
            }
            GlassGroup {
                VStack(spacing: 12) {
                    ForEach(store.tools) { tool in
                        ToolCard(tool: tool, history: store.history[tool.tool])
                    }
                }
            }
        }
        .padding(16)
        .frame(width: 380)
        .onAppear { store.refresh() }
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Agents").font(.title2.weight(.semibold))
                Group {
                    if let at = store.updatedAt {
                        Text("Updated \(at, format: .relative(presentation: .named))")
                    } else {
                        Text("Loading…")
                    }
                }
                .font(.caption)
                .foregroundStyle(.secondary)
            }
            Spacer()
            HStack(spacing: 8) {
                Button {
                    store.refresh(force: true)
                } label: {
                    Image(systemName: "arrow.clockwise")
                        .rotationEffect(.degrees(store.loading ? 360 : 0))
                        .animation(store.loading ? .linear(duration: 1).repeatForever(autoreverses: false) : .default, value: store.loading)
                }
                .help("Refresh (reads the active accounts live)")
                Menu {
                    if let hub = store.hubURL {
                        Button("Open Dashboard", systemImage: "chart.bar.xaxis") { openURL(hub) }
                    }
                    Toggle("Launch at Login", isOn: Binding(get: { store.launchAtLogin }, set: { store.launchAtLogin = $0 }))
                    Divider()
                    Button("Quit Agent Relay", systemImage: "power") { NSApplication.shared.terminate(nil) }
                } label: {
                    Image(systemName: "ellipsis")
                }
                .menuIndicator(.hidden)
            }
            .glassButton()
            .buttonBorderShape(.circle)
            .controlSize(.large)
        }
    }
}

// MARK: - Tool

struct ToolCard: View {
    let tool: ToolUsage
    let history: HistoryDoc?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 10) {
                Image(systemName: tool.symbol)
                    .font(.headline)
                    .frame(width: 32, height: 32)
                    .glass(.regular.tint(.accentColor.opacity(0.35)), in: .circle)
                VStack(alignment: .leading, spacing: 1) {
                    Text(tool.title).font(.headline)
                    if let active = tool.active {
                        Text("on \(active)").font(.caption).foregroundStyle(.secondary)
                    }
                }
                Spacer()
                if let f = tool.forecast, f.capacityPct > 0 {
                    VStack(alignment: .trailing, spacing: 0) {
                        Text("\(Int(f.poolRemainingPct.rounded()))%")
                            .font(.title2.weight(.semibold).monospacedDigit())
                            .foregroundStyle(Palette.left(f.leftFraction))
                        Text(f.capacityPct > 100 ? "of \(Int(f.capacityPct))% left" : "weekly left")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            if let f = tool.forecast, f.capacityPct > 0 {
                OutlookLine(forecast: f)
            }
            ForEach(tool.accounts) { account in
                if tool.accounts.count > 1 || account.id != tool.accounts.first?.id {
                    Divider().opacity(0.4)
                }
                AccountView(tool: tool, account: account, days: history?.accounts[account.name], dayNames: history?.days ?? [])
            }
        }
        .padding(14)
        .glass(.regular, in: .rect(cornerRadius: 20))
    }
}

struct OutlookLine: View {
    let forecast: Forecast

    var body: some View {
        HStack(spacing: 6) {
            if let dry = forecast.poolEmptyAt {
                Image(systemName: "hourglass.bottomhalf.filled").foregroundStyle(.orange)
                Text("Runs dry \(Fmt.when(dry))")
                    .foregroundStyle(.orange)
                if let back = forecast.refillAt {
                    Text("· back \(Fmt.when(back))").foregroundStyle(.secondary)
                }
            } else {
                Image(systemName: "checkmark.seal.fill").foregroundStyle(.green)
                Text("Lasts until the reset").foregroundStyle(.green)
                if let reset = forecast.nextResetAt {
                    Text("· \(Fmt.when(reset))").foregroundStyle(.secondary)
                }
            }
            Spacer()
            Text(String(format: "%.1f%%/h", forecast.burnPctPerHour))
                .foregroundStyle(.secondary)
                .help(forecast.burnSource == "history" ? "Burn rate over the last 3 hours" : "Burn rate (weekly average)")
        }
        .font(.caption)
    }
}

// MARK: - Account

struct AccountView: View {
    @Environment(Store.self) private var store
    let tool: ToolUsage
    let account: Account
    let days: [Double]?
    let dayNames: [String]

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Text(account.name).font(.subheadline.weight(.semibold))
                if let plan = account.plan, !plan.isEmpty {
                    Text(plan).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                if account.active {
                    Text("Active")
                        .font(.caption.weight(.semibold))
                        .padding(.horizontal, 10)
                        .padding(.vertical, 3)
                        .glass(.regular.tint(.green.opacity(0.35)), in: .capsule)
                } else if store.switching == "\(tool.tool):\(account.name)" {
                    ProgressView().controlSize(.small)
                } else {
                    Button("Use") { store.use(account.name, for: tool.tool) }
                        .glassButton()
                        .controlSize(.small)
                        .help("arl switch \(tool.tool) \(account.name)")
                }
            }
            ForEach(account.limits) { limit in
                MeterRow(limit: limit)
            }
            if account.limits.isEmpty {
                Text("No usage reading yet").font(.caption).foregroundStyle(.secondary)
            }
            if let days, days.filter({ $0 >= 0.005 }).count >= 2 {
                DayBars(values: days, dayNames: dayNames)
            }
            if let error = account.error {
                Label(error, systemImage: "exclamationmark.circle").font(.caption2).foregroundStyle(.orange)
            }
        }
    }
}

struct MeterRow: View {
    let limit: Limit

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(limit.title).font(.caption).foregroundStyle(.secondary)
                Spacer()
                Text(resetText).font(.caption2).foregroundStyle(.tertiary)
                Text("\(Int((limit.used * 100).rounded()))%")
                    .font(.caption.weight(.semibold).monospacedDigit())
                    .foregroundStyle(Palette.used(limit.used))
                    .frame(width: 38, alignment: .trailing)
            }
            GeometryReader { geo in
                ZStack(alignment: .leading) {
                    Capsule().fill(.quaternary)
                    Capsule()
                        .fill(Palette.used(limit.used).gradient)
                        .frame(width: max(4, geo.size.width * min(1, limit.used)))
                }
            }
            .frame(height: 6)
        }
    }

    private var resetText: String {
        guard let r = limit.resetsAt else { return "not started" }
        return "resets in \(Fmt.duration(r.timeIntervalSinceNow))"
    }
}

/// Share of a weekly quota used per day; a seventh a day is on pace.
struct DayBars: View {
    let values: [Double]
    let dayNames: [String]

    var body: some View {
        let peak = max(2.0 / 7, values.max() ?? 0)
        HStack(alignment: .bottom, spacing: 6) {
            ForEach(Array(values.enumerated()), id: \.offset) { i, v in
                VStack(spacing: 3) {
                    RoundedRectangle(cornerRadius: 3)
                        .fill(v < 0.005 ? AnyShapeStyle(.quaternary) : AnyShapeStyle(Palette.pace(v).gradient))
                        .frame(height: max(2, 26 * v / peak))
                    Text(Fmt.weekday(dayNames, i)).font(.system(size: 9)).foregroundStyle(.tertiary)
                }
                .frame(maxWidth: .infinity)
                .help("\(Int((v * 100).rounded()))% of a weekly quota")
            }
        }
        .frame(height: 40, alignment: .bottom)
    }
}

// MARK: - Menu bar label

struct MenuBarLabel: View {
    @Environment(Store.self) private var store

    var body: some View {
        HStack(spacing: 4) {
            Image(systemName: store.needsAttention ? "gauge.with.dots.needle.100percent" : "gauge.with.dots.needle.33percent")
            if let pct = store.menuBarPercent {
                Text("\(pct)%").monospacedDigit()
            }
        }
    }
}

// MARK: - Helpers

enum Palette {
    static func used(_ u: Double) -> Color { u >= 0.95 ? .red : u >= 0.8 ? .orange : .green }
    static func left(_ f: Double) -> Color { f < 0.1 ? .red : f < 0.25 ? .orange : .green }
    static func pace(_ v: Double) -> Color { v > 2.0 / 7 ? .red : v > 1.0 / 7 ? .orange : .green }
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

    static func weekday(_ days: [String], _ i: Int) -> String {
        guard i < days.count, let d = ArlDate.parse(days[i] + "T12:00:00Z") else { return "" }
        return String(d.formatted(.dateTime.weekday(.narrow)))
    }
}

// MARK: - Glass

/// Liquid Glass, except in `--snapshot` renders: ImageRenderer draws no glass, so
/// snapshots fall back to plain materials to show the layout.
enum GlassMode {
    @MainActor static var plain = false
}

extension View {
    @ViewBuilder
    func glass(_ glass: Glass = .regular, in shape: some Shape) -> some View {
        if GlassMode.plain {
            background(.white.opacity(0.07), in: shape)
        } else {
            glassEffect(glass, in: shape)
        }
    }

    @ViewBuilder
    func glassButton() -> some View {
        if GlassMode.plain { buttonStyle(.bordered) } else { buttonStyle(.glass) }
    }
}

struct GlassGroup<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        if GlassMode.plain {
            content
        } else {
            GlassEffectContainer(spacing: 12) { content }
        }
    }
}
