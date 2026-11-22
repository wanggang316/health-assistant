import Foundation

extension APIClient {
    func nonce() async throws -> NonceResponse {
        try await send("POST", "/v1/auth/nonce", authenticated: false)
    }

    func signInApple(identityToken: String, authorizationCode: String?, nonce: String, givenName: String?, familyName: String?) async throws -> SignInResponse {
        struct Name: Encodable { var givenName: String?; var familyName: String? }
        struct Body: Encodable { var identityToken: String; var authorizationCode: String?; var nonce: String; var fullName: Name? }
        let name = (givenName == nil && familyName == nil) ? nil : Name(givenName: givenName, familyName: familyName)
        return try await send("POST", "/v1/auth/apple", body: Body(identityToken: identityToken, authorizationCode: authorizationCode, nonce: nonce, fullName: name), authenticated: false)
    }

    func signInGoogle(idToken: String, nonce: String) async throws -> SignInResponse {
        struct Body: Encodable { var idToken: String; var nonce: String }
        return try await send("POST", "/v1/auth/google", body: Body(idToken: idToken, nonce: nonce), authenticated: false)
    }

    func signInDev(subject: String) async throws -> SignInResponse {
        struct Body: Encodable { var subject: String }
        return try await send("POST", "/v1/auth/dev", body: Body(subject: subject), authenticated: false)
    }

    func logout() async throws {
        try await sendNoContent("POST", "/v1/auth/logout")
    }

    func me() async throws -> MeResponse {
        try await send("GET", "/v1/me")
    }

    func putProfile(_ p: ProfileDTO) async throws -> MeResponse {
        try await send("PUT", "/v1/me/profile", body: p)
    }

    func deleteAccount(appleAuthorizationCode: String?) async throws {
        struct Body: Encodable { var appleAuthorizationCode: String? }
        try await sendNoContent("DELETE", "/v1/me", body: Body(appleAuthorizationCode: appleAuthorizationCode))
    }

    func searchFoods(_ q: String) async throws -> [FoodSearchItem] {
        let enc = q.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed.subtracting(CharacterSet(charactersIn: "&=+"))) ?? ""
        let r: FoodSearchResponse = try await send("GET", "/v1/foods/search?q=\(enc)&limit=15")
        return r.foods
    }

    func createMealEntry(_ input: MealEntryInput, idempotencyKey: String) async throws -> MealEntryDTO {
        try await send("POST", "/v1/meal-entries", body: input, headers: ["Idempotency-Key": idempotencyKey])
    }

    func updateMealEntry(id: String, _ input: MealEntryInput) async throws -> MealEntryDTO {
        try await send("PUT", "/v1/meal-entries/\(id)", body: input)
    }

    func mealEntry(id: String) async throws -> MealEntryDTO {
        try await send("GET", "/v1/meal-entries/\(id)")
    }

    func deleteMealEntry(id: String) async throws {
        try await sendNoContent("DELETE", "/v1/meal-entries/\(id)")
    }

    func mealFeedback(id: String) async throws -> AdviceDTO {
        try await send("GET", "/v1/meal-entries/\(id)/feedback")
    }

    func day(_ date: String) async throws -> DayDTO {
        try await send("GET", "/v1/days/\(date)")
    }

    func daySummary(_ date: String) async throws -> AdviceDTO {
        try await send("GET", "/v1/days/\(date)/summary")
    }

    func days(from: String, to: String) async throws -> [DayTotalDTO] {
        let r: DaysResponse = try await send("GET", "/v1/days?from=\(from)&to=\(to)")
        return r.days
    }
}
