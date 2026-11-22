import AuthenticationServices
import GoogleSignIn
import SwiftUI

struct SignInView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var serverNonce: String?
    @State private var busy = false
    @State private var error: String?
    @State private var float = false

    var body: some View {
        VStack(spacing: 0) {
            Spacer(minLength: 20)
            Image("Hero")
                .resizable()
                .scaledToFit()
                .frame(maxWidth: 320)
                .mask(RadialGradient(colors: [.black, .black, .clear], center: .center, startRadius: 90, endRadius: 170))
                .offset(y: float ? -6 : 6)
                .animation(reduceMotion ? nil : .easeInOut(duration: 3).repeatForever(autoreverses: true), value: float)
                .onAppear { float = true }
                .accessibilityHidden(true)
            VStack(spacing: 10) {
                Text("Log meals with one photo")
                    .font(.system(size: 30, weight: .bold))
                    .foregroundStyle(Palette.ink)
                    .multilineTextAlignment(.center)
                Text("Take a photo, check the dishes, and see how your day adds up.")
                    .font(.system(size: 16))
                    .foregroundStyle(Palette.ink2)
                    .multilineTextAlignment(.center)
            }
            .padding(.horizontal, 28)
            Spacer()
            VStack(spacing: 12) {
                SignInWithAppleButton(.signIn) { request in
                    request.requestedScopes = [.fullName, .email]
                    if let n = serverNonce { request.nonce = NonceHash.sha256Hex(n) }
                } onCompletion: { result in
                    Task { await handleApple(result) }
                }
                .signInWithAppleButtonStyle(.black)
                .frame(height: 54)
                .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
                .disabled(serverNonce == nil || busy)

                Button {
                    Task { await signInGoogle() }
                } label: {
                    HStack(spacing: 10) {
                        Image(systemName: "g.circle.fill").font(.system(size: 20))
                        Text("Sign in with Google")
                    }
                }
                .buttonStyle(SecondaryButtonStyle())
                .disabled(busy)

                if model.isLocalServer {
                    Button("Developer sign-in") { Task { await devSignIn() } }
                        .font(.footnote)
                        .foregroundStyle(Palette.ink3)
                        .accessibilityIdentifier("devSignIn")
                }
                if let error {
                    Text(error).font(.footnote).foregroundStyle(Palette.chili).multilineTextAlignment(.center)
                }
                Text("Nutrition values are estimates. This app does not give medical advice.")
                    .font(.system(size: 12))
                    .foregroundStyle(Palette.ink3)
                    .multilineTextAlignment(.center)
                    .padding(.top, 4)
            }
            .padding(.horizontal, 24)
            .padding(.bottom, 24)
        }
        .overlay { if busy { ProgressView().controlSize(.large).tint(Palette.tea) } }
        .task { await loadNonce() }
    }

    private func loadNonce() async {
        do { serverNonce = try await model.api.nonce().nonce } catch { self.error = (error as? APIError)?.userMessage }
    }

    private func handleApple(_ result: Result<ASAuthorization, Error>) async {
        guard case .success(let auth) = result,
              let cred = auth.credential as? ASAuthorizationAppleIDCredential,
              let tokenData = cred.identityToken, let token = String(data: tokenData, encoding: .utf8),
              let nonce = serverNonce else {
            if case .failure(let e) = result, (e as? ASAuthorizationError)?.code == .canceled { return }
            error = String(localized: "Sign in with Apple failed. Try again.")
            return
        }
        busy = true
        defer { busy = false }
        do {
            let code = cred.authorizationCode.flatMap { String(data: $0, encoding: .utf8) }
            let r = try await model.api.signInApple(identityToken: token, authorizationCode: code, nonce: nonce,
                                                    givenName: cred.fullName?.givenName, familyName: cred.fullName?.familyName)
            UserDefaults.standard.set(cred.user, forKey: "appleUserID")
            await model.didSignIn(r)
        } catch {
            self.error = (error as? APIError)?.userMessage
            await loadNonce()
        }
    }

    private func signInGoogle() async {
        guard let root = UIApplication.shared.connectedScenes.compactMap({ ($0 as? UIWindowScene)?.keyWindow?.rootViewController }).first else { return }
        busy = true
        defer { busy = false }
        do {
            let nonce = try await model.api.nonce().nonce
            let result = try await GIDSignIn.sharedInstance.signIn(withPresenting: root, hint: nil, additionalScopes: nil,
                                                                  nonce: NonceHash.sha256Hex(nonce))
            guard let idToken = result.user.idToken?.tokenString else { return }
            let r = try await model.api.signInGoogle(idToken: idToken, nonce: nonce)
            await model.didSignIn(r)
        } catch let e as APIError {
            error = e.userMessage
        } catch {
            if (error as NSError).code != GIDSignInError.canceled.rawValue {
                self.error = String(localized: "Sign in with Google failed. Try again.")
            }
        }
    }

    private func devSignIn() async {
        busy = true
        defer { busy = false }
        do {
            let subject = ProcessInfo.processInfo.environment["DEV_SUBJECT"] ?? "simulator"
            await model.didSignIn(try await model.api.signInDev(subject: subject))
        } catch {
            self.error = (error as? APIError)?.userMessage
        }
    }
}
