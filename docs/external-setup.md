# External setup

This file lists the external accounts and resources that the project uses. It contains no secrets. Secrets stay in local files outside the repository and in CI secrets.

## Apple

| Item | Value |
|---|---|
| Team | `HC438T2B8P` |
| Bundle ID | `com.gumpw.health-assistant` (capability: Sign in with Apple) |
| App Store Connect app | "Gump Health Assistant", Apple ID `6837908331`, SKU `health-assistant`, primary language Chinese (Simplified) |
| App Store profile | `health-assistant AppStore` (`TPA3M32W6Z`), expires 2027-11-19 |
| Distribution certificate | `SRH6U6PUU9`, expires 2027-11-18 (shared with trlm) |
| TestFlight internal group | `Internal`, access to all builds |
| Sign in with Apple key | `Health Assistant SIWA`, Key ID `X9UM3BZKR3`, primary App ID `com.gumpw.health-assistant`. The backend uses it for token revocation. |

The app name "Health Assistant" is used by another developer. "Gump Health Assistant" is a working name; change it before App Store release.

Local material: `~/.config/appstoreconnect/` (shared API key) and `~/.config/appstoreconnect/ios/projects/health-assistant/` (profile and the Sign in with Apple `.p8` key; Apple does not let you download the key again). No API key can create an App Store Connect app; do this in the web portal.

## Google Cloud

| Item | Value |
|---|---|
| Project | `health-assistant-515506` (number `425467050521`) |
| Gemini access | Vertex AI express mode API key, endpoint `https://aiplatform.googleapis.com/v1/publishers/google/models/<model>:generateContent` |
| Models checked on 2026-11-22 | `gemini-3.8-flash`, `gemini-3.1-flash-lite` |

| Google Auth Platform | App "Gump Health Assistant", user type External, publishing status In production (2026-11-23) |
| Branding | Home page and privacy policy `https://health-api.gumpw.com/privacy`, authorized domain `gumpw.com`, support and developer email `gumpwang2016@gmail.com` |
| iOS OAuth client | `health-assistant iOS`, client ID `425467050521-4qi7lq179gc5ljikm4r1iat7c0iqd6ud.apps.googleusercontent.com`, bundle ID `com.gumpw.health-assistant`, team `HC438T2B8P` |

Any Google account can sign in. The app uses only the `openid`, `email`, and `profile` scopes, which do not need Google verification. Do not upload a logo or add sensitive scopes: either one makes Google verification necessary.

The key is `GEMINI_API_KEY` in `~/.config/secrets.env`. It is restricted to the Vertex AI API, so the Gemini Developer API endpoint returns 403 with it.

## AWS

| Item | Value |
|---|---|
| Meal photo bucket | `health-assistant-prod-a7fe63` (`us-east-1`), private, SSE-S3 |
| Backend IAM user | `health-assistant-prod-s3`, object read, write, and delete on that bucket only |

Credentials: `~/.config/aws/s3/health-assistant-prod-s3.env`.

## Server

| Item | Value |
|---|---|
| API domain | `health-api.gumpw.com` |
| Host | nanops VM (DigitalOcean) |
| Port | `127.0.0.1:3140`, unit `health-assistant-api.service`, Caddy snippet `health-assistant.caddy` |
| DNS | Cloudflare A record → VM, DNS only, comment `nanops:health-assistant` |
| Runtime env | project-env `health-assistant/prod/env` (added in project-env PR #21) |
| Privacy policy | `https://health-api.gumpw.com/privacy` (served by the API) |

## GitHub Actions

| Workflow | Needs |
|---|---|
| `server-deploy` | secrets `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY` |
| `ios-testflight` | secrets `ASC_KEY_ID`, `ASC_ISSUER_ID`, `ASC_KEY_P8_BASE64`, `IOS_DIST_CERT_P12_BASE64`, `IOS_DIST_CERT_PASSWORD`, `IOS_APPSTORE_PROFILE_BASE64`; variable `TESTFLIGHT_INTERNAL_GROUP` |

`scripts/setup-ci-secrets.sh` sets all of them from the local files. `scripts/testflight-local.sh` runs the same release lane from this Mac.
