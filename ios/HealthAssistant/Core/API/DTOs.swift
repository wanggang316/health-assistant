import Foundation

// Wire types of the REST API (docs/design/technical-design.md §5). Keys are
// snake_case on the wire; the decoder converts them.

nonisolated struct Nutrients: Codable, Hashable, Sendable {
    var kcal: Double
    var proteinG: Double
    var fatG: Double
    var carbG: Double
    var fiberG: Double?
    var sodiumMg: Double?

    static let zero = Nutrients(kcal: 0, proteinG: 0, fatG: 0, carbG: 0)

    static func + (a: Nutrients, b: Nutrients) -> Nutrients {
        func add(_ x: Double?, _ y: Double?) -> Double? {
            if x == nil && y == nil { return nil }
            return (x ?? 0) + (y ?? 0)
        }
        return Nutrients(kcal: a.kcal + b.kcal, proteinG: a.proteinG + b.proteinG, fatG: a.fatG + b.fatG,
                         carbG: a.carbG + b.carbG, fiberG: add(a.fiberG, b.fiberG), sodiumMg: add(a.sodiumMg, b.sodiumMg))
    }

    func scaled(grams: Double) -> Nutrients {
        let k = grams / 100
        return Nutrients(kcal: kcal * k, proteinG: proteinG * k, fatG: fatG * k, carbG: carbG * k,
                         fiberG: fiberG.map { $0 * k }, sodiumMg: sodiumMg.map { $0 * k })
    }
}

nonisolated struct Tokens: Codable, Sendable, Equatable {
    var accessToken: String
    var accessExpiresAt: Date
    var refreshToken: String
    var refreshExpiresAt: Date
}

nonisolated struct NonceResponse: Codable, Sendable {
    var nonce: String
    var expiresAt: Date
}

nonisolated struct UserDTO: Codable, Sendable, Equatable {
    var id: String
    var provider: String
    var displayName: String?
    var email: String?
    var hasProfile: Bool?
}

nonisolated struct SignInResponse: Codable, Sendable {
    var accessToken: String
    var accessExpiresAt: Date
    var refreshToken: String
    var refreshExpiresAt: Date
    var user: UserDTO

    var tokens: Tokens { Tokens(accessToken: accessToken, accessExpiresAt: accessExpiresAt, refreshToken: refreshToken, refreshExpiresAt: refreshExpiresAt) }
}

nonisolated struct MacroSplit: Codable, Sendable, Equatable, Hashable {
    var proteinPct: Int
    var fatPct: Int
    var carbPct: Int
    static let standard = MacroSplit(proteinPct: 25, fatPct: 30, carbPct: 45)
}

nonisolated enum Sex: String, Codable, Sendable, CaseIterable { case male, female }
nonisolated enum ActivityLevel: String, Codable, Sendable, CaseIterable { case sedentary, light, moderate, active, veryActive = "very_active" }
nonisolated enum Goal: String, Codable, Sendable, CaseIterable { case lose, keep, gain }

nonisolated struct ProfileDTO: Codable, Sendable, Equatable {
    var sex: Sex
    var birthYear: Int
    var heightCm: Double
    var weightKg: Double
    var activityLevel: ActivityLevel
    var goal: Goal
    var macroSplit: MacroSplit?
}

nonisolated struct DailyTarget: Codable, Sendable, Equatable, Hashable {
    var kcal: Double
    var proteinG: Double
    var fatG: Double
    var carbG: Double
}

nonisolated struct QuotaDTO: Codable, Sendable, Equatable {
    var limit: Int
    var used: Int
    var resetsAt: Date
    var remaining: Int { max(limit - used, 0) }
}

nonisolated struct MeResponse: Codable, Sendable, Equatable {
    var user: UserDTO
    var profile: ProfileDTO?
    var dailyTarget: DailyTarget?
    var recognitionQuota: QuotaDTO
}

nonisolated struct FoodRef: Codable, Sendable, Hashable {
    var fdcId: Int64
    var description: String
    var per100g: Nutrients

    // convertFromSnakeCase turns "per_100g" into "per100G".
    enum CodingKeys: String, CodingKey {
        case fdcId, description
        case per100g = "per100G"
    }
}

