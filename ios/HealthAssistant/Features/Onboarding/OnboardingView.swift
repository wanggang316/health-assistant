import SwiftUI

/// Mifflin-St Jeor, the same rule as the server (ADR 0010); used for the
/// preview only. The server value is the one that counts.
enum TargetMath {
    static func kcal(sex: Sex, age: Int, heightCm: Double, weightKg: Double, activity: ActivityLevel, goal: Goal) -> Double {
        let bmr = 10 * weightKg + 6.25 * heightCm - 5 * Double(age) + (sex == .male ? 5 : -161)
        let af: Double = switch activity {
        case .sedentary: 1.2
        case .light: 1.375
        case .moderate: 1.55
        case .active: 1.725
        case .veryActive: 1.9
        }
        let gf: Double = switch goal {
        case .lose: 0.85
        case .keep: 1.0
        case .gain: 1.10
        }
        return (bmr * af * gf).rounded()
    }
}

extension ActivityLevel {
    var title: String {
        switch self {
        case .sedentary: String(localized: "Mostly sitting")
        case .light: String(localized: "Light activity")
        case .moderate: String(localized: "Moderate exercise")
        case .active: String(localized: "Active")
        case .veryActive: String(localized: "Very active")
        }
    }
    var detail: String {
        switch self {
        case .sedentary: String(localized: "Desk work, little walking")
        case .light: String(localized: "Walking or light exercise 1–3 days a week")
        case .moderate: String(localized: "Exercise 3–5 days a week")
        case .active: String(localized: "Hard exercise 6–7 days a week")
        case .veryActive: String(localized: "Physical work or training twice a day")
        }
    }
}

extension Goal {
    var title: String {
        switch self {
        case .lose: String(localized: "Lose weight")
        case .keep: String(localized: "Keep weight")
        case .gain: String(localized: "Gain weight")
        }
    }
}

struct ProfileForm: Equatable {
    var sex: Sex = .male
    var birthYear = Calendar.current.component(.year, from: .now) - 30
    var heightCm: Double = 170
    var weightKg: Double = 62
    var activity: ActivityLevel = .light
    var goal: Goal = .keep

    init() {}
    init(_ p: ProfileDTO) {
        sex = p.sex; birthYear = p.birthYear; heightCm = p.heightCm; weightKg = p.weightKg; activity = p.activityLevel; goal = p.goal
    }

    var age: Int { Calendar.current.component(.year, from: .now) - birthYear }
    var previewKcal: Double { TargetMath.kcal(sex: sex, age: age, heightCm: heightCm, weightKg: weightKg, activity: activity, goal: goal) }
    func dto(split: MacroSplit?) -> ProfileDTO {
        ProfileDTO(sex: sex, birthYear: birthYear, heightCm: heightCm, weightKg: weightKg, activityLevel: activity, goal: goal, macroSplit: split)
    }
}

struct OnboardingView: View {
    @Environment(AppModel.self) private var model
    @State private var step = 0
    @State private var form = ProfileForm()
    @State private var saving = false
    @State private var error: String?

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                ForEach(0..<3) { i in
                    Capsule().fill(i <= step ? Palette.tea : Palette.celadon).frame(height: 4)
                }
            }
            .padding(.horizontal, 24).padding(.top, 12)
            .animation(.spring(duration: 0.4), value: step)

            HStack {
                if step > 0 {
                    Button { withAnimation(.spring(duration: 0.45)) { step -= 1 } } label: { Image(systemName: "chevron.left") }
                        .foregroundStyle(Palette.ink2)
                }
                Spacer()
                Button("Skip for now") { model.skipOnboarding() }
                    .font(.system(size: 15)).foregroundStyle(Palette.ink3)
                    .accessibilityIdentifier("skipOnboarding")
            }
            .padding(.horizontal, 24).padding(.vertical, 12)

            ScrollView {
                Group {
                    switch step {
                    case 0: BodyStep(form: $form)
                    case 1: ActivityStep(form: $form)
                    default: GoalStep(form: $form)
                    }
                }
                .padding(.horizontal, 24)
                .transition(.asymmetric(insertion: .move(edge: .trailing).combined(with: .opacity), removal: .move(edge: .leading).combined(with: .opacity)))
                .id(step)
            }
            .scrollBounceBehavior(.basedOnSize)

            VStack(spacing: 8) {
                if let error { Text(error).font(.footnote).foregroundStyle(Palette.chili) }
                Button(step < 2 ? String(localized: "Continue") : String(localized: "Start logging")) {
                    if step < 2 { withAnimation(.spring(duration: 0.45)) { step += 1 } } else { Task { await save() } }
                }
                .buttonStyle(PrimaryButtonStyle())
                .disabled(saving)
                .accessibilityIdentifier("onboardingNext")
            }
            .padding(24)
        }
        .background(Palette.rice)
    }

    private func save() async {
        saving = true
        defer { saving = false }
        do {
            model.finishOnboarding(try await model.api.putProfile(form.dto(split: nil)))
        } catch {
            self.error = (error as? APIError)?.userMessage
        }
    }
}

