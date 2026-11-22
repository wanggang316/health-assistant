# frozen_string_literal: true

require "minitest/autorun"
require "tmpdir"
require "yaml"
require_relative "../lib/testflight_release"

class TestflightReleaseTest < Minitest::Test
  TR = TestflightRelease
  SHA = "A" * 40
  OTHER_SHA = "B" * 40

  def assert_stop(stage, pattern = nil)
    error = assert_raises(TR::Stop) { yield }
    assert_equal stage, error.stage
    assert_match pattern, error.message if pattern
    error
  end

  # --- build number ---------------------------------------------------------

  def test_next_build_number_is_one_above_highest_numeric_build
    assert_equal 13, TR.next_build_number(%w[3 12 9])
  end

  def test_next_build_number_compares_numbers_not_strings
    assert_equal 11, TR.next_build_number(%w[9 10])
  end

  def test_next_build_number_ignores_non_integer_builds
    assert_equal 5, TR.next_build_number(%w[4 1.2.3 20261101a])
  end

  def test_next_build_number_without_builds_uses_floor
    assert_equal 1, TR.next_build_number([], floor: 1)
    assert_equal 7, TR.next_build_number(%w[2], floor: 7)
  end

  def test_build_number_taken_stops_before_upload
    assert_stop(:blocked, /already exists/) { TR.check_build_number_free!("5", %w[4 5]) }
    assert_nil TR.check_build_number_free!("6", %w[4 5])
  end

  # --- app and group --------------------------------------------------------

  def test_project_uses_the_registered_app_and_derived_test_bundle
    spec = YAML.load_file(File.expand_path("../../project.yml", __dir__))
    assert_equal "com.gumpw", spec.dig("options", "bundleIdPrefix")
    assert_equal "com.gumpw.health-assistant", TR::BUNDLE_ID
    { "HealthAssistant" => TR::BUNDLE_ID, "HealthAssistantTests" => "#{TR::BUNDLE_ID}.tests" }.each do |target, bundle|
      settings = spec.dig("targets", target, "settings", "base")
      assert_equal bundle, settings["PRODUCT_BUNDLE_IDENTIFIER"]
      assert_equal "HC438T2B8P", settings["DEVELOPMENT_TEAM"]
      refute settings.key?("PROVISIONING_PROFILE_SPECIFIER")
      refute settings.key?("PROVISIONING_PROFILE")
    end
    assert_includes spec.dig("targets", "HealthAssistantTests", "dependencies"), { "target" => "HealthAssistant" }
    assert_equal ["test"], spec.dig("schemes", "HealthAssistant", "build", "targets", "HealthAssistantTests")
  end

  def test_old_app_id_is_not_an_asc_match
    assert_stop(:blocked, /No App Store Connect app/) do
      TR.check_single_app!([{ bundle_id: "com.trlm.app" }])
    end
  end

  def test_single_app_is_required
    assert_stop(:blocked, /No App Store Connect app/) { TR.check_single_app!([]) }
    two = [{ bundle_id: TR::BUNDLE_ID }, { bundle_id: TR::BUNDLE_ID }]
    assert_stop(:blocked, /2 App Store Connect apps/) { TR.check_single_app!(two) }
    one = TR.check_single_app!([{ bundle_id: "com.other" }, { bundle_id: TR::BUNDLE_ID, id: 1 }])
    assert_equal 1, one[:id]
  end

  def test_internal_group_must_exist_and_never_matches_external_group
    groups = [
      { name: "Team", internal: true, all_builds: true },
      { name: "Beta", internal: false, all_builds: false }
    ]
    assert_equal "Team", TR.select_internal_group!(groups, "Team")[:name]
    assert_stop(:blocked, /Internal groups: Team/) { TR.select_internal_group!(groups, "Beta") }
    assert_stop(:blocked, /repository variable/) { TR.select_internal_group!(groups, "") }
  end

  def test_duplicate_internal_group_names_block
    groups = [{ name: "Team", internal: true }, { name: "Team", internal: true }]
    assert_stop(:blocked, /2 internal/) { TR.select_internal_group!(groups, "Team") }
  end

  # --- marketing version ----------------------------------------------------

  def test_marketing_version_must_be_1_0_in_plist
    assert_stop(:blocked, /expected 1.0/) do
      TR.check_marketing_version!(plist_version: "1.0.0", pre_release_versions: [], app_store_versions: [])
    end
  end

  def test_marketing_version_must_match_existing_testflight_train
    assert_stop(:blocked, /1.0.0 but not 1.0/) do
      TR.check_marketing_version!(plist_version: "1.0", pre_release_versions: %w[1.0.0], app_store_versions: [])
    end
    assert_nil TR.check_marketing_version!(plist_version: "1.0", pre_release_versions: %w[1.0 0.9],
                                           app_store_versions: [{ version: "1.0", state: "PREPARE_FOR_SUBMISSION" }])
  end

  def test_closed_app_store_train_blocks
    assert_stop(:blocked, /READY_FOR_SALE/) do
      TR.check_marketing_version!(plist_version: "1.0", pre_release_versions: %w[1.0],
                                  app_store_versions: [{ version: "1.0", state: "READY_FOR_SALE" }])
    end
  end

  # --- signing --------------------------------------------------------------

  def find_identity(*names)
    lines = names.each_with_index.map { |(sha, name), i| "  #{i + 1}) #{sha} \"#{name}\"" }
    (lines + ["     #{names.size} valid identities found"]).join("\n")
  end

  def test_selects_the_team_distribution_identity
    output = find_identity([OTHER_SHA, "Apple Development: A (HC438T2B8P)"],
                           [SHA, "Apple Distribution: A (HC438T2B8P)"])
    assert_equal SHA, TR.select_distribution_identity!(output)[:sha1]
  end

  def test_identity_of_another_team_or_type_blocks
    output = find_identity([SHA, "Apple Distribution: A (ZZZZZZZZZZ)"],
                           [OTHER_SHA, "Developer ID Application: A (HC438T2B8P)"])
    assert_stop(:blocked, /no valid Apple Distribution/) { TR.select_distribution_identity!(output) }
  end

  def test_two_distribution_identities_block
    output = find_identity([SHA, "Apple Distribution: A (HC438T2B8P)"],
                           [OTHER_SHA, "iPhone Distribution: A (HC438T2B8P)"])
    assert_stop(:blocked, /exactly one/) { TR.select_distribution_identity!(output) }
  end

  def profile(overrides = {})
    {
      "Name" => "health-assistant AppStore",
      "UUID" => "uuid-1",
      "TeamIdentifier" => [TR::TEAM_ID],
      "ExpirationDate" => Time.utc(2027, 1, 1),
      "Entitlements" => { "application-identifier" => "HC438T2B8P.com.gumpw.health-assistant", "get-task-allow" => false }
    }.merge(overrides)
  end

  def check(prof, sha1s: [SHA], now: Time.utc(2026, 11, 18))
    TR.check_profile!(prof, profile_cert_sha1s: sha1s, identity_sha1: SHA, now: now)
  end

  def test_valid_app_store_profile_passes
    result = check(profile)
    assert_equal "health-assistant AppStore", result[:name]
    assert_equal "uuid-1", result[:uuid]
  end

  def test_profile_checks_block_wrong_material
    assert_stop(:blocked, /not for team/) { check(profile("TeamIdentifier" => ["ZZZZZZZZZZ"])) }
    assert_stop(:blocked, /expected HC438T2B8P.com.gumpw.health-assistant/) do
      check(profile("Entitlements" => { "application-identifier" => "HC438T2B8P.*" }))
    end
    assert_stop(:blocked, /not App Store/) { check(profile("ProvisionedDevices" => ["x"])) }
    assert_stop(:blocked, /not App Store/) { check(profile("ProvisionsAllDevices" => true)) }
    assert_stop(:blocked, /get-task-allow/) do
      check(profile("Entitlements" => { "application-identifier" => "HC438T2B8P.com.gumpw.health-assistant", "get-task-allow" => true }))
    end
    assert_stop(:blocked, /expired/) { check(profile, now: Time.utc(2027, 2, 1)) }
    assert_stop(:blocked, /does not contain the distribution certificate/) { check(profile, sha1s: [OTHER_SHA]) }
  end

  def test_old_or_test_bundle_profile_is_rejected
    %w[com.trlm.app com.trlm.app.tests com.gumpw.health-assistant.tests].each do |bundle|
      wrong = profile
      wrong["Entitlements"]["application-identifier"] = "#{TR::TEAM_ID}.#{bundle}"
      assert_stop(:blocked, /expected HC438T2B8P\.com\.gumpw\.health-assistant/) { check(wrong) }
    end
  end

  def verify_signature(entitlements: {}, prof: profile, signed_sha: SHA, profile_shas: [SHA])
    TR.verify_signature!(
      entitlements: { "application-identifier" => "HC438T2B8P.com.gumpw.health-assistant",
                      "com.apple.developer.team-identifier" => "HC438T2B8P" }.merge(entitlements),
      profile: prof, profile_cert_sha1s: profile_shas, signed_cert_sha1: signed_sha,
      identity_sha1: SHA, now: Time.utc(2026, 11, 19)
    )
  end

  def test_actual_signature_matches_selected_identity_and_embedded_profile
    assert_equal "uuid-1", verify_signature[:uuid]
    assert_stop(:blocked, /signing entitlements/) do
      verify_signature(entitlements: { "application-identifier" => "HC438T2B8P.com.trlm.app" })
    end
    assert_stop(:blocked, /signing entitlements/) do
      verify_signature(entitlements: { "com.apple.developer.team-identifier" => "OTHERTEAM1" })
    end
    assert_stop(:blocked, /get-task-allow/) { verify_signature(entitlements: { "get-task-allow" => true }) }
    assert_stop(:blocked, /selected Distribution identity/) { verify_signature(signed_sha: OTHER_SHA) }
    assert_stop(:blocked, /distribution certificate/) { verify_signature(profile_shas: [OTHER_SHA]) }
    assert_stop(:blocked, /not for team/) { verify_signature(prof: profile("TeamIdentifier" => ["OTHERTEAM1"])) }
    assert_stop(:blocked, /not App Store/) { verify_signature(prof: profile("ProvisionedDevices" => ["x"])) }
    assert_stop(:blocked, /expired/) { verify_signature(prof: profile("ExpirationDate" => Time.utc(2026, 1, 1))) }
    wrong = profile
    wrong["Entitlements"]["application-identifier"] = "HC438T2B8P.com.trlm.app"
    assert_stop(:blocked, /expected HC438T2B8P/) { verify_signature(prof: wrong) }
  end

  # --- bundle plist ---------------------------------------------------------

  def plist(overrides = {})
    {
      "CFBundleShortVersionString" => "1.0",
      "CFBundleVersion" => "8",
      "CFBundleIdentifier" => TR::BUNDLE_ID,
      "ITSAppUsesNonExemptEncryption" => false
    }.merge(overrides)
  end

  def test_bundle_plist_matching_values_pass
    assert_equal({ version: "1.0", build: "8" }, TR.verify_bundle_plist!(plist, build_number: "8", label: "Archive"))
  end

  def test_archive_and_ipa_reject_old_or_test_bundle_identifiers
    ["Archive", "Exported IPA"].each do |label|
      %w[com.trlm.app com.trlm.app.tests com.gumpw.health-assistant.tests].each do |bundle|
        assert_stop(:failed, /CFBundleIdentifier/) do
          TR.verify_bundle_plist!(plist("CFBundleIdentifier" => bundle), build_number: "8", label: label)
        end
      end
    end
  end

  def test_bundle_plist_mismatch_fails_before_upload
    error = assert_stop(:failed, /Nothing was uploaded/) do
      TR.verify_bundle_plist!(plist("CFBundleVersion" => "1", "CFBundleShortVersionString" => "1.0.0"),
                              build_number: "8", label: "Exported IPA")
    end
    assert_match(/CFBundleVersion is "1"/, error.message)
    assert_match(/CFBundleShortVersionString is "1.0.0"/, error.message)
  end

  def test_bundle_plist_without_encryption_key_fails
    bare = plist.reject { |k, _| k == "ITSAppUsesNonExemptEncryption" }
    assert_stop(:failed, /ITSAppUsesNonExemptEncryption is missing/) do
      TR.verify_bundle_plist!(bare, build_number: "8", label: "Archive")
    end
  end

  def test_bundle_plist_cannot_change_the_verified_encryption_declaration
    assert_stop(:failed, /must remain false/) do
      TR.verify_bundle_plist!(plist("ITSAppUsesNonExemptEncryption" => true), build_number: "8", label: "IPA")
    end
  end

  # --- processing and visibility --------------------------------------------

  def status(processing, internal, in_group: false, all_builds: false)
    TR.build_status(processing_state: processing, internal_state: internal, in_group: in_group, group_all_builds: all_builds)
  end

  def test_build_not_listed_or_processing_is_pending
    assert_equal :uploaded_pending, status(nil, nil)
    assert_equal :uploaded_pending, status("PROCESSING", "PROCESSING")
    assert_equal :uploaded_pending, status("VALID", "PROCESSING")
  end

  def test_processing_failure_is_failed
    assert_equal :failed, status("INVALID", nil)
    assert_equal :failed, status("FAILED", nil)
    assert_equal :failed, status("VALID", "MISSING_EXPORT_COMPLIANCE")
  end

  def test_done_only_when_processed_and_listed_in_group
    assert_equal :done, status("VALID", "READY_FOR_BETA_TESTING", in_group: true)
    assert_equal :done, status("VALID", "IN_BETA_TESTING", in_group: true)
  end

  def test_processed_build_not_in_group
    assert_equal :processed_group_pending, status("VALID", "IN_BETA_TESTING", all_builds: true)
    assert_equal :needs_group_decision, status("VALID", "IN_BETA_TESTING", all_builds: false)
    assert_equal :processed_group_pending, TR.stage_for(:needs_group_decision)
  end

  # --- summary --------------------------------------------------------------

  def test_summary_writes_json_and_markdown
    Dir.mktmpdir do |dir|
      path = File.join(dir, "summary.json")
      TR.write_summary(path, "阶段" => "阻塞", "错误" => "a|b\nc")
      assert_equal "阻塞", JSON.parse(File.read(path, encoding: "UTF-8"))["阶段"]
      assert_includes File.read(File.join(dir, "summary.md"), encoding: "UTF-8"), "| 错误 | a\\|b c |"
    end
  end

  def test_beijing_time
    assert_equal "2026-11-18 13:36:00 UTC+8", TR.beijing_time(Time.utc(2026, 11, 18, 5, 36))
  end
end
