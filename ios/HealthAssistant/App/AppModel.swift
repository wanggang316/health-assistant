import AuthenticationServices
import Foundation
import Observation

/// App-wide session state (§9.3).
@Observable
final class AppModel {
    enum Phase: Equatable { case launching, signedOut, onboarding, signedIn }

    var phase: Phase = .launching
    var me: MeResponse?
    /// Bumped after a meal entry changes, so that screens reload.
    var dataVersion = 0

    let api: APIClient
    private let tokens: TokenStore

    init(api: APIClient, tokens: TokenStore) {
        self.api = api
        self.tokens = tokens
        api.onSignedOut = { [weak self] in self?.signedOutLocally() }
    }

    static func live() -> AppModel {
        let url = (Bundle.main.object(forInfoDictionaryKey: "APIBaseURL") as? String).flatMap(URL.init(string:))
            ?? URL(string: "https://health-api.gumpw.com")!
        let store = KeychainTokenStore()
        if ProcessInfo.processInfo.arguments.contains("-UITestReset") { store.tokens = nil }
        return AppModel(api: APIClient(baseURL: url, store: store), tokens: store)
    }

    var isLocalServer: Bool {
        let host = api.baseURL.host() ?? ""
        return host == "127.0.0.1" || host == "localhost"
    }

    func start() async {
        guard tokens.tokens != nil else {
            phase = .signedOut
            return
        }
        await reloadMe()
    }

    func reloadMe() async {
        do {
            let m = try await api.me()
            me = m
            if phase != .signedIn { phase = m.profile == nil && !UserDefaults.standard.bool(forKey: skippedKey(m.user.id)) ? .onboarding : .signedIn }
        } catch APIError.signedOut {
            phase = .signedOut
        } catch {
            // Online only: keep the session and show the main screens; they show retry states.
            if phase == .launching { phase = .signedIn }
        }
    }

    func didSignIn(_ r: SignInResponse) async {
        tokens.tokens = r.tokens
        phase = .launching
        await reloadMe()
    }

    func skipOnboarding() {
        if let id = me?.user.id { UserDefaults.standard.set(true, forKey: skippedKey(id)) }
        phase = .signedIn
    }

    func finishOnboarding(_ m: MeResponse) {
        me = m
        phase = .signedIn
    }

    private func skippedKey(_ userID: String) -> String { "onboardingSkipped.\(userID)" }

    func signOut() async {
        try? await api.logout()
        signedOutLocally()
    }

    func signedOutLocally() {
        tokens.tokens = nil
        me = nil
        phase = .signedOut
    }

    func entriesChanged() {
        dataVersion += 1
        Task { await reloadMe() }
    }
}
