# tailctl

[![CI](https://github.com/davidcollom/tailctl/actions/workflows/ci.yaml/badge.svg)](https://github.com/davidcollom/tailctl/actions/workflows/ci.yaml)


An extensible Tailscale API CLI and Go SDK, built with Cobra and Viper.

- Complete typed client generated from the supplied upstream OpenAPI 3.1 schema.
- Named resource commands for all 93 schema operations, with kubectl-style tables, wide, JSON and YAML output.
- Go command extensions and cross-platform executable plugins.
- Home-directory configuration, secure OS credential storage and explicit mutation guards.
- Homebrew installation on macOS and Linux from the same repository.
- Reproducible `go generate`, GitHub Actions and GoReleaser v2 releases.

## Contents

- [Installation and quick start](#installation-and-quick-start)
- [Login and logout](#login-and-logout)
- [Configuration](#configuration)
- [Shell completion](#shell-completion)
- [Every API operation](#every-api-operation)
- [Go SDK](#go-sdk)
- [Plugins](#plugins)
- [Generation and maintenance](#generation-and-maintenance)
- [CI and releases](#ci-and-releases)
- [Troubleshooting](#troubleshooting)
- [Layout](#layout)
- [Contributing](#contributing)
- [Licence](#licence)

## Installation and quick start

### Homebrew (macOS and Linux)

After the repository is public and its first stable release has completed:

```sh
# An explicit URL allows the project repository to double as the tap.
brew tap davidcollom/tailctl https://github.com/davidcollom/tailctl.git
brew install --cask davidcollom/tailctl/tailctl

tailctl --version
tailctl --help

# Update later:
brew update
brew upgrade --cask davidcollom/tailctl/tailctl
```

The cask selects the macOS/Linux amd64 or arm64 release archive and verifies its SHA-256 checksum. It installs the binary and Bash, Zsh and Fish completion scripts; Go is not required. Use current Homebrew with Linux cask and completion-generation support. The commands above become available only after GoReleaser publishes `Casks/tailctl.rb` on `main`; there is no placeholder package pointing at a nonexistent release. Public source and public release assets are the intended distribution model.

Initial release binaries are not Apple-signed or notarised. The macOS cask checks and removes the quarantine attribute from the staged `tailctl` binary only in **preflight**, before Homebrew executes it to generate completions. Attribute read/removal failures stop installation rather than silently leaving a blocked binary. This requires trusting the release directly; Apple signing/notarisation can be added later. The hook is skipped on Linux.

### Build from source

Building from source requires Go 1.26.0 or newer. Generated code and `go.sum` are committed, so building does not require regenerating the API. Clone the repository:

```sh
git clone https://github.com/davidcollom/tailctl.git
cd tailctl
go mod download
go install ./cmd/tailctl
```

`go install` puts `tailctl` in `GOBIN`, or `$(go env GOPATH)/bin` when `GOBIN` is unset. Add that directory to your `PATH`. Alternatively, build a local binary:

```sh
go mod download
go build -trimpath -o bin/tailctl ./cmd/tailctl

# Save an API access token securely (hidden interactive prompt).
bin/tailctl login
export TAILCTL_TAILNET='example.com'

bin/tailctl get devices
bin/tailctl get devices -o wide --sort-by hostname
bin/tailctl get devices n123 -o yaml
bin/tailctl get users -o json
bin/tailctl get services
bin/tailctl get dns -o yaml
bin/tailctl get policy -o json
```

Example table (illustrative):

```text
ID     NAME       OS     ADDRESSES     AUTHORISED
n123   worker-01  linux  100.64.0.10   true
n456   laptop     macos  100.64.0.11   true
```

On Windows, the equivalent PowerShell setup is:

```powershell
go build -trimpath -o bin/tailctl.exe ./cmd/tailctl
.\bin\tailctl.exe login
$env:TAILCTL_TAILNET = 'example.com'
.\bin\tailctl.exe get devices -o wide
```

Tokens can also be supplied by a secret manager; avoid committing credentials or pasting real tokens into examples. The CLI talks to the Tailscale **control-plane HTTP API**; it does not require a local `tailscaled` daemon and does not replace the Tailscale network client.

`devices`, `device` and `nodes` are aliases. Other built-in resources are `users`, `keys`, `services`, `dns`, `settings` and `policy`. Tables use explicit columns; `-o json` and `-o yaml` retain all fields. `--no-headers` and `--sort-by FIELD` apply to tables. Sorting is lexicographic by the original top-level JSON field and does not change JSON/YAML ordering. Cobra also supplies `help`, `completion` and `--version`.

## Login and logout

```sh
tailctl login             # Hidden prompt; saves the token in the OS credential store.
tailctl get devices       # Uses the saved token automatically.
tailctl logout            # Deletes the saved token for the selected API server.
```

Paste a **Tailscale API access token** from the admin console, or an already-issued OAuth access token. Device enrolment keys (`tskey-auth-...`) cannot access the control-plane API and are rejected. `login` stores the supplied token; it does not launch a browser/OAuth flow, acquire or refresh OAuth tokens, or verify that the token has permissions for a particular operation. Authentication and permission failures are reported when you make an API request.

| Platform | Credential backend |
| --- | --- |
| macOS | OS Keychain, via the system `security` utility |
| Windows | Windows Credential Manager |
| Linux | Secret Service over the user's session D-Bus, such as GNOME Keyring |

Credentials are stored under service `tailctl`, with the canonical API server URL as the account. Trailing slashes, hostname case and default ports are normalised. A token saved for the Tailscale API is not automatically reused for another `--server`. All tailnets accessed through that server use the same saved token; named login profiles are not currently implemented.

For noninteractive input, pipe a token from your secret manager or an existing environment variable:

```sh
printf '%s' "$TAILCTL_TOKEN" | tailctl login --token-stdin
unset TAILCTL_TOKEN
```

There is no `--token` argument and no token is printed or written to YAML. API commands resolve authentication in this order: **`TAILCTL_TOKEN` → configured YAML token → saved OS credential**. Login warns when an environment/config token overrides the newly saved token. `config view` redacts configured tokens and reports deferred keychain lookup when none is configured; it does not read the keychain. Metadata commands such as `api list`, completion and help also avoid credential prompts.

On headless Linux, containers or an SSH session without Secret Service, use `TAILCTL_TOKEN` supplied by your automation/secret manager. `login` fails clearly if secure OS storage is unavailable and **never falls back to a plaintext file**. A Linux desktop keyring must be running and unlocked; installing only `libsecret-tools` does not create the keyring service.

`logout` is idempotent and removes only the local saved token for the selected API server. It does not revoke the token in Tailscale, remove another server's token, modify YAML, or unset your shell's environment. If an environment/config token remains active, logout warns about it; remove that override separately. External plugins are not given saved keychain tokens automatically. In-process Go extensions use the shared authenticated client. Test code can supply `cli.Options.Credentials` with a fake `credentials.Store` without accessing the OS keychain.

## Configuration

The default file is **`~/.config/tailctl/config.yaml`**, including on Windows (resolved using Go's `os.UserHomeDir`). Start from `config.example.yaml`:

```yaml
tailnet: '-'
server: https://api.tailscale.com/api/v2
output: table
timeout: 30s
# Use tailctl login for secure OS storage, or TAILCTL_TOKEN for automation.
```

Precedence: **changed flags → environment → YAML file → defaults**. Missing default configuration is fine; an explicitly selected missing file is an error.

| Setting | Environment | Flag |
| --- | --- | --- |
| Tailnet | `TAILCTL_TAILNET` | `--tailnet` |
| API token | `TAILCTL_TOKEN` (override) | `login` / `logout` manage secure OS storage |
| API server | `TAILCTL_SERVER` | `--server` |
| Output | `TAILCTL_OUTPUT` | `-o`, `--output` |
| HTTP timeout | `TAILCTL_TIMEOUT` | `--timeout` |
| Config file | `TAILCTL_CONFIG` | `--config` |
| Plugin directories | `TAILCTL_PLUGIN_DIRS` (space-separated) | YAML `plugin_dirs` |

`tailctl config path` prints the selected path. `tailctl config view -o yaml` prints effective settings with the token redacted. If storing credentials in YAML, restrict permissions (`chmod 600` on Unix). A scoped, already-issued OAuth access token works as a bearer token; acquiring or refreshing OAuth tokens is outside this implementation. Prefer `tailctl login` for local use and keep only non-secret settings in this file.

## Shell completion

Cobra generates completion scripts for Bash, Zsh, Fish and PowerShell:

```sh
tailctl completion bash > tailctl.bash
source tailctl.bash

# Zsh: load compinit, then source the generated script.
autoload -Uz compinit && compinit
tailctl completion zsh > tailctl.zsh
source tailctl.zsh

# Fish:
tailctl completion fish > ~/.config/fish/completions/tailctl.fish
```

```powershell
tailctl completion powershell | Out-String | Invoke-Expression
```

Use `tailctl help`, `tailctl devices --help`, `tailctl devices routes set --help` and `tailctl api call --help` for command-specific usage. External plugins own their flags and help output.

## Every API operation

The pinned schema defines **93 operations across 60 paths**, and every operation now has a named resource command. The typed SDK, embedded validation schema, operation catalogue and [complete command reference](docs/commands.md) are generated alongside the CLI documentation.

| Resource family | Operations | Includes |
| --- | ---: | --- |
| `devices` | 15 | Authorisation, names, tags, keys, IPs, routes and posture attributes |
| `users` | 7 | Listing, roles, approval, suspension, restoration and deletion |
| `invites users`, `invites devices` | 11 | Creation, inspection, resend, acceptance and deletion |
| `keys` | 5 | Auth/client/federated key management |
| `dns` | 11 | Complete configuration, nameservers, preferences, search paths and split DNS |
| `policy` | 4 | Read, replace, preview and validate/test policies |
| `logs` | 8 | Audit/network logs, streaming configuration/status and AWS external IDs |
| `posture` | 5 | Posture integration management |
| `contacts` | 3 | Contact details and verification emails |
| `webhooks` | 7 | CRUD, testing and secret rotation |
| `settings`, `tailnets` | 3 | Tailnet settings and deletion |
| `services` | 7 | Service definitions, hosts and per-device approval |
| `oauth-apps` | 5 | OAuth app management |
| `organisations` | 2 | List/create organisation tailnets (`organizations` is an alias) |

### Resource commands

Path IDs are positional; tailnet-scoped operations use the configured `--tailnet`. Simple body fields and query parameters become typed flags. Boolean flags send a field only when explicitly supplied, so `--authorized=false` and `--magic-dns=false` work correctly.

```sh
tailctl devices list -o wide
tailctl devices list --fields all --filter hostname=worker-01
tailctl devices routes get n123
tailctl users list --role admin --type member
tailctl invites users list
tailctl invites devices list n123
tailctl webhooks list
tailctl oauth-apps list

# Every non-GET/HEAD action requires --yes, including policy preview/validation.
tailctl devices authorise n123 --authorized --yes
tailctl devices rename n123 --name worker-02 --yes
tailctl devices routes set n123 --routes 10.0.0.0/8 --routes 192.168.0.0/16 --yes
tailctl devices tags set n123 --tags-json '[]' --yes
tailctl users role set u123 --role member --yes
tailctl invites users create --email user@example.com --role member --yes
tailctl invites devices create n123 --email user@example.com --allow-exit-node --yes
tailctl dns preferences set --magic-dns=false --yes
tailctl dns split update --route example.com=1.1.1.1 --route example.com=8.8.8.8 --yes
tailctl settings update --devices-approval-on=false --https-enabled --yes
tailctl services set svc:web --ports tcp:443 --display-name Web --yes
tailctl services approval set svc:web n123 --approved --yes

# Both timestamps are required by these log endpoints.
tailctl logs audit list --start 2026-09-01T00:00:00Z --end 2026-09-02T00:00:00Z
tailctl logs network list --start 2026-09-01T00:00:00Z --end 2026-09-02T00:00:00Z -o json
```

Repeat array flags for each value; use `--FIELD-json '[]'` to send an empty array. Object/union fields use JSON flags, for example `devices attributes set n123 custom:healthy --value-json true --yes`. Complex bodies, multi-item batches, policy documents and secret fields use `--file PATH` or `--file -` for stdin. Body flags and `--file` cannot be combined. Resource commands validate JSON bodies and schema parameter types, enums and bounds before credential lookup or HTTP requests. The server still checks permissions and business rules; HuJSON is passed through for server validation.

For schema endpoints whose body is an array of simple objects, such as user and
device invitations, body flags create one item. Omitting `--email` creates an
invite URL instead of emailing it. Use `--file` for batches:

```sh
printf '%s' '[{"email":"alice@example.com","role":"member"},{"email":"bob@example.com","role":"admin"}]' |
  tailctl invites users create --file - --yes
```

Nullable scalar settings use normal flags, including explicit false values such
as `--devices-approval-on=false`. Their `--FIELD-json null` form remains
available when the API distinguishes null from false. String maps accept
repeatable `KEY=VALUE` flags, for example:

```sh
tailctl keys update KEY-ID \
  --custom-claim-rules team=platform \
  --custom-claim-rules environment=production \
  --yes
```

Split DNS has dedicated repeatable route flags. Repeat a domain to add multiple
nameservers, use `--clear-domain DOMAIN` for a null mapping, or
`dns split set --clear-all --yes` to replace the configuration with an empty
map. Arbitrary policy/HuJSON documents, nested batch data and secret-bearing
bodies intentionally continue to use `--file` or explicit JSON flags.

```sh
# Inspect required fields, enums, body shapes and referenced schemas locally.
tailctl api describe createWebhook
tailctl api describe createKey -o json
tailctl api describe createOrganizationTailnet -o yaml

# Store a one-time key response without printing it or overwriting an existing file.
# key-request.json contains the complete request described by the schema above.
tailctl keys create --file key-request.json --response-file key-response.json --yes

# Preserve comments in HuJSON; optimistic concurrency is supported via If-Match.
tailctl policy get --accept application/hujson --raw > policy.hujson
tailctl policy validate --file policy.hujson --content-type application/hujson --yes -o json
tailctl policy set --file policy.hujson --content-type application/hujson \
  --if-match 'known-policy-etag' --yes
```

Tables unwrap list envelopes, use resource-specific columns and redact credential fields. `-o wide` adds useful details; `--sort-by FIELD` and `--no-headers` apply to tables. JSON/YAML retain the complete response, including secrets and pagination metadata. Lists perform one request: for endpoints with cursor pagination, read the returned cursor with `-o json` and pass it to the next request using `--cursor`. There are no automatic retries or write replays.

`--raw` writes exact response bytes, including HuJSON. `--response-file PATH` reserves a new file before making the request and saves the exact response there without echoing it. Files use mode `0600` on Unix and the user's filesystem ACLs on Windows; existing files are never overwritten. Use these modes for responses containing one-time secrets. Empty successful responses show the operation and HTTP status.

### Generic operation access

The generic operation interface remains available. The former `tailctl get`
tree is hidden and deprecated, but remains executable for compatibility with
pre-v1 scripts; use the resource-first commands in new usage.

```sh
# Shows every operation ID, HTTP route and corresponding resource command.
tailctl api list

tailctl api call listTailnetDevices \
  --query tags=tag:prod --query tags=tag:router -o json
tailctl api call listDeviceRoutes --param deviceId=n123 -o yaml
printf '%s' '{"authorized":true}' | tailctl api call authorizeDevice \
  --param deviceId=n123 --file - --yes -o json
```

`api call` is the lower-level JSON interface: it accepts repeatable `--param KEY=VALUE`, repeatable `--query KEY=VALUE`, `--file`, and `--content-type`, without schema body validation. It requests JSON; empty responses render as `null`. Prefer resource commands for validated requests, readable tables, HuJSON, endpoint-specific headers and response files. Go callers can use `c.CallRaw` for exact bytes, status, content type and response headers (including policy ETags), or `c.API` for the complete typed interface.

## Go SDK

```go
package main

import (
    "context"
    "fmt"
    "os"
    "time"

    "github.com/davidcollom/tailctl/pkg/client"
)

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    if err := run(ctx); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

func run(ctx context.Context) error {
    c, err := client.New(client.Options{
        Token:  os.Getenv("TAILCTL_TOKEN"),
        Tailnet: "example.com",
    })
    if err != nil {
        return err
    }
    devices, err := c.Devices(ctx)
    if err != nil {
        return err
    }
    fmt.Printf("Devices: %d\n", len(devices))
    return nil
}
```

`Devices`, `Device` and `Users` provide concise typed helpers. `c.API` exposes the complete `*api.ClientWithResponses`, with methods such as `ListDeviceRoutesWithResponse`. Generated methods return both successful and unsuccessful HTTP responses; callers must check `StatusCode()` before consuming success fields. `client.APIError` exposes `StatusCode`, `RequestID` and `RetryAfter` for facade/generic calls without including potentially sensitive response bodies in errors. The CLI handles OS credential lookup; the Go SDK itself takes an explicit `Options.Token`. Supply `Options.HTTPClient` to use a custom transport, tracing or test fixtures. Context cancellation is honoured; facade clients have a default 30-second timeout, reject redirects and restrict credentials to the configured origin. Responses through the facade transport are limited to 16 MiB.

## Plugins

### Separate executables

Build the included example:

```sh
mkdir -p ~/.config/tailctl/plugins
go build -o ~/.config/tailctl/plugins/tailctl-hello ./examples/hello-plugin
tailctl plugin list
tailctl hello --your-plugin-flag value
```

On Windows build `tailctl-hello.exe`. Configured `plugin_dirs` are searched before `PATH`; the default directory is `~/.config/tailctl/plugins`. First matching executable wins. A binary named `tailctl-audit-devices` handles `tailctl audit devices`; the longest matching command prefix wins. Built-ins always take precedence, including their aliases.

External command names must come first. Flags following the plugin command are passed to the plugin unchanged, rather than parsed as tailctl flags. Select configuration for plugins with `TAILCTL_CONFIG`, and other shared settings with environment/configuration. Plugins receive stdin/stdout/stderr, cancellation, exit status propagation and protocol version `TAILCTL_PLUGIN_PROTOCOL=1`, plus effective tailnet/output/server/timeout. Resolved tokens from YAML are **not** injected into plugins. Plugins inherit the process environment, so an existing `TAILCTL_TOKEN` remains available. Top-level help and plugin listing do not execute plugin binaries; `tailctl hello --help` forwards help to the selected plugin. Plugins are ordinary executable programs and run with the user's permissions; install ones you trust. There is no sandbox, automatic installer or RPC daemon.

### In-process Go extensions

Implement `cli.Extension`:

```go
func (MyExtension) Command(runtime *cli.Runtime) (*cobra.Command, error)
```

Register it via `cli.Options{Extensions: []cli.Extension{MyExtension{}}}`. The runtime offers `Client()`, `Print(...)`, a replaceable credential store, decoded configuration and an isolated Viper instance after persistent pre-run configuration loading. Commands should use `cmd.Context()`, `cmd.OutOrStdout()` and `cobra` argument validation. Preserve the root's persistent pre-run hook when adding commands. Conflicting root commands are rejected. See the complete buildable example in `examples/inprocess`.

```sh
go run ./examples/inprocess inventory -o wide
```

## Generation and maintenance

```sh
# Rebuild from the committed schema; does not refresh it from the network.
go generate ./...

# Explicitly fetch the current upstream YAML, then regenerate.
make schema-update

# Tests and static checks.
go test -race ./...
go vet ./...
```

The generator is pinned as a Go tool in `go.mod` (`oapi-codegen v2.8.0`). The source endpoint returns OpenAPI **3.1** YAML, which this generator supports directly. `api/codegen.yaml` sets `response-type-suffix: HTTPResponse` to avoid upstream schema model names colliding with generated response wrapper names. Both generator and runtime versions are pinned; generated files are committed. No manual patches or lossy conversion of the schema are required. The catalogue generator rejects duplicate operation IDs and embeds a deterministic JSON copy of the original schema for local validation. Resource commands resolve operation and path-level parameters plus component references and object composition. `go generate ./...` also rebuilds `docs/commands.md` from the real Cobra tree. Coverage tests exercise every schema operation, and newly added upstream operations require an explicit command mapping in `pkg/cli/commands.go`.

Upstream describes its OpenAPI schema as unstable. Schema refreshes are explicit so a routine build cannot silently change API coverage. Review the schema diff and regenerate before upgrading. The chosen module path is `github.com/davidcollom/tailctl`; change it and source imports if you publish under another repository name.

## CI and releases

The code, release configuration and Homebrew tap live in **one repository**. A stable version tag publishes release archives and then updates `Casks/tailctl.rb` on `main` automatically. The tap uses the same repository's `GITHUB_TOKEN` with `contents: write`; no extra secret or separate tap repository is required. The release workflow serialises runs to avoid simultaneous cask updates. This workflow publishes stable semantic versions only; prerelease tags are rejected by the release gate. Commits made by `GITHUB_TOKEN` do not recursively trigger workflows. After a public stable release, the release workflow installs the published cask on macOS and Linux and checks the CLI version, API catalogue and all three completion files. The macOS test deliberately quarantines the staged binary before preflight, then verifies that the installed binary has no quarantine attribute. Normal CI also runs this installation test against the proposed cask and the current published archive.

For the first public release, make the repository public, confirm CI passes, then use the manual Release workflow. Repository visibility is managed separately and is not changed by the release workflow. No stable cask has been published until that release completes.

GitHub Actions runs race-enabled tests, vet and build on Linux, macOS and Windows; a separate job regenerates and rejects drift. GoReleaser configuration is checked in CI. An authorised stable tag push or manual release runs tests and publishes a GitHub release with Linux/macOS/Windows amd64 and arm64 binaries, tar.gz/zip archives and checksums.

```sh
goreleaser check
goreleaser release --snapshot --clean --skip=sign
# Optional CLI alternative after CI succeeds (a named release CODEOWNER):
git tag v0.1.0
git push origin v0.1.0
```

Create and push a version tag only when you intend to publish a release. The workflow uses the repository's `GITHUB_TOKEN` with `contents: write`; it does not require a personal access token. Completed releases appear on the [Releases page](https://github.com/davidcollom/tailctl/releases). Snapshot output is excluded from source control. CI runs with read-only repository permissions. Only the release job grants `contents: write` for release assets and the Homebrew cask, and `id-token: write` for keyless signing.

### Release from GitHub's web interface

Open **Actions → Release → Run workflow**, select the default branch (`main`), choose **patch**, **minor** or **major**, then run it. For the initial release choose **minor** to create `v0.1.0`. No personal access token or manual tag creation is needed.

| Selection | Starting at `v0.1.2` | No stable tags yet |
| --- | --- | --- |
| patch | `v0.1.3` | `v0.0.1` |
| minor | `v0.2.0` | `v0.1.0` |
| major | `v1.0.0` | `v1.0.0` |

The highest stable `vMAJOR.MINOR.PATCH` tag is the version baseline; prereleases and other tags are ignored. Releases are serialised. Manual runs must use the latest default-branch commit, and the latest push-triggered CI run for that exact commit must have finished successfully. Wait for CI before dispatching. The release job repeats tests and generation checks, confirms the branch has not moved, creates an annotated tag without overwriting anything, then runs GoReleaser in the same workflow. This avoids GitHub's restriction that tag pushes made with `GITHUB_TOKEN` do not trigger another push workflow.

The gate uses `.github/CODEOWNERS` from the default branch and the effective owners of `/.github/workflows/release.yaml`. Both `github.actor` and `github.triggering_actor` must be named owners with current repository write access, including reruns. Initially this is `@davidcollom`. Update the ownership entry to add maintainers. Missing/cleared ownership, unsupported patterns, teams and email-based owners fail closed; team membership requires a separate organisation-aware integration, which this personal repository does not need.

A failed run can be rerun while its source remains the default-branch head. It resumes only its own annotated tag at the same commit. It never deletes or moves tags. If the default branch has advanced, inspect any existing tag/release before starting a fresh increment. Direct stable tag pushes remain supported for named release owners when the source commit passed default-branch CI.

Protect `main`, the release workflow, the scripts and CODEOWNERS with required owner reviews, and restrict tag creation with repository rulesets when adding collaborators. Workflow checks govern this release process; they cannot prevent a repository administrator or someone permitted to change workflows from changing the policy. Repository visibility stays unchanged; Homebrew installation tests run once the repository is public.

### Verify release downloads

Releases sign `checksums.txt` with Cosign using GitHub Actions OIDC. No signing key, password or signing secret is stored in the repository. GoReleaser publishes `checksums.txt.sigstore.json`, which contains the signature, certificate and transparency-log evidence. The authenticated SHA-256 manifest covers every release archive. The release workflow also verifies the bundle and all local archive checksums before reporting success.

Install [Cosign 3](https://docs.sigstore.dev/cosign/system_config/installation/) and download `checksums.txt`, `checksums.txt.sigstore.json` and your archive from the **same release**. For a manual release, use the default-branch identity shown below (`main`). A release started by a direct tag push instead uses `.../release.yaml@refs/tags/<exact-tag>`. The run summary records the source commit and successful CI run.

```sh
cosign verify-blob \
  --certificate-identity "https://github.com/davidcollom/tailctl/.github/workflows/release.yaml@refs/heads/main" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle checksums.txt.sigstore.json checksums.txt

# Run only after signature verification succeeds (Linux/GNU coreutils).
sha256sum --check --ignore-missing checksums.txt
```

On macOS, check the downloaded archive with `shasum -a 256 <archive>` against its entry in the verified manifest. On Windows, use `Get-FileHash <archive> -Algorithm SHA256`. Confirm the exact archive filename and digest match before extracting or running it. Do not disable certificate identity, issuer or transparency-log verification.

Homebrew checks archive SHA-256 values from the generated cask; it does not perform Cosign verification automatically. Cosign signing is separate from Apple Developer ID signing/notarisation and Windows Authenticode. Snapshot builds skip signing and are for local testing. Public Sigstore records include the repository/workflow identity, even while the repository is private. The release workflow verifies its actual event-ref identity against the resulting bundle.

### Dependency maintenance

Dependabot checks Go modules (including the pinned OpenAPI generator) and GitHub Actions every Monday at 08:00 UK time. Go minor/patch updates are grouped; major Go dependency upgrades remain separate for review. Action updates are grouped, and workflow actions are pinned to full commit SHAs. Security updates have separate groups and require **Dependabot alerts and security updates** to be enabled in the repository's Settings → Advanced Security.

Updates open pull requests and run normal CI; they are not automatically merged. Generator upgrades may require `go generate ./...` and committing generated changes before CI passes. The Tailscale schema still requires an explicit `go run ./cmd/update-schema` refresh and review. The Go toolchain in `go.mod` and the GoReleaser binary version in both workflows are maintained explicitly; Dependabot does not update arbitrary workflow `with.version` values. Cosign uses the installer action's bundled version, so its version follows reviewed installer updates.

## Troubleshooting

If an earlier Homebrew installation reports **“Apple could not verify tailctl is free of malware”**, update the tap and reinstall so the corrected preflight hook runs:

```sh
brew update
brew reinstall --cask davidcollom/tailctl/tailctl
tailctl --version
```

For an installation that failed and is not recorded as installed, use `brew install --cask davidcollom/tailctl/tailctl` after updating. The cask-only repair keeps the same released version and archive checksums; a new binary release is not needed. Direct archive downloads still require an explicit macOS approval until Apple Developer ID signing/notarisation is configured. Cosign signatures and checksum verification remain separate from Apple's Gatekeeper checks.


| Symptom | What to check |
| --- | --- |
| Homebrew cannot find `tailctl` | Confirm the repository is public, the stable release completed, and `Casks/tailctl.rb` exists on `main`; run `brew update`. |
| `API token required` | Run `tailctl login`, or supply `TAILCTL_TOKEN` for automation. Use an API access token or OAuth access token, rather than a device enrolment auth key. |
| OS credential store unavailable | Unlock/enable the OS keyring; Linux needs a session D-Bus and Secret Service provider. Headless sessions can use an environment token. No plaintext fallback is used. |
| API calls still authenticate after logout | Remove any `TAILCTL_TOKEN` or YAML `token` override; logout deletes only the saved OS credential. |
| HTTP 401 | Token validity and expiry. |
| HTTP 403 | Token permissions/OAuth scopes and access to the selected tailnet. |
| HTTP 404 | Tailnet name and resource ID. `--tailnet -` uses the token's tailnet where supported. |
| HTTP 429 | Rate limiting; inspect `client.APIError.RetryAfter` in Go callers and back off before retrying. Writes are never retried automatically. |
| Missing explicit config file | Check `--config` or `TAILCTL_CONFIG`; only the missing default file is ignored. |
| Plugin not found | Run `tailctl plugin list`; check its name, executable permission and `plugin_dirs`/`PATH`. On Windows use `.exe`. |
| Plugin flags rejected by the root command | Put the external plugin command first; following flags are passed to the plugin. |
| Non-JSON response | Use a resource command with `--raw` or `--response-file`, or the generated Go client. Generic `api call` requests JSON. |
| Generated code drift in CI | Run `go generate ./...` and commit the resulting generated files. |

## Layout

| Path | Purpose |
| --- | --- |
| `Casks/` | Release-generated Homebrew package in this same repository |
| `api/` | Original schema, provenance and generator configuration |
| `pkg/api/` | Complete generated client, models, catalogue and embedded validation schema |
| `docs/commands.md` | Generated reference for all 93 resource commands |
| `pkg/client/` | Authentication, typed helpers and operation invocation |
| `pkg/config/` | Isolated Viper instances and home-directory defaults |
| `pkg/credentials/` | Server-scoped OS credential store interface and adapter |
| `pkg/output/` | Stable table columns, wide, JSON and YAML output |
| `pkg/plugin/` | Executable discovery, prefix resolution and subprocess protocol |
| `pkg/cli/` | Cobra command tree and in-process extension contract |
| `cmd/tailctl/` | Main CLI entry point |
| `cmd/update-schema/` | Explicit upstream schema refresh |
| `examples/` | Buildable executable and Go extension examples |

## Contributing

Keep changes focused, add tests for behaviour that affects API requests or plugin dispatch, and run `go test -race ./...`, `go vet ./...` and `go generate ./...` before opening a pull request. Run `gofmt` on changed Go files. Edit generated code through the upstream schema/generator configuration rather than patching `*.gen.go` manually. Schema refreshes should include updated provenance and a review of generated diffs. Homebrew package changes should be made in `.goreleaser.yaml`; `Casks/tailctl.rb` is generated on release. Moving the tap later only requires changing `homebrew_casks.repository` and publishing a tap migration. A separate repository will require a credential scoped to that tap.

Tests use local HTTP fixtures and do not need Tailscale credentials. See [VALIDATION.md](VALIDATION.md) for the initial local validation results, and [Actions](https://github.com/davidcollom/tailctl/actions) for repository CI.

## Licence

Project code is licensed under the [MIT licence](LICENSE). The upstream Tailscale schema and third-party dependencies retain their respective terms. This is an independent project.

## Sources

- [Supplied upstream OpenAPI endpoint](https://api.tailscale.com/api/v2?outputOpenapiSchema=true)
- [Tailscale API documentation](https://tailscale.com/docs/reference/tailscale-api)
- [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen)
- [Cobra](https://github.com/spf13/cobra), [Viper](https://github.com/spf13/viper), [GoReleaser](https://goreleaser.com/)
