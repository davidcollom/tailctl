#!/usr/bin/env bash
# Exercise the checked-out cask (CI), or the published cask (release jobs).
set -euo pipefail

if ! command -v brew >/dev/null 2>&1; then
  eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
fi
brew update
brew tap davidcollom/tailctl https://github.com/davidcollom/tailctl.git
tap_path="$(brew --repository davidcollom/tailctl)"
cask_path="$tap_path/Casks/tailctl.rb"
if [[ $# -gt 0 ]]; then
  cp "$1" "$cask_path"
fi

expected_version="$(sed -n 's/^  version "\([^"]*\)"$/\1/p' "$cask_path")"
if [[ -n "${RELEASE_TAG:-}" ]]; then
  test "$expected_version" = "${RELEASE_TAG#v}"
fi
test -n "$expected_version"

if [[ "$(uname -s)" == Darwin ]]; then
  # GitHub runners/cached downloads may not carry quarantine. Deliberately add
  # it to the staged binary immediately before the real preflight hook runs.
  # This modifies only the runner's tap, never the published package.
  python3 - "$cask_path" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
source = path.read_text()
marker = "  preflight do\n"
if source.count(marker) != 1:
    raise SystemExit("Expected one preflight hook before completion generation")
fixture = '''    if OS.mac?
      system_command "/usr/bin/xattr", args: ["-w", "com.apple.quarantine", "0081;#{Time.now.to_i.to_s(16)};tailctl-ci;00000000-0000-4000-8000-000000000000", "#{staged_path}/tailctl"], must_succeed: true
    end
'''
path.write_text(source.replace(marker, marker + fixture, 1))
PY
fi

brew install --cask davidcollom/tailctl/tailctl
test "$(tailctl --version)" = "tailctl version $expected_version"
tailctl api list -o json

if [[ "$(uname -s)" == Darwin ]]; then
  attributes="$(/usr/bin/xattr "$(command -v tailctl)")"
  if printf '%s\n' "$attributes" | /usr/bin/grep -Fxq com.apple.quarantine; then
    echo 'Installed tailctl still has quarantine' >&2
    exit 1
  fi
fi

prefix="$(brew --prefix)"
test -s "$prefix/etc/bash_completion.d/tailctl"
test -s "$prefix/share/zsh/site-functions/_tailctl"
test -s "$prefix/share/fish/vendor_completions.d/tailctl.fish"