private struct BodyStep: View {
    @Binding var form: ProfileForm
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("About you").font(.system(size: 30, weight: .bold)).foregroundStyle(Palette.ink)
            Text("We use this to set a daily energy target. You can change it later.").foregroundStyle(Palette.ink2)
            Picker("Sex", selection: $form.sex) {
                Text("Male").tag(Sex.male)
                Text("Female").tag(Sex.female)
            }
            .pickerStyle(.segmented)
            StepperRow(title: String(localized: "Birth year"), value: Binding(get: { Double(form.birthYear) }, set: { form.birthYear = Int($0) }),
                       range: 1930...Double(Calendar.current.component(.year, from: .now) - 10), step: 1, unit: "", format: { String(Int($0)) })
            StepperRow(title: String(localized: "Height"), value: $form.heightCm, range: 100...250, step: 1, unit: "cm", format: { String(Int($0)) })
            StepperRow(title: String(localized: "Weight"), value: $form.weightKg, range: 30...300, step: 0.5, unit: "kg", format: { String(format: "%.1f", $0) })
            Text("Targets are not meant for pregnancy, breastfeeding, or medical conditions, and are not given under 18.")
                .font(.footnote).foregroundStyle(Palette.ink3)
        }
    }
}

struct StepperRow: View {
    var title: String
    @Binding var value: Double
    var range: ClosedRange<Double>
    var step: Double
    var unit: String
    var format: (Double) -> String

    var body: some View {
        HStack {
            Text(title).foregroundStyle(Palette.ink)
            Spacer()
            Button { value = max(range.lowerBound, value - step) } label: { Image(systemName: "minus") }
                .frame(width: 40, height: 40).background(Palette.celadon, in: Circle())
            Text("\(format(value)) \(unit)")
                .font(.number(20))
                .frame(minWidth: 90)
                .contentTransition(.numericText(value: value))
                .animation(.snappy, value: value)
            Button { value = min(range.upperBound, value + step) } label: { Image(systemName: "plus") }
                .frame(width: 40, height: 40).background(Palette.celadon, in: Circle())
        }
        .foregroundStyle(Palette.tea)
        .card(radius: 18)
    }
}

private struct ActivityStep: View {
    @Binding var form: ProfileForm
    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("How active are you?").font(.system(size: 30, weight: .bold)).foregroundStyle(Palette.ink)
            ForEach(ActivityLevel.allCases, id: \.self) { a in
                Button { withAnimation(.snappy) { form.activity = a } } label: {
                    HStack {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(a.title).font(.system(size: 17, weight: .semibold)).foregroundStyle(Palette.ink)
                            Text(a.detail).font(.system(size: 14)).foregroundStyle(Palette.ink2)
                        }
                        Spacer()
                        Image(systemName: form.activity == a ? "checkmark.circle.fill" : "circle")
                            .foregroundStyle(form.activity == a ? Palette.tea : Palette.ink3)
                            .font(.system(size: 22))
                    }
                    .card(radius: 18)
                    .overlay(RoundedRectangle(cornerRadius: 18).stroke(form.activity == a ? Palette.tea : .clear, lineWidth: 2))
                }
                .buttonStyle(.plain)
            }
        }
    }
}

private struct GoalStep: View {
    @Binding var form: ProfileForm
    @State private var shown: Double = 0

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Your goal").font(.system(size: 30, weight: .bold)).foregroundStyle(Palette.ink)
            Picker("Goal", selection: $form.goal) {
                ForEach(Goal.allCases, id: \.self) { Text($0.title).tag($0) }
            }
            .pickerStyle(.segmented)
            VStack(spacing: 6) {
                Text("Daily target").font(.system(size: 14)).foregroundStyle(Palette.ink2)
                Text("\(Int(shown))")
                    .font(.number(56, weight: .bold))
                    .foregroundStyle(Palette.tea)
                    .contentTransition(.numericText(value: shown))
                Text("kcal").font(.system(size: 15)).foregroundStyle(Palette.ink2)
                HStack(spacing: 16) {
                    MacroChip(color: Palette.protein, title: String(localized: "Protein"), value: "25%")
                    MacroChip(color: Palette.fat, title: String(localized: "Fat"), value: "30%")
                    MacroChip(color: Palette.carb, title: String(localized: "Carbs"), value: "45%")
                }
                .padding(.top, 8)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 24)
            .background(Palette.celadon, in: RoundedRectangle(cornerRadius: 24, style: .continuous))
            if form.age < 18 {
                Text("No target under 18. You can still log meals.").font(.footnote).foregroundStyle(Palette.ink3)
            }
        }
        .onAppear { withAnimation(.easeOut(duration: 0.9)) { shown = form.previewKcal } }
        .onChange(of: form.goal) { withAnimation(.snappy) { shown = form.previewKcal } }
    }
}

struct MacroChip: View {
    var color: Color
    var title: String
    var value: String
    var body: some View {
        HStack(spacing: 6) {
            Circle().fill(color).frame(width: 8, height: 8)
            Text(title).font(.system(size: 13)).foregroundStyle(Palette.ink2)
            Text(value).font(.number(13)).foregroundStyle(Palette.ink)
        }
    }
}
