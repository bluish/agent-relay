// Shared look for the panels: rounded cards on the menu bar material, a hero
// figure on top, small uppercase section labels, capsule meters and one
// accent colour.

import SwiftUI

enum Theme {
    static let accent = Color(red: 0.91, green: 0.54, blue: 0.18)
    static let good = Color(red: 0.36, green: 0.80, blue: 0.47)
    static let bad = Color(red: 0.94, green: 0.36, blue: 0.33)
    static let cardFill = Color.primary.opacity(0.06)
    static let width: CGFloat = 360
}

/// A rounded card. Pass a title for the header row, with an optional
/// trailing value.
struct Card<Content: View>: View {
    var title: String?
    var trailing: String?
    var trailingColor: Color = .primary
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let title {
                HStack(alignment: .firstTextBaseline) {
                    Text(title).font(.system(size: 13, weight: .semibold))
                    Spacer()
                    if let trailing {
                        Text(trailing).font(.system(size: 13, weight: .semibold).monospacedDigit())
                            .foregroundStyle(trailingColor)
                    }
                }
            }
            content
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.cardFill, in: .rect(cornerRadius: 12, style: .continuous))
    }
}

/// "PICTURE", "FOLLOWERS": the label above a group of cards.
struct SectionLabel: View {
    let text: String
    init(_ text: String) { self.text = text }

    var body: some View {
        Text(text.uppercased())
            .font(.system(size: 10, weight: .semibold))
            .tracking(0.6)
            .foregroundStyle(.tertiary)
            .padding(.leading, 4)
            .padding(.top, 4)
    }
}

/// The panel's first row: tinted symbol, title, a quiet trailing note.
struct PanelHeader<Trailing: View>: View {
    let icon: String
    let title: String
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: icon).font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.accent)
            Text(title).font(.system(size: 13, weight: .semibold))
            Spacer()
            trailing
        }
        .padding(.horizontal, 4)
    }
}

/// A big figure with a small unit after it ("83%", "PC").
struct HeroFigure: View {
    let value: String
    var unit: String?

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text(value).font(.system(size: 34, weight: .semibold, design: .rounded).monospacedDigit())
            if let unit { Text(unit).font(.system(size: 13)).foregroundStyle(.secondary) }
        }
    }
}

struct CapsuleMeter: View {
    let fraction: Double
    var color: Color = Theme.accent
    var height: CGFloat = 6

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule().fill(Color.primary.opacity(0.1))
                Capsule().fill(color).frame(width: max(height, geo.size.width * min(1, max(0, fraction))))
            }
        }
        .frame(height: height)
    }
}

/// A small label over a value, as in "Brightness / 50%".
struct Stat: View {
    let label: String
    let value: String
    var color: Color = .primary

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label).font(.system(size: 11)).foregroundStyle(.secondary)
            Text(value).font(.system(size: 15, weight: .semibold).monospacedDigit()).foregroundStyle(color)
        }
    }
}

/// The filled accent capsule for a panel's main action.
struct AccentButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 13, weight: .medium))
            .foregroundStyle(.white)
            .frame(maxWidth: .infinity, minHeight: 30)
            .background(Theme.accent.opacity(configuration.isPressed ? 0.75 : 1), in: .capsule)
    }
}

/// A quiet capsule for secondary actions.
struct QuietButtonStyle: ButtonStyle {
    var prominent = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 12, weight: .medium))
            .foregroundStyle(prominent ? .white : .primary)
            .padding(.horizontal, 10)
            .frame(minHeight: 28)
            .background(prominent ? Theme.accent : Color.primary.opacity(configuration.isPressed ? 0.16 : 0.09), in: .capsule)
    }
}

/// Footer link: quiet text that brightens on hover.
struct FooterButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 12))
            .foregroundStyle(configuration.isPressed ? .primary : .secondary)
    }
}
