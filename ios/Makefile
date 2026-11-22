.PHONY: generate build test e2e clean

export DEVELOPER_DIR ?= /Applications/Xcode.app/Contents/Developer

PROJECT := HealthAssistant.xcodeproj
SIGNING := CODE_SIGNING_ALLOWED=NO
DESTINATION ?= platform=iOS Simulator,name=iPhone 18 Pro,OS=27.0

generate:
	xcodegen generate

build: generate
	xcodebuild -project $(PROJECT) -scheme HealthAssistant -destination 'generic/platform=iOS Simulator' -configuration Debug $(SIGNING) build

test: generate
	xcodebuild -project $(PROJECT) -scheme HealthAssistant -destination '$(DESTINATION)' -configuration Debug $(SIGNING) test

# Needs scripts/dev-server.sh running on 127.0.0.1:3140.
e2e: generate
	xcodebuild -project $(PROJECT) -scheme HealthAssistantE2E -destination '$(DESTINATION)' -configuration Debug $(SIGNING) test

clean:
	rm -rf $(PROJECT) build
