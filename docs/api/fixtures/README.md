# API fixtures

Example responses of the REST API (docs/design/technical-design.md §5). Both sides test against them:

- The server compares the shape of real responses with these files (same keys, same JSON types; `null` matches any type).
- The iOS app decodes every file into its DTOs.

Change a fixture in the same pull request as the API change. A change here runs both server and iOS CI.
