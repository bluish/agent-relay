import Foundation

// Mirrors `arl usage --json` and `arl history --json`.

struct UsageDoc: Decodable {
    let generatedAt: Date?
    let tools: [ToolUsage]

    enum CodingKeys: String, CodingKey {
        case generatedAt = "generated_at"
        case tools
    }
}

struct ToolUsage: Decodable, Identifiable {
    let tool: String
    let active: String?
    let forecast: Forecast?
    let accounts: [Account]

    var id: String { tool }
    var title: String { tool == "claude" ? "Claude" : tool == "codex" ? "Codex" : tool.capitalized }
    var symbol: String { tool == "claude" ? "sparkle" : "chevron.left.forwardslash.chevron.right" }
}

struct Forecast: Decodable {
    let poolRemainingPct: Double
    let capacityPct: Double
    let burnPctPerHour: Double
    let burnSource: String
    let poolEmptyAt: Date?
    let refillAt: Date?
    let nextResetAt: Date?
    let nextResetGainPct: Double?

    enum CodingKeys: String, CodingKey {
        case poolRemainingPct = "pool_remaining_pct"
        case capacityPct = "capacity_pct"
        case burnPctPerHour = "burn_pct_per_hour"
        case burnSource = "burn_source"
        case poolEmptyAt = "pool_empty_at"
        case refillAt = "refill_at"
        case nextResetAt = "next_reset_at"
        case nextResetGainPct = "next_reset_gain_pct"
    }

    /// Fraction of the pooled weekly quota left (0...1).
    var leftFraction: Double { capacityPct > 0 ? poolRemainingPct / capacityPct : 0 }
}

struct Account: Decodable, Identifiable {
    let name: String
    let active: Bool
    let plan: String?
    let fetchedAt: Date?
    let error: String?
    let limits: [Limit]

    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name, active, plan, error, limits
        case fetchedAt = "fetched_at"
    }

    func limit(_ key: String) -> Limit? { limits.first { $0.key == key } }

    /// Whether any window is spent (at or over `threshold`).
    func atLimit(_ threshold: Double = 0.999) -> Bool { limits.contains { $0.used >= threshold } }
}

struct Limit: Decodable, Identifiable {
    let key: String
    let used: Double
    let resetsAt: Date?
    let windowSeconds: Double

    var id: String { key }
    var title: String { key == "five_hour" ? "Session" : key == "seven_day" ? "Weekly" : key }

    enum CodingKeys: String, CodingKey {
        case key, used
        case resetsAt = "resets_at"
        case windowSeconds = "window_seconds"
    }
}

struct HistoryDoc: Decodable {
    let tool: String
    let days: [String]
    let accounts: [String: [Double]]
}

// MARK: - Dates

enum ArlDate {
    /// Go writes RFC 3339 with up to nine fraction digits, which ISO8601DateFormatter rejects.
    static func parse(_ raw: String) -> Date? {
        var s = raw
        if let dot = s.firstIndex(of: "."), let end = s[dot...].firstIndex(where: { !$0.isNumber && $0 != "." }) {
            s.removeSubrange(dot..<end)
        }
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f.date(from: s)
    }

    static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let c = try decoder.singleValueContainer()
            let raw = try c.decode(String.self)
            guard let date = parse(raw) else {
                throw DecodingError.dataCorruptedError(in: c, debugDescription: "bad date \(raw)")
            }
            return date
        }
        return d
    }()
}
