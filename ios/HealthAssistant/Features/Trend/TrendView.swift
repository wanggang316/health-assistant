import SwiftUI

struct TrendView: View {
    @Environment(AppModel.self) private var app
    @State private var days: [String: DayTotalDTO] = [:]
    @State private var dayDetails: [String: DayDTO] = [:]
    @State private var loading = false

    private var week: [Date] {
        (0..<7).reversed().compactMap { Calendar.current.date(byAdding: .day, value: -$0, to: .now) }
    }

    private var target: Double? { app.me?.dailyTarget?.kcal }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    Text("Trend").font(.system(size: 30, weight: .bold)).foregroundStyle(Palette.ink).padding(.top, 8)
                    HStack(spacing: 6) {
                        ForEach(week, id: \.self) { d in
                            let key = DayKey.string(d)
                            let detail = dayDetails[key]
                            VStack(spacing: 6) {
                                DayBowl(eaten: detail?.totals ?? Nutrients(kcal: days[key]?.kcal ?? 0, proteinG: 0, fatG: 0, carbG: 0),
                                        targetKcal: target, size: 44)
                                Text(d.formatted(.dateTime.weekday(.narrow))).font(.system(size: 12)).foregroundStyle(Palette.ink3)
                                Text(days[key].map { $0.kcal.kcalText } ?? "–").font(.number(11)).foregroundStyle(Palette.ink2)
                            }
                            .frame(maxWidth: .infinity)
                        }
                    }
                    .card(radius: 24)

                    let logged = days.values.filter { $0.entryCount > 0 }
                    let avg = logged.isEmpty ? 0 : logged.reduce(0) { $0 + $1.kcal } / Double(logged.count)
                    HStack {
                        stat(String(localized: "Daily average"), "\(avg.kcalText)", "kcal")
                        stat(String(localized: "Days logged"), "\(logged.count)", "/ 7")
                        if let t = target { stat(String(localized: "Target"), t.kcalText, "kcal") }
                    }
                    let sum = dayDetails.values.reduce(Nutrients.zero) { $0 + $1.totals }
                    let n = Double(max(logged.count, 1))
                    VStack(spacing: 10) {
                        MacroBar(title: String(localized: "Protein"), color: Palette.protein, value: sum.proteinG / n, target: app.me?.dailyTarget?.proteinG)
                        MacroBar(title: String(localized: "Fat"), color: Palette.fat, value: sum.fatG / n, target: app.me?.dailyTarget?.fatG)
                        MacroBar(title: String(localized: "Carbs"), color: Palette.carb, value: sum.carbG / n, target: app.me?.dailyTarget?.carbG)
                        Text("Average per logged day").font(.system(size: 11)).foregroundStyle(Palette.ink3).frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .card(radius: 20)
                }
                .padding(.horizontal, 16)
            }
            .background(Palette.rice)
            .refreshable { await load() }
            .toolbar(.hidden, for: .navigationBar)
        }
        .task(id: app.dataVersion) { await load() }
    }

    private func stat(_ title: String, _ value: String, _ unit: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title).font(.system(size: 12)).foregroundStyle(Palette.ink3)
            HStack(alignment: .firstTextBaseline, spacing: 3) {
                Text(value).font(.number(22, weight: .bold)).foregroundStyle(Palette.ink)
                Text(unit).font(.system(size: 12)).foregroundStyle(Palette.ink3)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .card(radius: 18)
    }

    private func load() async {
        guard let first = week.first, let last = week.last else { return }
        loading = true
        defer { loading = false }
        if let list = try? await app.api.days(from: DayKey.string(first), to: DayKey.string(last)) {
            days = Dictionary(uniqueKeysWithValues: list.map { ($0.date, $0) })
            for d in list where d.entryCount > 0 {
                if let detail = try? await app.api.day(d.date) { dayDetails[d.date] = detail }
            }
        }
    }
}
