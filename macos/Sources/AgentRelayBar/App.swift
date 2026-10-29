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
