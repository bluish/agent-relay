import Foundation
import Observation
import ServiceManagement

/// Talks to the `arl` CLI. Everything the panel shows comes from `arl usage --json`
/// and `arl history --json`; switching runs `arl switch`. No logins are touched here.
@MainActor
@Observable
final class Store {
    private(set) var tools: [ToolUsage] = []
    private(set) var history: [String: HistoryDoc] = [:]
    private(set) var updatedAt: Date?
    private(set) var loading = false
    private(set) var switching: String?
    var error: String?

    private var timer: Timer?

    init() {
        refresh()
        timer = Timer.scheduledTimer(withTimeInterval: 120, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refresh() }
        }
    }

    /// The Claude pool left, for the menu bar label.
    var menuBarPercent: Int? {
        guard let f = tools.first(where: { $0.tool == "claude" })?.forecast ?? tools.first?.forecast,
              f.capacityPct > 0 else { return nil }
        return Int((f.leftFraction * 100).rounded())
    }

    /// True when an active account is close to a limit.
    var needsAttention: Bool {
        tools.contains { t in t.accounts.contains { $0.active && $0.atLimit(0.95) } }
    }

    var hubURL: URL? {
        let path = FileManager.default.homeDirectoryForCurrentUser
            .appending(path: ".config/agent-relay/hub")
        guard let raw = try? String(contentsOf: path, encoding: .utf8)
            .trimmingCharacters(in: .whitespacesAndNewlines), !raw.isEmpty else { return nil }
        return URL(string: raw)
    }

    func refresh(force: Bool = false) {
        guard !loading else { return }
        loading = true
        Task {
            defer { loading = false }
            do {
                let usage = try await Arl.run(["usage", "--json"] + (force ? ["--refresh"] : []))
                let doc = try ArlDate.decoder.decode(UsageDoc.self, from: usage)
                tools = doc.tools
                updatedAt = .now
                error = nil
                if let raw = try? await Arl.run(["history", "--json", "--days", "7"]),
                   let docs = try? ArlDate.decoder.decode([HistoryDoc].self, from: raw) {
                    history = Dictionary(uniqueKeysWithValues: docs.map { ($0.tool, $0) })
                }
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    func use(_ account: String, for tool: String) {
        switching = "\(tool):\(account)"
        Task {
            defer { switching = nil }
            do {
                _ = try await Arl.run(["switch", tool, account])
                refresh()
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    // MARK: Login item

    var launchAtLogin: Bool {
        get { SMAppService.mainApp.status == .enabled }
        set {
            do {
                if newValue { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            } catch {
                self.error = "Login item: \(error.localizedDescription)"
            }
        }
    }
}

enum Arl {
    struct Failure: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    /// The arl executable: ~/.local/bin, Homebrew, or PATH.
    static var executable: URL? {
        let home = FileManager.default.homeDirectoryForCurrentUser
        let candidates = [
            home.appending(path: ".local/bin/arl").path,
            "/opt/homebrew/bin/arl",
            "/usr/local/bin/arl",
        ]
        return candidates.first { FileManager.default.isExecutableFile(atPath: $0) }.map(URL.init(fileURLWithPath:))
    }

    static func run(_ args: [String]) async throws -> Data {
        guard let exe = executable else {
            throw Failure(message: "arl not found. Install it in ~/.local/bin (arl update, or make install).")
        }
        return try await Task.detached {
            let p = Process()
            p.executableURL = exe
            p.arguments = args
            var env = ProcessInfo.processInfo.environment
            env["NO_COLOR"] = "1"
            p.environment = env
            let out = Pipe(), err = Pipe()
            p.standardOutput = out
            p.standardError = err
            try p.run()
            let data = out.fileHandleForReading.readDataToEndOfFile()
            let errData = err.fileHandleForReading.readDataToEndOfFile()
            p.waitUntilExit()
            guard p.terminationStatus == 0 else {
                let text = String(decoding: errData, as: UTF8.self)
                    .replacingOccurrences(of: "arl: error: ", with: "")
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                throw Failure(message: text.isEmpty ? "arl exited with \(p.terminationStatus)" : text)
            }
            return data
        }.value
    }
}
