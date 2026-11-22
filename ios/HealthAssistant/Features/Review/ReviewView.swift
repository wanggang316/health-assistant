import SwiftUI

/// Review a recognition (or edit a meal entry) before it is saved (ADR 0003).
struct ReviewView: View {
    @Bindable var draft: ReviewDraft
    var photo: Data?
    var confirmTitle: String
    var saving: Bool
    var error: String?
    var onConfirm: () -> Void
    var onCancel: () -> Void

    @State private var appeared = false
    @State private var expanded: Set<UUID> = []
    @State private var choosing: ChooseTarget?
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    struct ChooseTarget: Identifiable {
        var item: UUID
        var ingredient: UUID?
        var id: String { "\(item)-\(ingredient?.uuidString ?? "new")" }
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                if let photo, let img = UIImage(data: photo) {
                    Image(uiImage: img).resizable().scaledToFill().frame(width: 58, height: 58)
                        .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
                }
                VStack(alignment: .leading, spacing: 2) {
                    Text("Check the meal").font(.system(size: 22, weight: .bold)).foregroundStyle(Palette.ink)
                    Text("Adjust grams or remove what you did not eat.").font(.system(size: 13)).foregroundStyle(Palette.ink2)
                }
                Spacer()
                Button(action: onCancel) { Image(systemName: "xmark").font(.system(size: 15, weight: .bold)) }
                    .frame(width: 34, height: 34).background(Palette.celadon, in: Circle()).foregroundStyle(Palette.ink2)
                    .accessibilityLabel(Text("Cancel"))
            }
            .padding(20)

            ScrollView {
                VStack(spacing: 12) {
                    mealTypePicker
                    ForEach(Array(draft.items.enumerated()), id: \.element.id) { index, item in
                        itemCard(item)
                            .opacity(appeared ? 1 : 0)
                            .offset(y: appeared ? 0 : 30)
                            .animation(reduceMotion ? nil : .spring(duration: 0.5, bounce: 0.25).delay(0.08 * Double(index)), value: appeared)
                    }
                    Button { choosing = ChooseTarget(item: UUID(), ingredient: nil) } label: {
                        Label("Add a food", systemImage: "plus").frame(maxWidth: .infinity)
                    }
                    .buttonStyle(SecondaryButtonStyle())
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 16)
            }

