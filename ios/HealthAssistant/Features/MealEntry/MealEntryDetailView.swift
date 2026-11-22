import SwiftUI

struct MealEntryDetailView: View {
    var entryID: String
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var entry: MealEntryDTO?
    @State private var feedback: String?
    @State private var feedbackLoading = false
    @State private var editing: ReviewDraft?
    @State private var saving = false
    @State private var error: String?
    @State private var confirmDelete = false

    var body: some View {
        ScrollView {
            if let entry {
                VStack(alignment: .leading, spacing: 16) {
                    AsyncImage(url: entry.photoUrl) { img in img.resizable().scaledToFill() } placeholder: {
                        Palette.celadon.overlay(Image(systemName: "fork.knife").font(.largeTitle).foregroundStyle(Palette.tea))
                    }
                    .frame(height: entry.photoUrl == nil ? 120 : 260)
                    .clipShape(RoundedRectangle(cornerRadius: 24, style: .continuous))

                    HStack(alignment: .firstTextBaseline) {
                        Text(entry.mealType.title).font(.system(size: 26, weight: .bold)).foregroundStyle(Palette.ink)
                        Spacer()
                        Text("\(entry.nutrients.kcal.kcalText) kcal").font(.number(22, weight: .bold)).foregroundStyle(Palette.ink)
                    }
                    Text(entry.eatenAt.formatted(date: .abbreviated, time: .shortened)).font(.system(size: 13)).foregroundStyle(Palette.ink3)

                    HStack(spacing: 14) {
                        MacroChip(color: Palette.protein, title: String(localized: "Protein"), value: "\(Int(entry.nutrients.proteinG)) g")
                        MacroChip(color: Palette.fat, title: String(localized: "Fat"), value: "\(Int(entry.nutrients.fatG)) g")
                        MacroChip(color: Palette.carb, title: String(localized: "Carbs"), value: "\(Int(entry.nutrients.carbG)) g")
                    }

                    feedbackCard

                    ForEach(entry.foodItems) { item in
                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Text(Names.pick(zh: item.nameZh, en: item.nameEn)).font(.system(size: 17, weight: .semibold))
                                Spacer()
                                Text("\(item.nutrients.kcal.kcalText) kcal").font(.number(15))
                            }
                            .foregroundStyle(Palette.ink)
                            ForEach(item.ingredients ?? [], id: \.id) { ing in
                                HStack {
                                    Text(Names.pick(zh: ing.nameZh, en: ing.nameEn)).font(.system(size: 14)).foregroundStyle(Palette.ink2)
                                    if ing.isCookingOil {
                                        Text("Cooking oil").font(.system(size: 10, weight: .semibold)).padding(.horizontal, 6).padding(.vertical, 2)
                                            .background(Palette.yolk.opacity(0.25), in: Capsule()).foregroundStyle(Color(hex: 0x8A5A00))
                                    }
                                    Spacer()
                                    Text("\(ing.grams.gramsText) g · \(ing.nutrients.kcal.kcalText) kcal").font(.number(12)).foregroundStyle(Palette.ink3)
                                }
                            }
                        }
                        .card()
                    }

                    Button("Edit") { editing = ReviewDraft(entry: entry) }
                        .buttonStyle(SecondaryButtonStyle())
                    Button("Delete this entry", role: .destructive) { confirmDelete = true }
                        .frame(maxWidth: .infinity).foregroundStyle(Palette.chili).padding(.vertical, 8)
                    Text("Nutrition values are estimates (usually within 20–40%).").font(.footnote).foregroundStyle(Palette.ink3)
                }
                .padding(16)
            } else if let error {
                Text(error).foregroundStyle(Palette.ink2).padding(40)
            } else {
                ProgressView().padding(80)
            }
        }
        .background(Palette.rice)
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .confirmationDialog("Delete this entry?", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button("Delete this entry", role: .destructive) { Task { await delete() } }
        } message: {
            Text("The photo and the recognition are deleted too.")
        }
        .sheet(item: Binding(get: { editing.map { IdentifiedDraft(draft: $0) } }, set: { if $0 == nil { editing = nil } })) { wrapped in
            ReviewView(draft: wrapped.draft, photo: nil, confirmTitle: String(localized: "Save changes"), saving: saving, error: error,
                       onConfirm: { Task { await saveEdit(wrapped.draft) } }, onCancel: { editing = nil })
        }
    }

    @ViewBuilder private var feedbackCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("Meal feedback", systemImage: "leaf").font(.system(size: 14, weight: .semibold)).foregroundStyle(Palette.tea)
            if let feedback {
                Text(feedback).font(.system(size: 15)).foregroundStyle(Palette.ink).accessibilityIdentifier("mealFeedback")
                Text("General diet information, not medical advice.").font(.system(size: 11)).foregroundStyle(Palette.ink3)
            } else {
                Text("Writing feedback for this meal, one moment please.").redacted(reason: .placeholder).shimmering()
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(16)
        .background(Palette.celadon, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .animation(.easeOut, value: feedback)
    }

    private func load() async {
        do {
            entry = try await app.api.mealEntry(id: entryID)
            feedback = try? await app.api.mealFeedback(id: entryID).text
        } catch let e as APIError {
            error = e.userMessage
        } catch {}
    }

    private func delete() async {
        do {
            try await app.api.deleteMealEntry(id: entryID)
            app.entriesChanged()
            dismiss()
        } catch let e as APIError {
            error = e.userMessage
        } catch {}
    }

    private func saveEdit(_ d: ReviewDraft) async {
        saving = true
        defer { saving = false }
        do {
            entry = try await app.api.updateMealEntry(id: entryID, d.input())
            editing = nil
            feedback = nil
            feedback = try? await app.api.mealFeedback(id: entryID).text
            app.entriesChanged()
        } catch let e as APIError {
            error = e.userMessage
        } catch {}
    }
}

struct IdentifiedDraft: Identifiable {
    var draft: ReviewDraft
    var id: ObjectIdentifier { ObjectIdentifier(draft) }
}

/// Manual input: search foods and enter grams. No recognition quota.
struct ManualEntrySheet: View {
    var mealType: MealType
    var onSaved: () -> Void
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var draft: ReviewDraft
    @State private var saving = false
    @State private var error: String?
    @State private var key = UUID().uuidString

    init(mealType: MealType, onSaved: @escaping () -> Void) {
        self.mealType = mealType
        self.onSaved = onSaved
        _draft = State(initialValue: ReviewDraft(items: [], mealType: mealType, eatenAt: .now, recognitionID: nil))
    }

    var body: some View {
        ReviewView(draft: draft, photo: nil, confirmTitle: String(localized: "Confirm and log"), saving: saving, error: error,
                   onConfirm: { Task { await save() } }, onCancel: { dismiss() })
    }

    private func save() async {
        saving = true
        defer { saving = false }
        do {
            _ = try await app.api.createMealEntry(draft.input(), idempotencyKey: key)
            onSaved()
            dismiss()
        } catch let e as APIError {
            error = e.userMessage
        } catch {}
    }
}
