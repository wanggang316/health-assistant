# frozen_string_literal: true

require "json"
require "time"

# Rules for the TestFlight release lanes. This file has no network or
# Fastlane dependency, so the tests in ../test can run it as plain Ruby.
module TestflightRelease
  BUNDLE_ID = "com.gumpw.health-assistant"
  TEAM_ID = "HC438T2B8P"
  MARKETING_VERSION = "1.0"

  # App Store versions in these states close the version train: App Store
  # Connect rejects new builds for that marketing version.
  CLOSED_TRAIN_STATES = %w[
    READY_FOR_SALE
    READY_FOR_DISTRIBUTION
    PENDING_DEVELOPER_RELEASE
    PENDING_APPLE_RELEASE
    PROCESSING_FOR_APP_STORE
    PROCESSING_FOR_DISTRIBUTION
    WAITING_FOR_REVIEW
    IN_REVIEW
    REPLACED_WITH_NEW_VERSION
  ].freeze

  READY_INTERNAL_STATES = %w[READY_FOR_BETA_TESTING IN_BETA_TESTING].freeze
  FAILED_INTERNAL_STATES = %w[PROCESSING_EXCEPTION MISSING_EXPORT_COMPLIANCE EXPIRED].freeze

  # A stop before or after the upload. `stage` is one of the report stages in
  # STAGES; the message must never contain secret values.
  class Stop < StandardError
    attr_reader :stage

    def initialize(stage, message)
      raise ArgumentError, "unknown stage #{stage}" unless STAGES.key?(stage)

      @stage = stage
      super(message)
    end
  end

  STAGES = {
    blocked: "阻塞",
    failed: "失败",
    uploaded_pending: "已上传、待处理",
    processed_group_pending: "处理完成、内部组待确认",
    done: "处理完成、已有内部组可见"
  }.freeze

  module_function

  def blocked!(message)
    raise Stop.new(:blocked, message)
  end

  # Next free build number: one more than the highest numeric build number of
  # any build of the app (all marketing versions), and at least `floor`.
  def next_build_number(existing, floor: 1)
    numbers = existing.map(&:to_s).select { |n| n.match?(/\A\d+\z/) }.map(&:to_i)
    [numbers.max.to_i + 1, floor].max
  end

  def check_build_number_free!(build_number, existing)
    return unless existing.map(&:to_s).include?(build_number.to_s)

    blocked!("Build #{build_number} already exists in App Store Connect. Run the workflow again to pick the next number.")
  end

  def check_single_app!(apps)
    matches = apps.select { |app| app[:bundle_id] == BUNDLE_ID }
    blocked!("No App Store Connect app has bundle ID #{BUNDLE_ID} for this API key.") if matches.empty?
    blocked!("#{matches.size} App Store Connect apps have bundle ID #{BUNDLE_ID}.") if matches.size > 1
    matches.first
  end

  # `pre_release_versions` are TestFlight trains (version strings);
  # `app_store_versions` are hashes with :version and :state.
  def check_marketing_version!(plist_version:, pre_release_versions:, app_store_versions:)
    unless plist_version == MARKETING_VERSION
      blocked!("Info.plist CFBundleShortVersionString is #{plist_version.inspect}, expected #{MARKETING_VERSION}.")
    end

    trains = pre_release_versions.uniq
    if trains.any? && !trains.include?(MARKETING_VERSION)
      blocked!("App Store Connect has TestFlight versions #{trains.sort.join(', ')} but not #{MARKETING_VERSION}.")
    end

    closed = app_store_versions.find do |v|
      v[:version] == MARKETING_VERSION && CLOSED_TRAIN_STATES.include?(v[:state].to_s)
    end
    return unless closed

    blocked!("App Store version #{MARKETING_VERSION} is #{closed[:state]}; App Store Connect does not accept new builds for it.")
  end

  # `groups` are hashes with :name, :internal and :all_builds.
  def select_internal_group!(groups, name)
    blocked!("Set the TESTFLIGHT_INTERNAL_GROUP repository variable to the name of an existing internal group.") if name.to_s.strip.empty?

    internal = groups.select { |g| g[:internal] }
    matches = internal.select { |g| g[:name] == name }
    names = internal.map { |g| g[:name] }.sort.join(", ")
    blocked!("No internal TestFlight group is named #{name.inspect}. Internal groups: #{names.empty? ? 'none' : names}.") if matches.empty?
    blocked!("#{matches.size} internal TestFlight groups are named #{name.inspect}.") if matches.size > 1
    matches.first
  end

  # Parse `security find-identity -v -p codesigning` output and return the one
  # valid distribution identity of the team as { sha1:, name: }.
  def select_distribution_identity!(find_identity_output)
    identities = find_identity_output.each_line.filter_map do |line|
      match = line.match(/\A\s*\d+\)\s+([0-9A-F]{40})\s+"(.+)"/)
      match && { sha1: match[1], name: match[2] }
    end
    dist = identities.select do |i|
      i[:name].match?(/\A(Apple|iPhone) Distribution: .+ \(#{TEAM_ID}\)\z/)
    end
    blocked!("The certificate secret has no valid Apple Distribution identity for team #{TEAM_ID}.") if dist.empty?
    blocked!("The certificate secret has #{dist.size} distribution identities for team #{TEAM_ID}; supply exactly one.") if dist.size > 1
    dist.first
  end

  # `profile` is the decoded provisioning profile plist as a Hash.
  # `profile_cert_sha1s` are the SHA-1 fingerprints of its DeveloperCertificates.
  def check_profile!(profile, profile_cert_sha1s:, identity_sha1:, now: Time.now)
    name = profile["Name"].to_s
    teams = Array(profile["TeamIdentifier"])
    app_id = profile.dig("Entitlements", "application-identifier")
    expires = profile["ExpirationDate"]

    blocked!("Provisioning profile #{name.inspect} is not for team #{TEAM_ID}.") unless teams == [TEAM_ID]
    unless app_id == "#{TEAM_ID}.#{BUNDLE_ID}"
      blocked!("Provisioning profile #{name.inspect} is for #{app_id.inspect}, expected #{TEAM_ID}.#{BUNDLE_ID}.")
    end
    if profile.key?("ProvisionedDevices") || profile["ProvisionsAllDevices"]
      blocked!("Provisioning profile #{name.inspect} is a development, ad hoc or enterprise profile, not App Store.")
    end
    if profile.dig("Entitlements", "get-task-allow")
      blocked!("Provisioning profile #{name.inspect} allows get-task-allow; it is not an App Store profile.")
    end
    blocked!("Provisioning profile #{name.inspect} has no expiration date.") unless expires
    expires_at = expires.respond_to?(:to_time) ? expires.to_time : Time.parse(expires.to_s)
    blocked!("Provisioning profile #{name.inspect} expired at #{expires_at.utc.iso8601}.") if expires_at <= now
    unless profile_cert_sha1s.map(&:upcase).include?(identity_sha1.upcase)
      blocked!("Provisioning profile #{name.inspect} does not contain the distribution certificate from the certificate secret.")
    end

    { name: name, uuid: profile["UUID"].to_s, expires_at: expires_at }
  end

  # Check an Info.plist from the archive or the exported IPA.
  def verify_bundle_plist!(plist, build_number:, label:)
    errors = []
    version = plist["CFBundleShortVersionString"]
    build = plist["CFBundleVersion"]
    errors << "CFBundleShortVersionString is #{version.inspect}, expected #{MARKETING_VERSION}" unless version == MARKETING_VERSION
    errors << "CFBundleVersion is #{build.inspect}, expected #{build_number}" unless build == build_number.to_s
    errors << "CFBundleIdentifier is #{plist['CFBundleIdentifier'].inspect}" unless plist["CFBundleIdentifier"] == BUNDLE_ID
    errors << "ITSAppUsesNonExemptEncryption is missing" unless plist.key?("ITSAppUsesNonExemptEncryption")
    if plist.key?("ITSAppUsesNonExemptEncryption") && plist["ITSAppUsesNonExemptEncryption"] != false
      errors << "ITSAppUsesNonExemptEncryption must remain false"
    end
    raise Stop.new(:failed, "#{label} Info.plist check failed: #{errors.join('; ')}. Nothing was uploaded.") if errors.any?

    { version: version, build: build }
  end

  # Values read from codesign and the artifact's embedded profile, not from
  # the source project or the profile supplied for signing.
  def verify_signature!(entitlements:, profile:, profile_cert_sha1s:, signed_cert_sha1:, identity_sha1:, now: Time.now)
    unless entitlements["application-identifier"] == "#{TEAM_ID}.#{BUNDLE_ID}" &&
           entitlements["com.apple.developer.team-identifier"] == TEAM_ID
      blocked!("Artifact signing entitlements do not match #{TEAM_ID}.#{BUNDLE_ID}.")
    end
    blocked!("Artifact allows get-task-allow.") if entitlements["get-task-allow"]
    unless signed_cert_sha1.upcase == identity_sha1.upcase
      blocked!("Artifact was not signed by the selected Distribution identity.")
    end
    check_profile!(profile, profile_cert_sha1s: profile_cert_sha1s, identity_sha1: signed_cert_sha1, now: now)
  end

  # Decide the state of an uploaded build.
  #   processing_state: PROCESSING | VALID | INVALID | FAILED | nil (not listed yet)
  #   internal_state:   BuildBetaDetail internal build state or nil
  #   in_group:         the group lists the build
  #   group_all_builds: the group has access to all builds
  def build_status(processing_state:, internal_state:, in_group:, group_all_builds:)
    return :failed if %w[INVALID FAILED].include?(processing_state)
    return :failed if FAILED_INTERNAL_STATES.include?(internal_state)
    return :uploaded_pending unless processing_state == "VALID" && READY_INTERNAL_STATES.include?(internal_state)
    return :done if in_group
    # The group gets every build, but App Store Connect does not list this one
    # yet. Wait and check again; it is not visible until it is listed.
    return :processed_group_pending if group_all_builds

    :needs_group_decision
  end

  def stage_for(status)
    status == :needs_group_decision ? :processed_group_pending : status
  end

  def beijing_time(time = Time.now)
    time.getlocal("+08:00").strftime("%Y-%m-%d %H:%M:%S UTC+8")
  end

  # Write the run summary as JSON and as Markdown for $GITHUB_STEP_SUMMARY.
  def write_summary(path, fields)
    File.write(path, JSON.pretty_generate(fields), encoding: "UTF-8")
    lines = ["## iOS TestFlight", "", "| Field | Value |", "|---|---|"]
    fields.each { |k, v| lines << "| #{k} | #{v.to_s.gsub('|', '\\|').gsub("\n", ' ')} |" }
    File.write(path.sub(/\.json\z/, ".md"), "#{lines.join("\n")}\n", encoding: "UTF-8")
  end
end