            VStack(spacing: 10) {
                if let error { Text(error).font(.footnote).foregroundStyle(Palette.chili).multilineTextAlignment(.center) }
                HStack {
                    Text("Total").foregroundStyle(Palette.ink2)
                    Spacer()
                    Text("\(draft.total.kcal.kcalText) kcal").font(.number(22, weight: .bold)).foregroundStyle(Palette.ink)
                        .contentTransition(.numericText(value: draft.total.kcal))
                        .animation(.snappy, value: draft.total.kcal)
                }
                Button(action: onConfirm) {
                    if saving { ProgressView().tint(.white) } else { Text(confirmTitle) }
                }
                .buttonStyle(PrimaryButtonStyle())
                .disabled(!draft.canSave || saving)
                .accessibilityIdentifier("confirmEntry")
                if draft.items.contains(where: \.unresolved) {
                    Text("Choose a food for each ingredient marked in yellow.").font(.footnote).foregroundStyle(Palette.ink3)
                }
            }
            .padding(20)
            .background(.ultraThinMaterial)
        }
        .background(Palette.rice)
        .onAppear { appeared = true; expanded = Set(draft.items.filter { $0.unresolved || $0.needsGramCheck }.map(\.id)) }
        .sheet(item: $choosing) { target in
            FoodSearchView(initialQuery: initialQuery(target), candidates: candidates(target)) { food, name in
                apply(food: food, name: name, to: target)
                choosing = nil
            }
        }
    }

    private var mealTypePicker: some View {
        HStack(spacing: 8) {
            ForEach(MealType.allCases) { t in
                Button { withAnimation(.snappy) { draft.mealType = t } } label: {
                    Text(t.title).font(.system(size: 14, weight: .semibold))
                        .padding(.horizontal, 14).frame(height: 34)
                        .foregroundStyle(draft.mealType == t ? .white : Palette.ink2)
                        .background(draft.mealType == t ? Palette.tea : Palette.celadon, in: Capsule())
                }
                .buttonStyle(.plain)
            }
            Spacer()
        }
    }

    private func binding(_ item: DraftItem) -> Binding<DraftItem>? {
        guard let i = draft.items.firstIndex(where: { $0.id == item.id }) else { return nil }
        return $draft.items[i]
    }

    private func itemCard(_ item: DraftItem) -> some View {
        let open = expanded.contains(item.id)
        return VStack(alignment: .leading, spacing: 10) {
            Button {
                withAnimation(.spring(duration: 0.4)) { if open { expanded.remove(item.id) } else { expanded.insert(item.id) } }
            } label: {
                HStack {
                    VStack(alignment: .leading, spacing: 3) {
                        Text(item.name).font(.system(size: 17, weight: .semibold)).foregroundStyle(Palette.ink)
                        Text("\(item.ingredients.count) ingredients").font(.system(size: 12)).foregroundStyle(Palette.ink3)
                    }
                    Spacer()
                    Text("\(item.nutrients.kcal.kcalText) kcal").font(.number(16)).foregroundStyle(Palette.ink)
                    Image(systemName: "chevron.down").rotationEffect(.degrees(open ? 180 : 0)).foregroundStyle(Palette.ink3)
                }
            }
            .buttonStyle(.plain)

            if item.needsGramCheck {
                Label("Check the grams: the photo and the database disagree on energy.", systemImage: "exclamationmark.triangle.fill")
                    .font(.system(size: 13)).foregroundStyle(Color(hex: 0x8A5A00))
                    .padding(10).frame(maxWidth: .infinity, alignment: .leading)
                    .background(Palette.yolk.opacity(0.2), in: RoundedRectangle(cornerRadius: 12))
            }

            if open, let b = binding(item) {
                ForEach(b.ingredients) { $ing in
                    ingredientRow($ing, itemID: item.id)
                }
                HStack {
                    Button("Add ingredient", systemImage: "plus") { choosing = ChooseTarget(item: item.id, ingredient: UUID()) }
                    Spacer()
                    Button("Remove dish", systemImage: "trash", role: .destructive) {
                        withAnimation { draft.items.removeAll { $0.id == item.id } }
                    }
                    .foregroundStyle(Palette.chili)
                }
                .font(.system(size: 14, weight: .medium))
                .padding(.top, 4)
            }
        }
        .card(radius: 20)
    }

    private func ingredientRow(_ ing: Binding<DraftIngredient>, itemID: UUID) -> some View {
        let value = ing.wrappedValue
        return VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Text(value.name).font(.system(size: 15)).foregroundStyle(Palette.ink)
                if value.isCookingOil {
                    Text("Cooking oil").font(.system(size: 11, weight: .semibold)).padding(.horizontal, 7).padding(.vertical, 2)
                        .foregroundStyle(Color(hex: 0x8A5A00)).background(Palette.yolk.opacity(0.25), in: Capsule())
                }
                Spacer()
                Button { ing.wrappedValue.grams = max(1, (value.grams - 10).rounded()) } label: { Image(systemName: "minus") }
                    .frame(width: 30, height: 30).background(Palette.celadon, in: Circle())
                Text("\(value.grams.gramsText) g").font(.number(15)).frame(minWidth: 58)
                    .contentTransition(.numericText(value: value.grams))
                    .animation(.snappy, value: value.grams)
                Button { ing.wrappedValue.grams = min(5000, (value.grams + 10).rounded()) } label: { Image(systemName: "plus") }
                    .frame(width: 30, height: 30).background(Palette.celadon, in: Circle())
            }
            .foregroundStyle(Palette.tea)
            HStack {
                if let food = value.food {
                    Button { choosing = ChooseTarget(item: itemID, ingredient: value.id) } label: {
                        Text(food.description).font(.system(size: 11)).foregroundStyle(Palette.ink3).lineLimit(1)
                    }
                } else {
                    Button { choosing = ChooseTarget(item: itemID, ingredient: value.id) } label: {
                        Label("Choose a food", systemImage: "magnifyingglass").font(.system(size: 13, weight: .semibold))
                    }
                    .foregroundStyle(Color(hex: 0x8A5A00))
                    .padding(.horizontal, 10).padding(.vertical, 4)
                    .background(Palette.yolk.opacity(0.25), in: Capsule())
                }
                Spacer()
                if let n = value.nutrients { Text("\(n.kcal.kcalText) kcal").font(.number(12)).foregroundStyle(Palette.ink3) }
                Button(role: .destructive) {
                    withAnimation { removeIngredient(value.id, from: itemID) }
                } label: { Image(systemName: "minus.circle") }
                .foregroundStyle(Palette.ink3)
                .accessibilityLabel(Text("Remove ingredient"))
            }
        }
        .padding(.vertical, 6)
        .overlay(alignment: .bottom) { Divider() }
    }

    private func removeIngredient(_ id: UUID, from itemID: UUID) {
        guard let i = draft.items.firstIndex(where: { $0.id == itemID }) else { return }
        draft.items[i].ingredients.removeAll { $0.id == id }
        if draft.items[i].ingredients.isEmpty { draft.items.remove(at: i) }
    }

    private func find(_ t: ChooseTarget) -> (Int, Int?)? {
        guard let i = draft.items.firstIndex(where: { $0.id == t.item }) else { return nil }
        let j = t.ingredient.flatMap { id in draft.items[i].ingredients.firstIndex { $0.id == id } }
        return (i, j)
    }

    private func initialQuery(_ t: ChooseTarget) -> String {
        guard let (i, j) = find(t), let j else { return "" }
        let ing = draft.items[i].ingredients[j]
        return Names.pick(zh: ing.nameZh, en: ing.nameEn)
    }

    private func candidates(_ t: ChooseTarget) -> [FoodRef] {
        guard let (i, j) = find(t), let j else { return [] }
        return draft.items[i].ingredients[j].candidates
    }

    private func apply(food: FoodRef, name: String, to t: ChooseTarget) {
        if let (i, j) = find(t) {
            if let j {
                draft.items[i].ingredients[j].food = food
            } else {
                draft.items[i].ingredients.append(DraftIngredient(nameZh: name, nameEn: name, grams: 100, isCookingOil: false, food: food))
            }
        } else {
            draft.items.append(DraftItem(nameZh: name, nameEn: name, cookingMethod: "other", modelKcal: nil, serverGramCheck: false,
                                         ingredients: [DraftIngredient(nameZh: name, nameEn: name, grams: 100, isCookingOil: false, food: food)]))
        }
    }
}
