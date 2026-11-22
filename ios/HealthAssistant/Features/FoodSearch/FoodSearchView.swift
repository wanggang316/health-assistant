import SwiftUI

/// Search nutrition.db for manual input and to change an ingredient.
struct FoodSearchView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State var query: String
    var candidates: [FoodRef]
    var onPick: (FoodRef, String) -> Void

    @State private var results: [FoodSearchItem] = []
    @State private var searching = false

    init(initialQuery: String, candidates: [FoodRef], onPick: @escaping (FoodRef, String) -> Void) {
        _query = State(initialValue: initialQuery)
        self.candidates = candidates
        self.onPick = onPick
    }

    var body: some View {
        NavigationStack {
            List {
                if !candidates.isEmpty {
                    Section("Suggested") {
                        ForEach(candidates, id: \.fdcId) { f in row(f.description, f.per100g.kcal) { onPick(f, label) } }
                    }
                }
                Section(results.isEmpty ? "" : String(localized: "Results")) {
                    ForEach(results) { f in row(f.description, f.per100g.kcal) { onPick(f.ref, label) } }
                    if results.isEmpty && !query.isEmpty && !searching {
                        Text("No match. Try a simpler name, like “rice” or “米饭”.").font(.footnote).foregroundStyle(Palette.ink3)
                    }
                }
            }
            .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: Text("Search foods"))
            .navigationTitle("Choose a food")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } } }
            .task(id: query) {
                try? await Task.sleep(for: .milliseconds(300))
                await search()
            }
        }
    }

    private var label: String { query.trimmingCharacters(in: .whitespaces) }

    private func row(_ title: String, _ kcal: Double, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack {
                Text(title).font(.system(size: 15)).foregroundStyle(Palette.ink)
                Spacer()
                Text("\(kcal.kcalText) kcal/100 g").font(.number(12)).foregroundStyle(Palette.ink3)
            }
        }
    }

    private func search() async {
        let q = label
        guard !q.isEmpty else { results = []; return }
        searching = true
        defer { searching = false }
        results = (try? await app.api.searchFoods(q)) ?? []
    }
}
