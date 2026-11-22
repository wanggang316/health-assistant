import Foundation
import Security

/// Keeps the session tokens in the Keychain: this device only, after first
/// unlock, no iCloud sync (§9.5).
final class KeychainTokenStore: TokenStore {
    private let service = "com.gumpw.health-assistant.session"
    private let account = "tokens"
    private var cached: Tokens?
    private var loaded = false

    init() {
        // Keychain items survive app deletion; drop them on the first launch.
        let flag = "keychainInitialized"
        if !UserDefaults.standard.bool(forKey: flag) {
            delete()
            UserDefaults.standard.set(true, forKey: flag)
        }
    }

    var tokens: Tokens? {
        get {
            if !loaded {
                cached = read()
                loaded = true
            }
            return cached
        }
        set {
            cached = newValue
            loaded = true
            if let newValue { write(newValue) } else { delete() }
        }
    }

    private var query: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account]
    }

    private func read() -> Tokens? {
        var q = query
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: AnyObject?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data else { return nil }
        return try? APIClient.decoder.decode(Tokens.self, from: data)
    }

    private func write(_ t: Tokens) {
        guard let data = try? APIClient.encoder.encode(t) else { return }
        let attrs: [String: Any] = [kSecValueData as String: data,
                                    kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly]
        if SecItemUpdate(query as CFDictionary, attrs as CFDictionary) == errSecItemNotFound {
            var q = query
            q.merge(attrs) { $1 }
            SecItemAdd(q as CFDictionary, nil)
        }
    }

    private func delete() {
        SecItemDelete(query as CFDictionary)
    }
}

/// In-memory store for tests and previews.
final class MemoryTokenStore: TokenStore {
    var tokens: Tokens?
    init(tokens: Tokens? = nil) { self.tokens = tokens }
}
