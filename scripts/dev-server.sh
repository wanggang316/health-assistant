#!/usr/bin/env bash
# Run the API on 127.0.0.1:3140 for the simulator, with the developer
# sign-in, the real Gemini key, and the S3 bucket. Secrets come from local
# files and are not printed.
#
#   scripts/dev-server.sh            # foreground
set -euo pipefail
cd "$(dirname "$0")/../server"
DATA=${DEV_DATA_DIR:-$HOME/.cache/health-assistant/dev}
mkdir -p "$DATA"

if [[ ! -f "$DATA/nutrition.db" ]]; then
  go run ./cmd/fdcimport -cache "$HOME/.cache/health-assistant/fdc" -out "$DATA/nutrition.db"
fi
[[ -f "$DATA/token.key" ]] || head -c 32 /dev/urandom | base64 > "$DATA/token.key"

set -a
eval "$(grep -E '^(export )?GEMINI_API_KEY=' ~/.config/secrets.env | sed 's/^export //')"
# shellcheck disable=SC1090
. ~/.config/aws/s3/health-assistant-prod-s3.env
set +a

export LISTEN_ADDR=127.0.0.1:3140
export DATABASE_PATH="$DATA/health.db"
export NUTRITION_DB_PATH="$DATA/nutrition.db"
export APPLE_TEAM_ID=HC438T2B8P APPLE_BUNDLE_ID=com.gumpw.health-assistant APPLE_SIWA_KEY_ID=X9UM3BZKR3
APPLE_SIWA_PRIVATE_KEY=$(base64 < ~/.config/appstoreconnect/ios/projects/health-assistant/AuthKey_X9UM3BZKR3.p8 | tr -d '\n')
export APPLE_SIWA_PRIVATE_KEY
export GOOGLE_IOS_CLIENT_ID=425467050521-4qi7lq179gc5ljikm4r1iat7c0iqd6ud.apps.googleusercontent.com
export AWS_REGION=us-east-1 S3_BUCKET=${S3_BUCKET:-health-assistant-prod-a7fe63}
TOKEN_ENCRYPTION_KEY=$(cat "$DATA/token.key")
export TOKEN_ENCRYPTION_KEY
export DEV_AUTH=1 LOG_LEVEL=${LOG_LEVEL:-info}

go build -o "$DATA/health-api" ./cmd/health-api
"$DATA/health-api" migrate
exec "$DATA/health-api" serve
