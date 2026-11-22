# Health Assistant

An iOS app that records meals from photos, estimates nutrition, and gives diet advice, with a Go backend.

## Layout

| Path | Content |
|---|---|
| `server/` | Go API (`cmd/health-api`), nutrition database builder (`cmd/fdcimport`), deploy files |
| `ios/` | SwiftUI app (XcodeGen), Fastlane release lanes |
| `docs/` | Design, ADRs, API fixtures shared by both sides |

## Run locally

```sh
scripts/dev-server.sh          # API on 127.0.0.1:3140 with developer sign-in, real Gemini and S3
cd ios && make test            # unit tests
cd ios && make e2e             # simulator end-to-end test against the local API
cd server && go test ./...
```

## Release

- Server: every merge to `main` that changes `server/` deploys to the nanops VM (`server-deploy`).
- iOS: every merge to `main` that changes `ios/` uploads one TestFlight build (`ios-testflight`). `scripts/testflight-local.sh` does the same from this Mac.

## Documents

- [Glossary](docs/glossary.md)
- [External setup](docs/external-setup.md)
- [Architecture decisions](docs/adrs/)
- [UI design and prototype](docs/design/ui-design.md)
- [Technical design](docs/design/technical-design.md)
- [API fixtures](docs/api/fixtures/README.md)
- [Research: food recognition models (2026-11-21)](docs/research/2026-11-21-food-recognition-models.md)
