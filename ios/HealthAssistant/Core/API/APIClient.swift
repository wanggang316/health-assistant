import Foundation

nonisolated enum APIError: Error, Equatable, Sendable {
    case server(status: Int, code: String, message: String)
    case transport(String)
    case decoding(String)
    case signedOut

    var code: String {
        switch self {
        case .server(_, let code, _): code
        case .transport: "network"
        case .decoding: "decoding"
        case .signedOut: "session_revoked"
        }
    }

    /// Text for the user, by error code (§5.1).
    var userMessage: String {
        switch code {
        case "quota_exceeded": String(localized: "You used today's recognitions. Add the meal by hand, or try again tomorrow.")
        case "model_unavailable": String(localized: "Recognition is busy now. Try again in a moment.")
        case "model_output_invalid": String(localized: "We could not read this photo. Try another angle with more light.")
        case "invalid_photo": String(localized: "This photo cannot be used. Take a new one.")
        case "payload_too_large": String(localized: "The photo is too large.")
        case "unresolved_ingredient": String(localized: "Choose a food for every ingredient before you save.")
        case "network": String(localized: "No connection. Check the network and try again.")
        case "apple_reauth_required": String(localized: "Sign in with Apple again to confirm.")
        default: String(localized: "Something went wrong. Try again.")
        }
    }
}

/// Stores and refreshes the session tokens.
protocol TokenStore: AnyObject {
    var tokens: Tokens? { get set }
}

/// JSON client for the backend. One refresh runs at a time; other requests
/// wait for it and retry once (§9.4).
final class APIClient {
    let baseURL: URL
    private let session: URLSession
    private let store: TokenStore
    private var refreshTask: Task<Tokens, Error>?
    var onSignedOut: (() -> Void)?

    static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            if let date = ISO8601.parse(s) { return date }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "bad date \(s)"))
        }
        return d
    }()

    static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.keyEncodingStrategy = .convertToSnakeCase
        e.dateEncodingStrategy = .custom { date, encoder in
            var c = encoder.singleValueContainer()
            try c.encode(ISO8601.format(date))
        }
        return e
    }()

    init(baseURL: URL, store: TokenStore, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.store = store
        self.session = session
    }

    // MARK: Requests

    func send<T: Decodable>(_ method: String, _ path: String, body: (any Encodable)? = nil, headers: [String: String] = [:],
                            authenticated: Bool = true, as: T.Type = T.self) async throws -> T {
        let data = try await raw(method, path, body: body.map { try Self.encoder.encode($0) }, contentType: "application/json",
                                 headers: headers, authenticated: authenticated)
        do {
            return try Self.decoder.decode(T.self, from: data)
        } catch {
            throw APIError.decoding(String(describing: error))
        }
    }

    func sendNoContent(_ method: String, _ path: String, body: (any Encodable)? = nil) async throws {
        _ = try await raw(method, path, body: body.map { try Self.encoder.encode($0) }, contentType: "application/json",
                          headers: [:], authenticated: true)
    }

    func raw(_ method: String, _ path: String, body: Data?, contentType: String, headers: [String: String],
             authenticated: Bool, timeout: TimeInterval = 30, retried: Bool = false) async throws -> Data {
        var req = URLRequest(url: baseURL.appending(path: path.components(separatedBy: "?")[0]), timeoutInterval: timeout)
        if let q = path.split(separator: "?", maxSplits: 1).dropFirst().first {
            var comps = URLComponents(url: req.url!, resolvingAgainstBaseURL: false)!
            comps.percentEncodedQuery = String(q)
            req.url = comps.url
        }
        req.httpMethod = method
        req.httpBody = body
        if body != nil { req.setValue(contentType, forHTTPHeaderField: "Content-Type") }
        req.setValue(Self.languageHeader, forHTTPHeaderField: "Accept-Language")
        req.setValue(String(TimeZone.current.secondsFromGMT() / 60), forHTTPHeaderField: "X-Utc-Offset-Minutes")
        for (k, v) in headers { req.setValue(v, forHTTPHeaderField: k) }
        if authenticated {
            guard let tokens = store.tokens else { throw APIError.signedOut }
            req.setValue("Bearer \(tokens.accessToken)", forHTTPHeaderField: "Authorization")
        }
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: req)
        } catch {
            throw APIError.transport(error.localizedDescription)
        }
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        if (200..<300).contains(status) { return data }
        let err = (try? Self.decoder.decode(ErrorBody.self, from: data))?.error
        let code = err?.code ?? "internal"
        if authenticated && status == 401 && code == "token_expired" && !retried {
            _ = try await refresh()
            return try await raw(method, path, body: body, contentType: contentType, headers: headers,
                                 authenticated: authenticated, timeout: timeout, retried: true)
        }
        if authenticated && status == 401 && (code == "session_revoked" || code == "unauthenticated") {
            store.tokens = nil
            onSignedOut?()
            throw APIError.signedOut
        }
        throw APIError.server(status: status, code: code, message: err?.message ?? "")
    }

    /// Rotates the tokens. Concurrent callers share one request.
    func refresh() async throws -> Tokens {
        if let task = refreshTask { return try await task.value }
        guard let current = store.tokens else { throw APIError.signedOut }
        let task = Task { () throws -> Tokens in
            struct Body: Encodable { var refreshToken: String }
            let data = try await raw("POST", "/v1/auth/refresh", body: try Self.encoder.encode(Body(refreshToken: current.refreshToken)),
                                     contentType: "application/json", headers: [:], authenticated: false)
            return try Self.decoder.decode(Tokens.self, from: data)
        }
        refreshTask = task
        defer { refreshTask = nil }
        do {
            let t = try await task.value
            store.tokens = t
            return t
        } catch let e as APIError where e.code == "session_revoked" {
            store.tokens = nil
            onSignedOut?()
            throw APIError.signedOut
        }
    }

    static var languageHeader: String {
        let lang = Bundle.main.preferredLocalizations.first ?? "en"
        return lang.hasPrefix("zh") ? "zh-Hans" : "en"
    }

    // MARK: Multipart upload

    func uploadRecognition(jpeg: Data, takenAt: Date, offsetMinutes: Int, idempotencyKey: String) async throws -> RecognitionDTO {
        let boundary = "ha-\(UUID().uuidString)"
        var body = Data()
        func line(_ s: String) { body.append(Data((s + "\r\n").utf8)) }
        line("--\(boundary)")
        line("Content-Disposition: form-data; name=\"meta\"")
        line("Content-Type: application/json")
        line("")
        line("{\"taken_at\":\"\(ISO8601.format(takenAt))\",\"utc_offset_minutes\":\(offsetMinutes)}")
        line("--\(boundary)")
        line("Content-Disposition: form-data; name=\"photo\"; filename=\"meal.jpg\"")
        line("Content-Type: image/jpeg")
        line("")
        body.append(jpeg)
        line("")
        line("--\(boundary)--")
        let data = try await raw("POST", "/v1/recognitions", body: body, contentType: "multipart/form-data; boundary=\(boundary)",
                                 headers: ["Idempotency-Key": idempotencyKey], authenticated: true, timeout: 75)
        do {
            return try Self.decoder.decode(RecognitionDTO.self, from: data)
        } catch {
            throw APIError.decoding(String(describing: error))
        }
    }
}

nonisolated enum ISO8601 {
    private static let withFraction = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
    private static let plain = Date.ISO8601FormatStyle()

    static func parse(_ s: String) -> Date? { (try? withFraction.parse(s)) ?? (try? plain.parse(s)) }
    static func format(_ d: Date) -> String { plain.format(d) }
}
