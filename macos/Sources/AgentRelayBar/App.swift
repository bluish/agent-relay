import ServiceManagement
import SwiftUI

@main
struct AgentRelayBarApp: App {
    @State private var store = Store()

    init() {
        // `AgentRelayBar --snapshot out.png` renders the panel to an image and exits (for previews).
        let args = CommandLine.arguments
        if let i = args.firstIndex(of: "--snapshot"), i + 1 < args.count {
            Snapshot.render(to: args[i + 1])
        }
        // `AgentRelayBar --login-item on|off` registers the app as a login item and exits.
        if let i = args.firstIndex(of: "--login-item"), i + 1 < args.count {
            do {
                if args[i + 1] == "on" { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
                print("login item: \(SMAppService.mainApp.status == .enabled ? "on" : "off")")
                exit(0)
            } catch {
                FileHandle.standardError.write(Data("login item: \(error.localizedDescription)\n".utf8))
                exit(1)
            }
        }
    }

    var body: some Scene {
        MenuBarExtra {
            PanelView().environment(store)
        } label: {
            MenuBarLabel().environment(store)
        }
        .menuBarExtraStyle(.window)
    }
}

@MainActor
enum Snapshot {
    static func render(to path: String) {
        GlassMode.plain = true
        let store = Store()
        let deadline = Date.now.addingTimeInterval(20)
        while store.tools.isEmpty && store.error == nil && Date.now < deadline {
            RunLoop.main.run(until: .now.addingTimeInterval(0.2))
        }
        RunLoop.main.run(until: .now.addingTimeInterval(1))
        let view = PanelView()
            .environment(store)
            .background(Color(nsColor: .windowBackgroundColor))
            .environment(\.colorScheme, .dark)
        let renderer = ImageRenderer(content: view)
        renderer.scale = 2
        if let image = renderer.nsImage, let tiff = image.tiffRepresentation,
           let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:]) {
            try? png.write(to: URL(fileURLWithPath: path))
        }
        exit(0)
    }
}
