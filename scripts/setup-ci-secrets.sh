#!/usr/bin/env bash
# Set the GitHub Actions secrets of this repository from local files.
# Run it yourself once: values go from local files to `gh secret set` and are
# never printed.
#
#   scripts/setup-ci-secrets.sh            # all secrets
#   scripts/setup-ci-secrets.sh deploy     # only server deploy
#   scripts/setup-ci-secrets.sh ios        # only TestFlight
set -euo pipefail

REPO=wanggang316/health-assistant
ASC=~/.config/appstoreconnect
# The ASC_* secrets use the low-role CI key in $ASC/ci/, never the Admin key in $ASC/.
SIGN=$ASC/signing/apple-distribution/20261119-141744
PROFILE=$ASC/ios/projects/health-assistant/health-assistant-AppStore.mobileprovision
what=${1:-all}

set_file() { gh secret set "$1" -R "$REPO" < "$2"; echo "set $1"; }
set_value() { printf '%s' "$2" | gh secret set "$1" -R "$REPO"; echo "set $1"; }
set_b64() { base64 < "$2" | tr -d '\n' | gh secret set "$1" -R "$REPO"; echo "set $1"; }

if [[ $what == all || $what == deploy ]]; then
  set_value DEPLOY_HOST 162.243.195.13
  set_value DEPLOY_USER root
  set_file DEPLOY_SSH_KEY ~/.ssh/deploy
fi

if [[ $what == all || $what == ios ]]; then
  key_id=$(python3 -c 'import json,os;print(json.load(open(os.path.expanduser("~/.config/appstoreconnect/ci/credentials.json")))["key_id"])')
  issuer=$(python3 -c 'import json,os;print(json.load(open(os.path.expanduser("~/.config/appstoreconnect/ci/credentials.json")))["issuer_id"])')
  set_value ASC_KEY_ID "$key_id"
  set_value ASC_ISSUER_ID "$issuer"
  set_b64 ASC_KEY_P8_BASE64 "$ASC/ci/api-key.p8"
  set_b64 IOS_DIST_CERT_P12_BASE64 "$SIGN/distribution.p12"
  set_value IOS_DIST_CERT_PASSWORD "$(cat "$SIGN/distribution.p12.pass")"
  set_b64 IOS_APPSTORE_PROFILE_BASE64 "$PROFILE"
  gh variable set TESTFLIGHT_INTERNAL_GROUP -R "$REPO" --body Internal
  echo "set TESTFLIGHT_INTERNAL_GROUP"
fi
