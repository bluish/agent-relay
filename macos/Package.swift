// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "AgentRelayBar",
    platforms: [.macOS("27.0")],
    targets: [
        .executableTarget(
            name: "AgentRelayBar",
            path: "Sources/AgentRelayBar"
        )
    ]
)
