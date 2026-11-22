import GoogleSignIn
import SwiftUI

@main
struct HealthAssistantApp: App {
    @State private var model = AppModel.live()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(model)
                .tint(Palette.tea)
                .onOpenURL { GIDSignIn.sharedInstance.handle($0) }
                .task { await model.start() }
        }
    }
}

struct RootView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        ZStack {
            Palette.rice.ignoresSafeArea()
            switch model.phase {
            case .launching:
                ProgressView().tint(Palette.tea)
            case .signedOut:
                SignInView().transition(.opacity)
            case .onboarding:
                OnboardingView().transition(.move(edge: .trailing).combined(with: .opacity))
            case .signedIn:
                MainTabs().transition(.opacity)
            }
        }
        .animation(.easeOut(duration: 0.35), value: model.phase)
    }
}

struct MainTabs: View {
    @State private var tab = 0
    var body: some View {
        TabView(selection: $tab) {
            Tab(String(localized: "Today"), systemImage: "circle.circle", value: 0) { TodayView() }
            Tab(String(localized: "Trend"), systemImage: "chart.bar.xaxis", value: 1) { TrendView() }
            Tab(String(localized: "Me"), systemImage: "person.crop.circle", value: 2) { MeView() }
        }
    }
}
