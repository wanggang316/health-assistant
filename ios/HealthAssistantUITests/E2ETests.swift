import XCTest

/// End-to-end flow against a local server (`make e2e` starts it with the
/// developer sign-in and real Gemini and S3).
@MainActor
final class E2ETests: XCTestCase {
    func testLogMealFromSamplePhoto() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-UITestReset", "-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"]
        app.launchEnvironment["DEV_SUBJECT"] = "e2e-\(UUID().uuidString.prefix(8))"
        app.launch()

        let dev = app.buttons["devSignIn"]
        XCTAssertTrue(dev.waitForExistence(timeout: 15))
        dev.tap()

        // Onboarding: three steps, then the daily target.
        let next = app.buttons["onboardingNext"]
        XCTAssertTrue(next.waitForExistence(timeout: 10))
        attach(app, "onboarding-1")
        next.tap()
        next.tap()
        attach(app, "onboarding-3")
        next.tap()

        let log = app.buttons["logMeal"]
        XCTAssertTrue(log.waitForExistence(timeout: 10))
        attach(app, "today-empty")
        log.tap()

        let sample = app.buttons["useSamplePhoto"]
        XCTAssertTrue(sample.waitForExistence(timeout: 5))
        sample.tap()
        sleep(2)
        attach(app, "recognizing")

        let confirm = app.buttons["confirmEntry"]
        XCTAssertTrue(confirm.waitForExistence(timeout: 90), "recognition did not finish")
        attach(app, "review")
        // Resolve any unresolved ingredient by its first suggestion.
        while app.buttons["选择食物"].firstMatch.exists && !confirm.isEnabled {
            app.buttons["选择食物"].firstMatch.tap()
            let first = app.cells.firstMatch
            if first.waitForExistence(timeout: 10) { first.tap() } else { break }
        }
        XCTAssertTrue(confirm.isEnabled)
        confirm.tap()

        // The saved view closes and the entry detail opens with feedback.
        let feedback = app.staticTexts["mealFeedback"]
        XCTAssertTrue(feedback.waitForExistence(timeout: 40), "meal feedback missing")
        sleep(5) // photo loads from S3
        attach(app, "entry-detail")

        app.navigationBars.buttons.firstMatch.tap()
        let summary = app.staticTexts["dailySummary"]
        XCTAssertTrue(summary.waitForExistence(timeout: 40), "daily summary missing")
        sleep(5) // photos load from S3
        attach(app, "today-logged")

        app.tabBars.buttons.element(boundBy: 1).tap()
        sleep(3)
        attach(app, "trend")
        app.tabBars.buttons.element(boundBy: 2).tap()
        sleep(1)
        attach(app, "me")
    }

    private func attach(_ app: XCUIApplication, _ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
    }
}
