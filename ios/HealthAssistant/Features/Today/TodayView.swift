import SwiftUI

@Observable
final class DayModel {
    var date: Date = .now
    var day: DayDTO?
    var summary: String?
    var summaryLoading = false
    var error: String?
    var loading = false

    var key: String { DayKey.string(date) }
    var isToday: Bool { Calendar.current.isDateInToday(date) }

    func load(api: APIClient) async {
        loading = true
        defer { loading = false }
        do {
            let d = try await api.day(key)
            day = d
            error = nil
            summary = d.dailySummary.text
            if d.dailySummary.state == "missing" || d.dailySummary.state == "stale" {
                await loadSummary(api: api)
            }
        } catch let e as APIError {
            error = e.userMessage
        } catch {}
    }

    func loadSummary(api: APIClient) async {
        summaryLoading = true
        defer { summaryLoading = false }
        if let a = try? await api.daySummary(key) { summary = a.text }
    }

    func entries(_ type: MealType) -> [MealEntryDTO] {
        (day?.mealEntries ?? []).filter { $0.mealType == type }
    }
}

struct TodayView: View {
    @Environment(AppModel.self) private var app
    @State private var model = DayModel()
    @State private var capturing = false
    @State private var manualType: MealType?
    @State private var path = NavigationPath()

    var body: some View {
        NavigationStack(path: $path) {
            ScrollView {
                VStack(spacing: 18) {
                    header
                    bowlCard
                    summaryCard
                    ForEach(MealType.allCases) { type in mealSection(type) }
                    Color.clear.frame(height: 80)
                }
                .padding(.horizontal, 16)
            }
            .background(Palette.rice)
            .refreshable { await model.load(api: app.api) }
            .overlay(alignment: .bottom) { cameraButton }
            .navigationDestination(for: String.self) { id in MealEntryDetailView(entryID: id) }
            .toolbar(.hidden, for: .navigationBar)
        }
        .task(id: "\(model.key)-\(app.dataVersion)") { await model.load(api: app.api) }
        .fullScreenCover(isPresented: $capturing) {
            CaptureFlow { entry in
                capturing = false
                app.entriesChanged()
                if let entry { path.append(entry.id) }
            }
        }
        .sheet(item: $manualType) { type in
            ManualEntrySheet(mealType: type) { app.entriesChanged() }
        }
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 2) {
                Text(model.isToday ? String(localized: "Today") : model.date.formatted(.dateTime.month().day().weekday()))
                    .font(.system(size: 30, weight: .bold)).foregroundStyle(Palette.ink)
                Text(model.date.formatted(.dateTime.year().month().day()))
                    .font(.system(size: 13)).foregroundStyle(Palette.ink3)
            }
            Spacer()
            Button { shift(-1) } label: { Image(systemName: "chevron.left") }
                .accessibilityLabel(Text("Previous day"))
            Button { shift(1) } label: { Image(systemName: "chevron.right") }
                .disabled(model.isToday)
                .accessibilityLabel(Text("Next day"))
        }
        .font(.system(size: 18, weight: .semibold))
        .foregroundStyle(Palette.tea)
        .padding(.top, 8)
    }

    private func shift(_ days: Int) {
        withAnimation(.snappy) { model.date = Calendar.current.date(byAdding: .day, value: days, to: model.date) ?? model.date }
    }

    private var bowlCard: some View {
        let totals = model.day?.totals ?? .zero
        let target = model.day?.dailyTarget
        return VStack(spacing: 14) {
            DayBowl(eaten: totals, targetKcal: target?.kcal)
                .padding(.top, 6)
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                Text(totals.kcal.kcalText)
                    .font(.number(34, weight: .bold))
                    .foregroundStyle((target?.kcal ?? .infinity) < totals.kcal ? Palette.chili : Palette.ink)
                    .contentTransition(.numericText(value: totals.kcal))
                    .accessibilityIdentifier("dayKcal")
                if let t = target {
                    Text("/ \(t.kcal.kcalText) kcal").font(.number(16, weight: .medium)).foregroundStyle(Palette.ink3)
                } else {
                    Text("kcal").font(.system(size: 16)).foregroundStyle(Palette.ink3)
                }
            }
            .animation(.snappy, value: totals.kcal)
            VStack(spacing: 10) {
                MacroBar(title: String(localized: "Protein"), color: Palette.protein, value: totals.proteinG, target: target?.proteinG)
                MacroBar(title: String(localized: "Fat"), color: Palette.fat, value: totals.fatG, target: target?.fatG)
                MacroBar(title: String(localized: "Carbs"), color: Palette.carb, value: totals.carbG, target: target?.carbG)
            }
            if target == nil {
                Text("Set up your profile in Me to get a daily target.").font(.footnote).foregroundStyle(Palette.ink3)
            }
        }
        .card(radius: 24)
    }

    @ViewBuilder private var summaryCard: some View {
        if let day = model.day, !day.mealEntries.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                Label("Daily summary", systemImage: "sparkles").font(.system(size: 14, weight: .semibold)).foregroundStyle(Palette.tea)
                if model.summaryLoading && model.summary == nil {
                    Text("Writing your summary for today, with a few practical tips.")
                        .redacted(reason: .placeholder)
                        .shimmering()
                } else if let s = model.summary {
                    Text(s).font(.system(size: 15)).foregroundStyle(Palette.ink).accessibilityIdentifier("dailySummary")
                    Text("General diet information, not medical advice.").font(.system(size: 11)).foregroundStyle(Palette.ink3)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(16)
            .background(Palette.celadon, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
            .animation(.easeOut, value: model.summary)
        }
    }

    private func mealSection(_ type: MealType) -> some View {
        let entries = model.entries(type)
        return VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(type.title).font(.system(size: 17, weight: .semibold)).foregroundStyle(Palette.ink)
                Spacer()
                let kcal = entries.reduce(0) { $0 + $1.nutrients.kcal }
                if kcal > 0 { Text("\(kcal.kcalText) kcal").font(.number(14)).foregroundStyle(Palette.ink2) }
                Menu {
                    Button("Take a photo", systemImage: "camera") { capturing = true }
                    Button("Add by hand", systemImage: "square.and.pencil") { manualType = type }
                } label: {
                    Image(systemName: "plus.circle.fill").font(.system(size: 22)).foregroundStyle(Palette.tea)
                }
                .accessibilityLabel(Text("Add to \(type.title)"))
            }
            if entries.isEmpty {
                if type != .snack {
                    Button { capturing = true } label: {
                        HStack(spacing: 14) {
                            Image("Empty").resizable().scaledToFill().frame(width: 56, height: 56)
                                .clipShape(RoundedRectangle(cornerRadius: 14))
                            VStack(alignment: .leading, spacing: 3) {
                                Text("\(type.title) is empty").font(.system(size: 15, weight: .semibold)).foregroundStyle(Palette.ink)
                                Text("Take a photo to log it").font(.system(size: 13)).foregroundStyle(Palette.ink3)
                            }
                            Spacer()
                        }
                        .padding(12)
                        .background(Palette.card.opacity(0.6), in: RoundedRectangle(cornerRadius: 18))
                        .overlay(RoundedRectangle(cornerRadius: 18).stroke(Palette.celadon, style: StrokeStyle(lineWidth: 1.5, dash: [6, 5])))
                    }
                    .buttonStyle(.plain)
                }
            } else {
                ForEach(entries) { e in
                    NavigationLink(value: e.id) { EntryRow(entry: e) }.buttonStyle(.plain)
                }
            }
        }
    }

    private var cameraButton: some View {
        Button { capturing = true } label: {
            Label("Log a meal", systemImage: "camera.fill")
                .font(.system(size: 17, weight: .semibold))
                .padding(.horizontal, 26).frame(height: 56)
                .foregroundStyle(.white)
                .background(Palette.tea, in: Capsule())
                .shadow(color: Palette.tea.opacity(0.35), radius: 14, y: 8)
        }
        .padding(.bottom, 12)
        .accessibilityIdentifier("logMeal")
    }
}

