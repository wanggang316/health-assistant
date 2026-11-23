#!/usr/bin/env bash
# Build and upload one TestFlight build from this Mac with the same Fastlane
# lane as CI. Signing material comes from local files and is not printed.
set -euo pipefail
cd "$(dirname "$0")/../ios"
ASC=~/.config/appstoreconnect
# Use the same low-role CI key as CI ($ASC/ci/), not the Admin key in $ASC/.
SIGN=$ASC/signing/apple-distribution/20261119-141744

ASC_KEY_ID=$(python3 -c 'import json,os;print(json.load(open(os.path.expanduser("~/.config/appstoreconnect/ci/credentials.json")))["key_id"])')
ASC_ISSUER_ID=$(python3 -c 'import json,os;print(json.load(open(os.path.expanduser("~/.config/appstoreconnect/ci/credentials.json")))["issuer_id"])')
ASC_KEY_P8_BASE64=$(base64 < "$ASC/ci/api-key.p8" | tr -d '\n')
IOS_DIST_CERT_P12_BASE64=$(base64 < "$SIGN/distribution.p12" | tr -d '\n')
IOS_DIST_CERT_PASSWORD=$(cat "$SIGN/distribution.p12.pass")
IOS_APPSTORE_PROFILE_BASE64=$(base64 < "$ASC/ios/projects/health-assistant/health-assistant-AppStore.mobileprovision" | tr -d '\n')
TESTFLIGHT_INTERNAL_GROUP=Internal
export ASC_KEY_ID ASC_ISSUER_ID ASC_KEY_P8_BASE64 IOS_DIST_CERT_P12_BASE64 IOS_DIST_CERT_PASSWORD IOS_APPSTORE_PROFILE_BASE64 TESTFLIGHT_INTERNAL_GROUP
export HA_SIGNING_DIR=${TMPDIR:-/tmp}/ha-signing HA_SUMMARY_PATH=$PWD/build/testflight/summary.json
export FASTLANE_SKIP_UPDATE_CHECK=1 FASTLANE_OPT_OUT_USAGE=1 FASTLANE_HIDE_CHANGELOG=1 LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8

xcodegen generate
fastlane ios release
