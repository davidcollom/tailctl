# tailctl

[![CI](https://github.com/davidcollom/tailctl/actions/workflows/ci.yaml/badge.svg)](https://github.com/davidcollom/tailctl/actions/workflows/ci.yaml)


An extensible Tailscale API CLI and Go SDK, built with Cobra and Viper.

- Complete typed client generated from the supplied upstream OpenAPI 3.1 schema.
- Common resource commands with kubectl-style tables, wide, JSON and YAML output.
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

Initial release binaries are not Apple-signed or notarised. The macOS cask removes the quarantine attribute from the staged `tailctl` binary only, so the executable can run and generate completions. This requires trusting the release directly; Apple signing/notarisation can be added later. The hook is skipped on Linux.

### Build from source

Building from source requires Go 1.25.1 or newer. Generated code and `go.sum` are committed, so building does not require regenerating the API. Clone the repository:

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

Use `tailctl help`, `tailctl get --help` and `tailctl api call --help` for command-specific usage. External plugins own their flags and help output.

## Every API operation

The pinned schema defines 93 operations across 60 paths. Both the typed client and operation catalogue are generated from it.

```sh
# Discover operation IDs and path placeholders, without a token.
tailctl api list

# Server-side filters, including repeated query values.
tailctl api call listTailnetDevices \
  --query tags=tag:prod --query tags=tag:router -o json

# Path parameters are escaped, and tailnet defaults to configured tailnet.
tailctl api call listDeviceRoutes --param deviceId=n123 -o yaml

# Mutations require --yes, even when invoked through the generic API command.
printf '%s' '{"authorized":true}' | tailctl api call authorizeDevice \
  --param deviceId=n123 --file - --yes -o json
```

`api call` accepts repeatable `--param KEY=VALUE`, repeatable `--query KEY=VALUE`, `--file PATH` or stdin (`--file -`), and `--content-type`. It sends `Accept: application/json` and renders successful JSON responses. Empty successful bodies render as `null`. Use the generated client directly for HuJSON/binary responses, endpoint-specific headers (including ETag / If-Match), or other advanced options. `api call` does not validate request bodies against the schema; typed generated request models are available to Go callers. There are no automatic retries: HTTP 429 is surfaced to the caller without replaying writes.

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

The generator is pinned as a Go tool in `go.mod` (`oapi-codegen v2.8.0`). The source endpoint returns OpenAPI **3.1** YAML, which this generator supports directly. `api/codegen.yaml` sets `response-type-suffix: HTTPResponse` to avoid upstream schema model names colliding with generated response wrapper names. Both generator and runtime versions are pinned; generated files are committed. No manual patches or lossy conversion of the schema are required. The small catalogue generator skips path-level parameter metadata and rejects duplicate operation IDs.

Upstream describes its OpenAPI schema as unstable. Schema refreshes are explicit so a routine build cannot silently change API coverage. Review the schema diff and regenerate before upgrading. The chosen module path is `github.com/davidcollom/tailctl`; change it and source imports if you publish under another repository name.

## CI and releases

The code, release configuration and Homebrew tap live in **one repository**. A stable version tag publishes release archives and then updates `Casks/tailctl.rb` on `main` automatically. The tap uses the same repository's `GITHUB_TOKEN` with `contents: write`; no extra secret or separate tap repository is required. The release workflow serialises runs to avoid simultaneous cask updates. Prereleases produce release assets but do not update the stable Homebrew cask (`skip_upload: auto`). Commits made by `GITHUB_TOKEN` do not recursively trigger workflows. After a public stable release, the release workflow installs the published cask on macOS and Linux and checks the CLI version and API catalogue.

For the first public release, make the repository public, confirm CI passes, then create the version tag. Repository visibility is managed separately and is not changed by the release workflow. No stable cask has been published until that release completes.

GitHub Actions runs race-enabled tests, vet and build on Linux, macOS and Windows; a separate job regenerates and rejects drift. GoReleaser configuration is checked in CI. Pushing a `v*` tag runs tests and publishes a GitHub release with Linux/macOS/Windows amd64 and arm64 binaries, tar.gz/zip archives and checksums.

```sh
goreleaser check
goreleaser release --snapshot --clean
# Once this source is in your repository and CI passes:
git tag v0.1.0
git push origin v0.1.0
```

Create and push a version tag only when you intend to publish a release. The workflow uses the repository's `GITHUB_TOKEN` with `contents: write`; it does not require a personal access token. Completed releases appear on the [Releases page](https://github.com/davidcollom/tailctl/releases). Snapshot output is excluded from source control. CI runs with read-only repository permissions; the release job grants write access only for publishing release assets.

## Troubleshooting

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
| Non-JSON response | Use the generated Go client for HuJSON or binary bodies. Generic CLI calls request JSON. |
| Generated code drift in CI | Run `go generate ./...` and commit the resulting generated files. |

## Layout

| Path | Purpose |
| --- | --- |
| `Casks/` | Release-generated Homebrew package in this same repository |
| `api/` | Original schema, provenance and generator configuration |
| `pkg/api/` | Complete generated client, models and operation catalogue |
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
