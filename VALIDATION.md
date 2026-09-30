# Validation

Validated locally on 2026-09-30 with Go 1.25.1 and GoReleaser 2.18.2.

- `go test -race ./...`: passed, including authentication, error handling, redirect rejection, cancellation, response bounds, config precedence, redaction, table/JSON/YAML output and executable plugin tests.
- `go vet ./...`: passed.
- `go generate ./...` followed by `git diff --exit-code`: passed; generated output is reproducible.
- `go build -trimpath -o bin/tailctl ./cmd/tailctl`: passed.
- CLI smoke checks: root help, effective config and 93-operation catalogue passed without credentials.
- `goreleaser check`: passed.
- GoReleaser snapshot (publishing disabled): succeeded for linux, darwin and windows, each on amd64 and arm64. Archives and checksums were produced.

HTTP integration tests use local mock servers. No authenticated requests against a real tailnet were made. GitHub Actions workflows are included but have not been run on GitHub. Cross-platform binaries were built here; macOS/Windows runtime testing is delegated to the CI matrix. Unix shell plugin fixtures are skipped on Windows.

Build binaries and distribution output are excluded by `.gitignore`. Credentials must not be committed.

## Homebrew packaging

Same-repository public-release packaging validated locally on 2026-09-30:

- GoReleaser 2.18.2 configuration check passed.
- A publishing-disabled snapshot built all six OS/architecture binaries and generated `dist/homebrew/Casks/tailctl.rb`.
- Generated cask URLs select the four macOS/Linux amd64/arm64 archives; each cask SHA-256 was checked against its actual archive.
- Cobra completion settings match Homebrew's completion invocation contract. The Linux snapshot generated Bash, Zsh and Fish completions; Bash syntax validation passed.
- Updated GitHub workflow YAML parsed successfully.

A live `brew install` has not yet been exercised. The release workflow includes macOS/Linux install smoke checks after each public stable release. No version tag, public release or repository visibility change was made while adding this support.

## Login and logout

- Race-enabled tests and `go vet` passed with Go 1.25.1.
- Tests cover secure-store round trips through a mocked native provider, server scoping, environment/YAML precedence, lazy lookup, invalid token rejection, idempotent logout, store failures, secret redaction and absence of plaintext configuration writes.
- A pseudo-terminal check confirmed that interactive login disables echo and restores the terminal on Ctrl-C without saving a token.
- A new GoReleaser snapshot built Linux, macOS and Windows binaries on amd64 and arm64, then generated the Homebrew cask. GoReleaser configuration validation passed.
- Native OS keychain APIs were mocked for automated tests; live macOS/Windows keychain round trips were not performed in this Linux environment.
- Login stores API tokens; it does not perform OAuth browser login or token refresh.
