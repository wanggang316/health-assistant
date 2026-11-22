# Release to TestFlight and deploy the backend on every main merge

Status: Accepted

Gump wants each merge to main to be testable at once. Thus every push to main uploads an iOS build with Fastlane to an internal TestFlight group, and deploys the backend through nanops. The build number is one more than the highest build number in App Store Connect. This is different from trlm, which runs only by hand with an exact SHA; the signing and version checks of trlm are kept.

Trade-offs: every merge uses CI time and makes a build, and a bad merge reaches testers at once. Builds go only to internal testers; external beta review and App Store review stay manual.
