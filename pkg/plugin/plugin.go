// Package plugin discovers and runs kubectl-style external executables.
package plugin

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const ProtocolVersion = "1"

type Plugin struct{ Name, Path string }

// Discover searches configured directories before PATH. First match wins.
// Executables are discovered by name only and are never executed during help/listing.
func Discover(dirs []string) ([]Plugin, error) {
	seen := map[string]bool{}
	out := []Plugin{}
	all := append(append([]string{}, dirs...), filepath.SplitList(os.Getenv("PATH"))...)
	for _, dir := range all {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("scan plugin directory %s: %w", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "tailctl-") {
				continue
			}
			if runtime.GOOS == "windows" {
				if !strings.HasSuffix(strings.ToLower(name), ".exe") {
					continue
				}
				name = name[:len(name)-4]
			}
			name = strings.TrimPrefix(name, "tailctl-")
			if name == "" || strings.HasPrefix(name, "-") {
				continue
			}
			path, err := filepath.Abs(filepath.Join(dir, entry.Name()))
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, Plugin{Name: name, Path: path})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Resolve chooses the longest matching command prefix: tailctl-foo-bar for foo bar.
func Resolve(plugins []Plugin, args []string) (Plugin, []string, bool) {
	for n := len(args); n > 0; n-- {
		valid := true
		for _, a := range args[:n] {
			if strings.HasPrefix(a, "-") {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		name := strings.Join(args[:n], "-")
		for _, p := range plugins {
			if p.Name == name {
				return p, args[n:], true
			}
		}
	}
	return Plugin{}, nil, false
}

type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("plugin exited with status %d", e.Code) }
func Run(ctx context.Context, p Plugin, args []string, in io.Reader, out, stderr io.Writer, environment map[string]string) error {
	cmd := exec.CommandContext(ctx, p.Path, args...)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = stderr
	// No resolved credentials are copied into the subprocess. Ambient environment is inherited.
	cmd.Env = os.Environ()
	environmentCopy := map[string]string{"TAILCTL_PLUGIN_PROTOCOL": ProtocolVersion}
	for k, v := range environment {
		if k == "TAILCTL_TOKEN" {
			return fmt.Errorf("refusing to inject credentials into plugin environment")
		}
		environmentCopy[k] = v
	}
	for k, v := range environmentCopy {
		prefix := k + "="
		filtered := cmd.Env[:0]
		for _, entry := range cmd.Env {
			if !strings.HasPrefix(entry, prefix) {
				filtered = append(filtered, entry)
			}
		}
		cmd.Env = append(filtered, prefix+v)
	}
	if err := cmd.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code := e.ExitCode()
			if code < 0 {
				code = 1
			}
			return &ExitError{Code: code}
		}
		return err
	}
	return nil
}
