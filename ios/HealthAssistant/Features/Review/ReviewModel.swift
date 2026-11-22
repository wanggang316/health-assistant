import Foundation
import Observation

/// Editable copy of a recognition, or of a meal entry, or a manual entry. The
/// app recalculates nutrients while the user edits; the server recalculates
/// on save (§9.3).
struct DraftIngredient: Identifiable, Hashable {
    let id = UUID()
    var nameZh: String
    var nameEn: String
    var grams: Double
    var isCookingOil: Bool
    var food: FoodRef?
    var candidates: [FoodRef] = []
    var recognizedGrams: Double?

    var nutrients: Nutrients? { food.map { $0.per100g.scaled(grams: grams) } }
    var name: String { Names.pick(zh: nameZh, en: nameEn) }
}

struct DraftItem: Identifiable, Hashable {
    let id = UUID()
    var nameZh: String
    var nameEn: String
    var cookingMethod: String
    var modelKcal: Double?
    var serverGramCheck: Bool
    var ingredients: [DraftIngredient]

    var name: String { Names.pick(zh: nameZh, en: nameEn) }
    var nutrients: Nutrients { ingredients.compactMap(\.nutrients).reduce(.zero, +) }
    var unresolved: Bool { ingredients.contains { $0.food == nil } }
    /// The warning stays while the recognized grams are unchanged (§7.4).
    var needsGramCheck: Bool { serverGramCheck && ingredients.allSatisfy { $0.recognizedGrams == $0.grams } }
}

@Observable
final class ReviewDraft {
    var items: [DraftItem]
    var mealType: MealType
    var eatenAt: Date
    var recognitionID: String?

    init(items: [DraftItem], mealType: MealType, eatenAt: Date, recognitionID: String?) {
        self.items = items
        self.mealType = mealType
        self.eatenAt = eatenAt
        self.recognitionID = recognitionID
    }

    convenience init(recognition r: RecognitionDTO, takenAt: Date) {
        let items = r.foodItems.map { fi in
            DraftItem(nameZh: fi.nameZh, nameEn: fi.nameEn, cookingMethod: fi.cookingMethod, modelKcal: fi.modelKcal,
                      serverGramCheck: fi.needsGramCheck,
                      ingredients: fi.ingredients.map {
                          DraftIngredient(nameZh: $0.nameZh, nameEn: $0.nameEn, grams: $0.grams, isCookingOil: $0.isCookingOil,
                                          food: $0.food, candidates: $0.candidates, recognizedGrams: $0.grams)
                      })
        }
        self.init(items: items, mealType: r.suggestedMealType, eatenAt: takenAt, recognitionID: r.id)
    }

    convenience init(entry e: MealEntryDTO) {
        let items = e.foodItems.map { fi in
            DraftItem(nameZh: fi.nameZh, nameEn: fi.nameEn, cookingMethod: fi.cookingMethod, modelKcal: nil, serverGramCheck: fi.needsGramCheck,
                      ingredients: (fi.ingredients ?? []).map {
                          let per100 = $0.grams > 0 ? $0.nutrients.scaled(grams: 100 * 100 / $0.grams) : $0.nutrients
                          return DraftIngredient(nameZh: $0.nameZh, nameEn: $0.nameEn, grams: $0.grams, isCookingOil: $0.isCookingOil,
                                                 food: FoodRef(fdcId: $0.fdcId, description: $0.fdcDescription, per100g: per100),
                                                 recognizedGrams: fi.needsGramCheck ? $0.grams : nil)
                      })
        }
        self.init(items: items, mealType: e.mealType, eatenAt: e.eatenAt, recognitionID: nil)
    }

    var total: Nutrients { items.map(\.nutrients).reduce(.zero, +) }
    var canSave: Bool { !items.isEmpty && !items.contains(where: \.unresolved) && items.allSatisfy { !$0.ingredients.isEmpty } }

    func input() -> MealEntryInput {
        MealEntryInput(
            recognitionId: recognitionID, mealType: mealType, eatenAt: eatenAt,
            utcOffsetMinutes: TimeZone.current.secondsFromGMT(for: eatenAt) / 60,
            foodItems: items.map { item in
                FoodItemInput(nameZh: item.nameZh, nameEn: item.nameEn, cookingMethod: item.cookingMethod,
                              ingredients: item.ingredients.map {
                                  IngredientInput(nameZh: $0.nameZh, nameEn: $0.nameEn, grams: $0.grams, fdcId: $0.food?.fdcId, isCookingOil: $0.isCookingOil)
                              })
            })
    }
}
