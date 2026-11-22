# frozen_string_literal: true

require "minitest/autorun"
require "tmpdir"
require "xcodeproj"

# Synthetic projects exercise the gate; they do not replace XcodeGen + an iOS
# test build on macOS against the actual project.yml.
class VerifyProjectTest < Minitest::Test
  def with_project
    Dir.mktmpdir do |dir|
      project = Xcodeproj::Project.new(File.join(dir, "HealthAssistant.xcodeproj"))
      app = project.new_target(:application, "HealthAssistant", :ios, "17.0")
      tests = project.new_target(:unit_test_bundle, "HealthAssistantTests", :ios, "17.0")
      [app, tests].each do |target|
        target.build_configurations.each do |config|
          config.build_settings["PRODUCT_BUNDLE_IDENTIFIER"] = "com.gumpw.health-assistant#{target == tests ? '.tests' : ''}"
          config.build_settings["DEVELOPMENT_TEAM"] = "HC438T2B8P"
          next unless target == tests

          config.build_settings["TEST_HOST"] = "$(BUILT_PRODUCTS_DIR)/HealthAssistant.app/HealthAssistant"
          config.build_settings["BUNDLE_LOADER"] = "$(TEST_HOST)"
        end
      end
      scheme = Xcodeproj::XCScheme.new
      scheme.add_build_target(app)
      scheme.add_build_target(tests, false)
      yield project, tests, scheme
    end
  end

  def verify(project, scheme)
    project.save
    scheme.save_as(project.path, "HealthAssistant", true)
    previous_args = ARGV.dup
    ARGV.replace([project.path.to_s])
    load File.expand_path("../verify_project.rb", __dir__)
  ensure
    ARGV.replace(previous_args)
  end

  def test_hosted_test_bundle_passes
    with_project do |project, _tests, scheme|
      output, = capture_io { verify(project, scheme) }
      assert_includes output, "archive scope verified"
    end
  end

  def test_wrong_host_loader_identity_or_profile_is_rejected
    {
      "TEST_HOST" => "$(BUILT_PRODUCTS_DIR)/other.app/other",
      "BUNDLE_LOADER" => "",
      "PRODUCT_BUNDLE_IDENTIFIER" => "com.trlm.app.tests",
      "DEVELOPMENT_TEAM" => "OTHERTEAM1",
      "PROVISIONING_PROFILE_SPECIFIER" => "App Store profile"
    }.each do |key, value|
      with_project do |project, tests, scheme|
        tests.build_configurations.first.build_settings[key] = value
        capture_io { assert_raises(SystemExit, key) { verify(project, scheme) } }
      end
    end
  end

  def test_archiving_tests_is_rejected
    with_project do |project, _tests, scheme|
      scheme.build_action.entries.last.build_for_archiving = true
      _, error = capture_io { assert_raises(SystemExit) { verify(project, scheme) } }
      assert_includes error, "Unit tests must not be archived"
    end
  end
end