struct EntryRow: View {
    var entry: MealEntryDTO
    var body: some View {
        HStack(spacing: 12) {
            AsyncImage(url: entry.photoUrl) { img in img.resizable().scaledToFill() } placeholder: {
                Palette.celadon.overlay(Image(systemName: "fork.knife").foregroundStyle(Palette.tea))
            }
            .frame(width: 56, height: 56)
            .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
            VStack(alignment: .leading, spacing: 4) {
                Text(entry.foodItems.map { Names.pick(zh: $0.nameZh, en: $0.nameEn) }.joined(separator: String(localized: ", ")))
                    .font(.system(size: 15, weight: .semibold)).foregroundStyle(Palette.ink).lineLimit(2)
                Text(entry.eatenAt.formatted(date: .omitted, time: .shortened)).font(.system(size: 12)).foregroundStyle(Palette.ink3)
            }
            Spacer()
            Text("\(entry.nutrients.kcal.kcalText)").font(.number(17)).foregroundStyle(Palette.ink)
            Text("kcal").font(.system(size: 12)).foregroundStyle(Palette.ink3)
        }
        .padding(12)
        .background(Palette.card, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
    }
}

/// Moving highlight for loading text.
struct Shimmer: ViewModifier {
    @State private var phase: CGFloat = -1
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    func body(content: Content) -> some View {
        content.overlay {
            if !reduceMotion {
                GeometryReader { g in
                    LinearGradient(colors: [.clear, .white.opacity(0.7), .clear], startPoint: .leading, endPoint: .trailing)
                        .frame(width: g.size.width / 2)
                        .offset(x: phase * g.size.width)
                }
                .mask(content)
                .onAppear { withAnimation(.linear(duration: 1.3).repeatForever(autoreverses: false)) { phase = 1.5 } }
            }
        }
    }
}

extension View {
    func shimmering() -> some View { modifier(Shimmer()) }
}
