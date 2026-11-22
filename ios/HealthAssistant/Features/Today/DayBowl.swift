import SwiftUI

/// The day bowl: a porcelain bowl seen from above. The filled area is
/// proportional to eaten energy / daily target; the fill is split into
/// sectors by the energy share of protein, fat, and carbohydrate.
struct DayBowl: View {
    var eaten: Nutrients
    var targetKcal: Double?
    var size: CGFloat = 220

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var shownFill: Double = 0

    private var fill: Double {
        guard let t = targetKcal, t > 0 else { return eaten.kcal > 0 ? 0.6 : 0 }
        return min(eaten.kcal / t, 1)
    }
    private var over: Bool { (targetKcal ?? .infinity) < eaten.kcal }

    private var shares: (Double, Double, Double) {
        let p = 4 * eaten.proteinG, f = 9 * eaten.fatG, c = 4 * eaten.carbG
        let sum = p + f + c
        guard sum > 0 else { return (1.0 / 3, 1.0 / 3, 1.0 / 3) }
        return (p / sum, f / sum, c / sum)
    }

    var body: some View {
        ZStack {
            // Rim and porcelain pattern.
            Circle().fill(Color.white)
                .shadow(color: Palette.tea.opacity(0.12), radius: 18, y: 10)
            Circle().stroke(Palette.porcelain.opacity(0.85), lineWidth: size * 0.035).padding(size * 0.03)
            Circle().stroke(Palette.porcelain.opacity(0.35), style: StrokeStyle(lineWidth: 1.5, dash: [3, 5])).padding(size * 0.075)
            ForEach(0..<12) { i in
                Image(systemName: "leaf.fill")
                    .font(.system(size: size * 0.035))
                    .foregroundStyle(Palette.porcelain.opacity(0.55))
                    .offset(y: -size * 0.41)
                    .rotationEffect(.degrees(Double(i) * 30))
            }
            Circle().fill(Palette.rice).padding(size * 0.12)

            MacroFill(fill: shownFill, protein: shares.0, fat: shares.1)
                .padding(size * 0.12)

            if over {
                Circle().stroke(Palette.chili, lineWidth: 4).padding(size * 0.1)
                    .transition(.scale.combined(with: .opacity))
            }
        }
        .frame(width: size, height: size)
        .onAppear { animate() }
        .onChange(of: fill) { animate() }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text("Eaten \(Int(eaten.kcal)) kcal"))
    }

    private func animate() {
        if reduceMotion { shownFill = fill } else { withAnimation(.spring(duration: 0.9, bounce: 0.25)) { shownFill = fill } }
    }
}

/// Three wedges in a disk whose area is `fill` of the full disk.
struct MacroFill: View, Animatable {
    var fill: Double
    var protein: Double
    var fat: Double

    var animatableData: Double {
        get { fill }
        set { fill = newValue }
    }

    var body: some View {
        Canvas { ctx, size in
            let r = min(size.width, size.height) / 2 * CGFloat(sqrt(max(fill, 0)))
            let c = CGPoint(x: size.width / 2, y: size.height / 2)
            guard r > 0.5 else { return }
            var start = Angle.degrees(-90)
            for (share, color) in [(protein, Palette.protein), (fat, Palette.fat), (1 - protein - fat, Palette.carb)] where share > 0 {
                let end = start + .degrees(360 * share)
                var p = Path()
                p.move(to: c)
                p.addArc(center: c, radius: r, startAngle: start, endAngle: end, clockwise: false)
                p.closeSubpath()
                ctx.fill(p, with: .color(color.opacity(0.88)))
                start = end
            }
            ctx.stroke(Path(ellipseIn: CGRect(x: c.x - r, y: c.y - r, width: 2 * r, height: 2 * r)), with: .color(.white.opacity(0.7)), lineWidth: 1.5)
        }
    }
}

struct MacroBar: View {
    var title: String
    var color: Color
    var value: Double
    var target: Double?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Circle().fill(color).frame(width: 8, height: 8)
                Text(title).font(.system(size: 13)).foregroundStyle(Palette.ink2)
                Spacer()
                Text(target.map { "\(Int(value)) / \(Int($0)) g" } ?? "\(Int(value)) g")
                    .font(.number(13)).foregroundStyle(Palette.ink)
            }
            GeometryReader { g in
                let frac = target.map { value / max($0, 1) } ?? 0
                ZStack(alignment: .leading) {
                    Capsule().fill(Palette.celadon)
                    Capsule().fill(frac > 1.2 ? Palette.chili : color).frame(width: g.size.width * min(frac, 1))
                }
            }
            .frame(height: 8)
            .animation(.spring(duration: 0.6), value: value)
        }
    }
}
