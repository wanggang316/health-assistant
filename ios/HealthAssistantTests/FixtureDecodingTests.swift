import Foundation
import Testing
@testable import HealthAssistant

/// Every API fixture in docs/api/fixtures must decode into the app DTOs.
struct FixtureDecodingTests {
    private func fixture(_ name: String) throws -> Data {
        let bundle = Bundle(for: BundleToken.self)
        let url = try #require(bundle.url(forResource: name, withExtension: "json", subdirectory: "fixtures"))
        return try Data(contentsOf: url)
    }

    @Test func signIn() throws {
        let r = try APIClient.decoder.decode(SignInResponse.self, from: fixture("auth_sign_in"))
        #expect(r.user.provider == "apple")
        #expect(r.user.hasProfile == false)
    }

    @Test func me() throws {
        let m = try APIClient.decoder.decode(MeResponse.self, from: fixture("me"))
        #expect(m.profile?.activityLevel == .light)
        #expect(m.dailyTarget?.kcal == 1779)
        let empty = try APIClient.decoder.decode(MeResponse.self, from: fixture("me_without_profile"))
        #expect(empty.profile == nil)
    }

    @Test func recognition() throws {
        let r = try APIClient.decoder.decode(RecognitionDTO.self, from: fixture("recognition"))
        #expect(r.suggestedMealType == .lunch)
        let egg = r.foodItems[0].ingredients[1]
        #expect(egg.food == nil)
        #expect(egg.candidates.first?.per100g.kcal == 149)
    }

    @Test func mealEntriesDaysAndAdvice() throws {
        let e = try APIClient.decoder.decode(MealEntryDTO.self, from: fixture("meal_entry"))
        #expect(e.foodItems[0].ingredients?.first?.isCookingOil == false)
        _ = try APIClient.decoder.decode(MealEntriesPage.self, from: fixture("meal_entries"))
        let d = try APIClient.decoder.decode(DayDTO.self, from: fixture("day"))
        #expect(d.dailySummary.state == "fresh")
        _ = try APIClient.decoder.decode(DaysResponse.self, from: fixture("days"))
        _ = try APIClient.decoder.decode(AdviceDTO.self, from: fixture("advice_meal_feedback"))
        _ = try APIClient.decoder.decode(FoodSearchResponse.self, from: fixture("foods_search"))
        _ = try APIClient.decoder.decode(ErrorBody.self, from: fixture("error"))
        _ = try APIClient.decoder.decode(NonceResponse.self, from: fixture("auth_nonce"))
    }

    @Test func mealEntryInputEncodesSnakeCase() throws {
        let input = MealEntryInput(recognitionId: "r", mealType: .lunch, eatenAt: Date(timeIntervalSince1970: 0), utcOffsetMinutes: 480,
                                   foodItems: [FoodItemInput(nameZh: "米饭", nameEn: "rice", cookingMethod: "steamed",
                                                             ingredients: [IngredientInput(nameZh: "米饭", nameEn: "rice", grams: 150, fdcId: 1, isCookingOil: false)])])
        let json = String(decoding: try APIClient.encoder.encode(input), as: UTF8.self)
        #expect(json.contains("\"recognition_id\":\"r\""))
        #expect(json.contains("\"fdc_id\":1"))
        #expect(json.contains("\"eaten_at\":\"1970-01-01T00:00:00Z\""))
    }
}

private final class BundleToken {}

struct DraftTests {
    @Test func gramCheckClearsWhenGramsChange() {
        let food = FoodRef(fdcId: 1, description: "Rice", per100g: Nutrients(kcal: 130, proteinG: 2.4, fatG: 0.2, carbG: 28.6))
        var item = DraftItem(nameZh: "米饭", nameEn: "rice", cookingMethod: "steamed", modelKcal: 500, serverGramCheck: true,
                             ingredients: [DraftIngredient(nameZh: "米饭", nameEn: "rice", grams: 200, isCookingOil: false, food: food, recognizedGrams: 200)])
        #expect(item.needsGramCheck)
        #expect(item.nutrients.kcal == 260)
        item.ingredients[0].grams = 300
        #expect(!item.needsGramCheck)
        #expect(abs(item.nutrients.kcal - 390) < 0.001)
    }

    @Test func unresolvedBlocksSave() {
        let d = ReviewDraft(items: [DraftItem(nameZh: "x", nameEn: "x", cookingMethod: "other", modelKcal: nil, serverGramCheck: false,
                                             ingredients: [DraftIngredient(nameZh: "x", nameEn: "x", grams: 10, isCookingOil: false, food: nil)])],
                            mealType: .lunch, eatenAt: .now, recognitionID: nil)
        #expect(!d.canSave)
    }

    @Test func targetMatchesServerRule() {
        #expect(TargetMath.kcal(sex: .male, age: 31, heightCm: 170, weightKg: 61, activity: .light, goal: .lose) == 1779)
    }

    @Test func nonceHash() {
        #expect(NonceHash.sha256Hex("abc") == "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
    }
}
