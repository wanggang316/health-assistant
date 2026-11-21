# One repository with path-filtered CI

Status: Accepted

The iOS app, the Go backend, and the documents change together, often in one pull request. Thus they live in one repository (`ios/`, `server/`, `docs/`). CI jobs use path filters: a change only in `ios/` does not deploy the backend, and a change only in `server/` does not upload a TestFlight build (see [0006](0006-release-on-every-main-merge.md)).

Trade-offs: a change to the API contract must update both sides in the same pull request, and path filters must include shared files that both sides use.
