# frozen_string_literal: true

# Read the generated project, without signing or changing it.
require "xcodeproj"
require_relative "lib/testflight_release"

project_path = ARGV.fetch(0) { File.expand_path("../HealthAssistant.xcodeproj", __dir__) }
project = Xcodeproj::Project.open(project_path)
expected = { "HealthAssistant" => TestflightRelease::BUNDLE_ID, "HealthAssistantTests" => "#{TestflightRelease::BUNDLE_ID}.tests" }
expected.each do |name, bundle_id|
  target = project.targets.find { |t| t.name == name }
  abort "Missing target #{name}" unless target

  target.build_configurations.each do |config|
    settings = config.build_settings
    label = "#{name}/#{config.name}"
    abort "#{label}: wrong bundle ID" unless settings["PRODUCT_BUNDLE_IDENTIFIER"] == bundle_id
    abort "#{label}: wrong team" unless settings["DEVELOPMENT_TEAM"] == TestflightRelease::TEAM_ID
    next unless name == "HealthAssistantTests"

    abort "#{label}: wrong TEST_HOST" unless settings["TEST_HOST"] == "$(BUILT_PRODUCTS_DIR)/HealthAssistant.app/HealthAssistant"
    abort "#{label}: wrong BUNDLE_LOADER" unless settings["BUNDLE_LOADER"] == "$(TEST_HOST)"
    %w[PROVISIONING_PROFILE PROVISIONING_PROFILE_SPECIFIER].each do |key|
      values = target.resolved_build_setting(key).values
      abort "#{label}: test bundle must not use an app profile" if values.any? { |v| !v.to_s.empty? }
    end
    puts "#{label}: #{bundle_id}, TEST_HOST=#{settings['TEST_HOST']}, BUNDLE_LOADER=#{settings['BUNDLE_LOADER']}"
  end
end

# Archive only the application: Fastlane's distribution signing arguments must
# never reach the unit-test bundle.
scheme_path = File.join(project_path, "xcshareddata/xcschemes/HealthAssistant.xcscheme")
scheme = Xcodeproj::XCScheme.new(scheme_path)
test_entries = scheme.build_action.entries.select do |entry|
  entry.buildable_references.any? { |ref| ref.target_name == "HealthAssistantTests" }
end
abort "Missing test build entry" if test_entries.empty?
abort "Unit tests must not be archived" if test_entries.any?(&:build_for_archiving?)
puts "Generated app/test identifiers, test host and archive scope verified."
