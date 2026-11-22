import SwiftUI

/// Design tokens (docs/design/ui-design.md).
enum Palette {
    static let rice = Color(hex: 0xF5F7F2)
    static let celadon = Color(hex: 0xE3EEDD)
    static let tea = Color(hex: 0x2F7D5B)
    static let yolk = Color(hex: 0xF2B33D)
    static let porcelain = Color(hex: 0x3E6FB0)
    static let chili = Color(hex: 0xD9513C)
    static let ink = Color(hex: 0x1E2A24)
    static let ink2 = Color(hex: 0x56635C)
    static let ink3 = Color(hex: 0x8B968F)
    static let card = Color.white

    static let protein = tea
    static let fat = yolk
    static let carb = porcelain
}

extension Color {
    init(hex: UInt32) {
        self.init(red: Double((hex >> 16) & 0xFF) / 255, green: Double((hex >> 8) & 0xFF) / 255, blue: Double(hex & 0xFF) / 255)
    }
}

extension Font {
    /// SF Pro Rounded for every number.
    static func number(_ size: CGFloat, weight: Font.Weight = .semibold) -> Font {
        .system(size: size, weight: weight, design: .rounded).monospacedDigit()
    }
}

struct CardStyle: ViewModifier {
    var radius: CGFloat = 20
    func body(content: Content) -> some View {
        content
            .padding(16)
            .background(Palette.card, in: RoundedRectangle(cornerRadius: radius, style: .continuous))
    }
}

extension View {
    func card(radius: CGFloat = 20) -> some View { modifier(CardStyle(radius: radius)) }
}

struct PrimaryButtonStyle: ButtonStyle {
    var color: Color = Palette.tea
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 17, weight: .semibold))
            .foregroundStyle(.white)
            .frame(maxWidth: .infinity, minHeight: 54)
            .background(color.opacity(configuration.isPressed ? 0.85 : 1), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
            .scaleEffect(configuration.isPressed ? 0.98 : 1)
            .animation(.spring(duration: 0.25), value: configuration.isPressed)
    }
}

struct SecondaryButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 16, weight: .semibold))
            .foregroundStyle(Palette.tea)
            .frame(maxWidth: .infinity, minHeight: 50)
            .background(Palette.celadon.opacity(configuration.isPressed ? 0.7 : 1), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
    }
}

/// Localized names.
extension MealType {
    var title: String {
        switch self {
        case .breakfast: String(localized: "Breakfast")
        case .lunch: String(localized: "Lunch")
        case .dinner: String(localized: "Dinner")
        case .snack: String(localized: "Snack")
        }
    }

    static func suggested(for date: Date = .now) -> MealType {
        let c = Calendar.current.dateComponents([.hour, .minute], from: date)
        let m = (c.hour ?? 0) * 60 + (c.minute ?? 0)
        switch m {
        case 240..<630: return .breakfast
        case 630..<870: return .lunch
        case 1020..<1290: return .dinner
        default: return .snack
        }
    }
}

enum Names {
    static var useChinese: Bool { APIClient.languageHeader == "zh-Hans" }
    static func pick(zh: String, en: String) -> String {
        if useChinese { return zh.isEmpty ? en : zh }
        return en.isEmpty ? zh : en
    }
}

extension Double {
    var kcalText: String { String(Int(self.rounded())) }
    var gramsText: String { self < 10 ? String(format: "%.1f", self) : String(Int(self.rounded())) }
}

enum DayKey {
    static let formatter: DateFormatter = {
        let f = DateFormatter()
        f.calendar = Calendar(identifier: .gregorian)
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "yyyy-MM-dd"
        return f
    }()

    static func string(_ d: Date) -> String { formatter.string(from: d) }
    static func date(_ s: String) -> Date? { formatter.date(from: s) }
}

/// Reduce Motion aware animation.
extension Animation {
    static func motion(_ a: Animation, reduce: Bool) -> Animation? { reduce ? nil : a }
}