nonisolated struct FoodSearchItem: Codable, Sendable, Hashable, Identifiable {
    var fdcId: Int64
    var description: String
    var dataType: String
    var per100g: Nutrients
    var id: Int64 { fdcId }

    enum CodingKeys: String, CodingKey {
        case fdcId, description, dataType
        case per100g = "per100G"
    }

    var ref: FoodRef { FoodRef(fdcId: fdcId, description: description, per100g: per100g) }
}

nonisolated struct FoodSearchResponse: Codable, Sendable {
    var foods: [FoodSearchItem]
}

nonisolated struct RecognizedIngredient: Codable, Sendable, Hashable {
    var nameZh: String
    var nameEn: String
    var grams: Double
    var isCookingOil: Bool
    var food: FoodRef?
    var nutrients: Nutrients?
    var matchMethod: String?
    var candidates: [FoodRef]
}

nonisolated struct RecognizedFoodItem: Codable, Sendable, Hashable {
    var nameZh: String
    var nameEn: String
    var cookingMethod: String
    var confidence: Double
    var modelKcal: Double
    var needsGramCheck: Bool
    var ingredients: [RecognizedIngredient]
    var nutrients: Nutrients
}

nonisolated struct RecognitionDTO: Codable, Sendable, Identifiable {
    var id: String
    var status: String
    var isFood: Bool
    var photoUrl: URL?
    var suggestedMealType: MealType
    var foodItems: [RecognizedFoodItem]
    var nutrients: Nutrients
    var mealEntryId: String?
    var recognitionQuota: QuotaDTO?
}

nonisolated enum MealType: String, Codable, Sendable, CaseIterable, Identifiable {
    case breakfast, lunch, dinner, snack
    var id: String { rawValue }
}

nonisolated struct EntryIngredientDTO: Codable, Sendable, Hashable {
    var id: String
    var nameZh: String
    var nameEn: String
    var grams: Double
    var fdcId: Int64
    var fdcDescription: String
    var isCookingOil: Bool
    var matchMethod: String
    var nutrients: Nutrients
}

nonisolated struct EntryFoodItemDTO: Codable, Sendable, Hashable, Identifiable {
    var id: String
    var nameZh: String
    var nameEn: String
    var cookingMethod: String
    var needsGramCheck: Bool
    var ingredients: [EntryIngredientDTO]?
    var nutrients: Nutrients
}

nonisolated struct MealEntryDTO: Codable, Sendable, Hashable, Identifiable {
    var id: String
    var source: String
    var recognitionId: String?
    var mealType: MealType
    var eatenAt: Date
    var utcOffsetMinutes: Int
    var entryDate: String
    var photoUrl: URL?
    var foodItems: [EntryFoodItemDTO]
    var nutrients: Nutrients
    var createdAt: Date
    var updatedAt: Date
}

nonisolated struct MealEntriesPage: Codable, Sendable {
    var mealEntries: [MealEntryDTO]
    var nextCursor: String?
}

nonisolated struct SummaryState: Codable, Sendable, Equatable {
    var state: String
    var text: String?
    var generatedAt: Date?
}

nonisolated struct DayDTO: Codable, Sendable {
    var date: String
    var dailyTarget: DailyTarget?
    var totals: Nutrients
    var mealEntries: [MealEntryDTO]
    var dailySummary: SummaryState
}

nonisolated struct DayTotalDTO: Codable, Sendable, Hashable {
    var date: String
    var kcal: Double
    var entryCount: Int
}

nonisolated struct DaysResponse: Codable, Sendable {
    var days: [DayTotalDTO]
}

nonisolated struct AdviceDTO: Codable, Sendable {
    var kind: String
    var text: String
    var generatedAt: Date
    var disclaimerRequired: Bool
}

// Request bodies

nonisolated struct IngredientInput: Codable, Sendable, Hashable {
    var nameZh: String
    var nameEn: String
    var grams: Double
    var fdcId: Int64?
    var isCookingOil: Bool
}

nonisolated struct FoodItemInput: Codable, Sendable, Hashable {
    var nameZh: String
    var nameEn: String
    var cookingMethod: String
    var ingredients: [IngredientInput]
}

nonisolated struct MealEntryInput: Codable, Sendable {
    var recognitionId: String?
    var mealType: MealType
    var eatenAt: Date
    var utcOffsetMinutes: Int
    var foodItems: [FoodItemInput]
}

nonisolated struct ErrorBody: Codable, Sendable {
    struct Inner: Codable, Sendable {
        var code: String
        var message: String
    }
    var error: Inner
    var requestId: String?
}
