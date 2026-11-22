import AuthenticationServices
import GoogleSignIn
import SwiftUI

struct MeView: View {
    @Environment(AppModel.self) private var app
    @State private var editing = false
    @State private var deleting = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            List {
                Section {
                    HStack(spacing: 14) {
                        Image(systemName: "person.crop.circle.fill").font(.system(size: 44)).foregroundStyle(Palette.tea)
                        VStack(alignment: .leading) {
                            Text(app.me?.user.displayName ?? String(localized: "Signed in")).font(.system(size: 18, weight: .semibold))
                            Text(app.me?.user.provider == "apple" ? "Apple" : "Google").font(.system(size: 13)).foregroundStyle(Palette.ink3)
                        }
                    }
                }
                Section("Daily target") {
                    if let t = app.me?.dailyTarget {
                        LabeledContent("Energy") { Text("\(t.kcal.kcalText) kcal").font(.number(15)) }
                        LabeledContent("Protein") { Text("\(Int(t.proteinG)) g").font(.number(15)) }
                        LabeledContent("Fat") { Text("\(Int(t.fatG)) g").font(.number(15)) }
                        LabeledContent("Carbs") { Text("\(Int(t.carbG)) g").font(.number(15)) }
                    } else {
                        Text(app.me?.profile == nil ? String(localized: "Add your profile to get a daily target.") : String(localized: "No target under 18."))
                            .foregroundStyle(Palette.ink2)
                    }
                    Button(app.me?.profile == nil ? String(localized: "Set up profile") : String(localized: "Edit profile")) { editing = true }
                }
                Section("About") {
                    LabeledContent("Recognitions today") {
                        Text(app.me.map { "\($0.recognitionQuota.used) / \($0.recognitionQuota.limit)" } ?? "–").font(.number(15))
                    }
                    Text("Nutrition values are estimates from a photo and the USDA FoodData Central database. They are usually within 20–40% of the true value. This app gives general diet information, not medical advice.")
                        .font(.footnote).foregroundStyle(Palette.ink2)
                    Link("Privacy policy", destination: URL(string: "https://health-api.gumpw.com/privacy")!)
                }
                Section {
                    Button("Sign out") { Task { await app.signOut() } }
                    Button("Delete account", role: .destructive) { deleting = true }
                }
                if let error { Text(error).foregroundStyle(Palette.chili).font(.footnote) }
            }
            .scrollContentBackground(.hidden)
            .background(Palette.rice)
            .navigationTitle("Me")
            .sheet(isPresented: $editing) { ProfileEditSheet() }
            .sheet(isPresented: $deleting) { DeleteAccountSheet() }
        }
    }
}

struct ProfileEditSheet: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var form = ProfileForm()
    @State private var split = MacroSplit.standard
    @State private var saving = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            Form {
                Picker("Sex", selection: $form.sex) {
                    Text("Male").tag(Sex.male)
                    Text("Female").tag(Sex.female)
                }
                Stepper("Birth year: \(String(form.birthYear))", value: $form.birthYear, in: 1930...(Calendar.current.component(.year, from: .now) - 10))
                Stepper("Height: \(Int(form.heightCm)) cm", value: $form.heightCm, in: 100...250)
                Stepper("Weight: \(String(format: "%.1f", form.weightKg)) kg", value: $form.weightKg, in: 30...300, step: 0.5)
                Picker("Activity", selection: $form.activity) { ForEach(ActivityLevel.allCases, id: \.self) { Text($0.title).tag($0) } }
                Picker("Goal", selection: $form.goal) { ForEach(Goal.allCases, id: \.self) { Text($0.title).tag($0) } }
                Section("Macro split (energy %)") {
                    Stepper("Protein \(split.proteinPct)%", value: $split.proteinPct, in: 5...60, step: 5)
                    Stepper("Fat \(split.fatPct)%", value: $split.fatPct, in: 5...60, step: 5)
                    LabeledContent("Carbs", value: "\(100 - split.proteinPct - split.fatPct)%")
                }
                if let error { Text(error).foregroundStyle(Palette.chili) }
            }
            .navigationTitle("Profile")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") { Task { await save() } }.disabled(saving || 100 - split.proteinPct - split.fatPct < 5)
                }
            }
            .onAppear {
                if let p = app.me?.profile { form = ProfileForm(p); split = p.macroSplit ?? .standard }
            }
        }
    }

    private func save() async {
        saving = true
        defer { saving = false }
        var s = split
        s.carbPct = 100 - s.proteinPct - s.fatPct
        do {
            app.me = try await app.api.putProfile(form.dto(split: s))
            app.entriesChanged()
            dismiss()
        } catch let e as APIError {
            error = e.userMessage
        } catch {}
    }
}

struct DeleteAccountSheet: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var working = false
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Delete account").font(.system(size: 24, weight: .bold)).foregroundStyle(Palette.ink)
            Text("This deletes, and cannot be undone:").foregroundStyle(Palette.ink2)
            ForEach([String(localized: "All meal entries and meal photos"), String(localized: "Your profile and daily target"),
                     String(localized: "Health advice"), String(localized: "Your sign-in link with Apple or Google")], id: \.self) { item in
                HStack(alignment: .top, spacing: 10) {
                    Circle().fill(Palette.chili).frame(width: 6, height: 6).padding(.top, 7)
                    Text(item).foregroundStyle(Palette.ink)
                }
            }
            Text("Backups keep deleted data for up to 30 days.").font(.footnote).foregroundStyle(Palette.ink3)
            if let error { Text(error).font(.footnote).foregroundStyle(Palette.chili) }
            Spacer()
            if app.me?.user.provider == "apple" {
                SignInWithAppleButton(.continue) { req in req.requestedScopes = [] } onCompletion: { result in
                    guard case .success(let auth) = result, let cred = auth.credential as? ASAuthorizationAppleIDCredential,
                          let code = cred.authorizationCode.flatMap({ String(data: $0, encoding: .utf8) }) else { return }
                    Task { await delete(appleCode: code) }
                }
                .signInWithAppleButtonStyle(.black)
                .frame(height: 54).clipShape(RoundedRectangle(cornerRadius: 16))
                Text("Confirm with Apple to delete the account.").font(.footnote).foregroundStyle(Palette.ink3)
            } else {
                Button("Delete account") { Task { await delete(appleCode: nil) } }
                    .buttonStyle(PrimaryButtonStyle(color: Palette.chili))
                    .disabled(working)
                    .accessibilityIdentifier("confirmDeleteAccount")
            }
            Button("Cancel") { dismiss() }.frame(maxWidth: .infinity).foregroundStyle(Palette.ink2)
        }
        .padding(24)
        .background(Palette.rice)
        .presentationDetents([.large])
    }

    private func delete(appleCode: String?) async {
        working = true
        defer { working = false }
        do {
            try await app.api.deleteAccount(appleAuthorizationCode: appleCode)
            if app.me?.user.provider == "google" { try? await GIDSignIn.sharedInstance.disconnect() }
            dismiss()
            app.signedOutLocally()
        } catch let e as APIError {
            error = e.userMessage
        } catch {}
    }
}
