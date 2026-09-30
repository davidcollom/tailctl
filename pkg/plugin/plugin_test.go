package plugin

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiscoverAndLongestPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable fixtures")
	}
	first, second := t.TempDir(), t.TempDir()
	t.Setenv("PATH", second)
	for _, name := range []string{"tailctl-foo", "tailctl-foo-bar"} {
		if err := os.WriteFile(filepath.Join(first, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(second, "tailctl-foo"), []byte("#!/bin/sh\nexit 9\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first, "tailctl-hidden"), []byte("no executable"), 0600); err != nil {
		t.Fatal(err)
	}
	ps, err := Discover([]string{first})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("plugins: %#v", ps)
	}
	p, args, ok := Resolve(ps, []string{"foo", "bar", "--thing", "hello world"})
	if !ok || p.Path != filepath.Join(first, "tailctl-foo-bar") || len(args) != 2 {
		t.Fatalf("resolve: %v %v", p, args)
	}
}
func TestRunPreservesArgsAndExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable fixture")
	}
	p := filepath.Join(t.TempDir(), "tailctl-test")
	script := "#!/bin/sh\nprintf '%s|%s|%s' \"$1\" \"$TAILCTL_TAILNET\" \"$TAILCTL_PLUGIN_PROTOCOL\"\nexit 7\n"
	if err := os.WriteFile(p, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := Run(context.Background(), Plugin{Name: "test", Path: p}, []string{"hello world"}, strings.NewReader(""), &out, &out, map[string]string{"TAILCTL_TAILNET": "example.com"})
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 7 {
		t.Fatalf("exit: %v", err)
	}
	if out.String() != "hello world|example.com|1" {
		t.Fatalf("output: %q", out.String())
	}
}
