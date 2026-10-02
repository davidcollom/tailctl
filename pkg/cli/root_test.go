package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"TAILCTL_CONFIG", "TAILCTL_TOKEN", "TAILCTL_TAILNET", "TAILCTL_SERVER", "TAILCTL_OUTPUT", "TAILCTL_TIMEOUT", "TAILCTL_PLUGIN_DIRS"} {
		t.Setenv(k, "")
	}
}
func TestGetDevicesAndFlagPrecedence(t *testing.T) {
	isolate(t)
	t.Setenv("TAILCTL_TOKEN", "secret")
	t.Setenv("TAILCTL_TAILNET", "env.example")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/flag.example/") {
			t.Errorf("tailnet: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"devices":[{"nodeId":"n1","hostname":"worker","os":"linux"}]}`)
	}))
	defer s.Close()
	var out bytes.Buffer
	err := Execute(context.Background(), []string{"devices", "list", "--tailnet", "flag.example", "--server", s.URL + "/api/v2", "-o", "json"}, Options{}, strings.NewReader(""), &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Devices[0]["hostname"] != "worker" {
		t.Fatalf("result: %v", got)
	}
}

func TestLegacyGetIsHiddenAndDeprecated(t *testing.T) {
	root, err := NewRoot(Options{})
	if err != nil {
		t.Fatal(err)
	}
	get, _, err := root.Find([]string{"get"})
	if err != nil {
		t.Fatal(err)
	}
	if !get.Hidden || get.Deprecated == "" {
		t.Fatal("legacy get command must be hidden and deprecated")
	}
	for _, resource := range []string{"devices", "users", "keys", "services", "dns", "settings", "policy"} {
		cmd, _, err := root.Find([]string{"get", resource})
		if err != nil {
			t.Fatalf("find get %s: %v", resource, err)
		}
		if cmd.Deprecated == "" {
			t.Errorf("get %s is not deprecated", resource)
		}
	}
}

func TestMutationGuardBeforeAuthentication(t *testing.T) {
	isolate(t)
	var out bytes.Buffer
	err := Execute(context.Background(), []string{"api", "call", "deleteDevice", "--param", "deviceId=n1"}, Options{}, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("guard: %v", err)
	}
}
func TestConfigViewRedactsSecret(t *testing.T) {
	isolate(t)
	t.Setenv("TAILCTL_TOKEN", "secret-value")
	var out bytes.Buffer
	err := Execute(context.Background(), []string{"config", "view", "-o", "json"}, Options{}, strings.NewReader(""), &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "secret-value") || !strings.Contains(out.String(), "redacted") {
		t.Fatalf("config leaked: %s", out.String())
	}
}
func TestCatalogAndNoAuth(t *testing.T) {
	isolate(t)
	var out bytes.Buffer
	err := Execute(context.Background(), []string{"api", "list"}, Options{}, strings.NewReader(""), &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "listTailnetDevices") {
		t.Fatal("catalogue missing operation")
	}
}
func TestServicesUsesUpstreamEnvelope(t *testing.T) {
	isolate(t)
	t.Setenv("TAILCTL_TOKEN", "secret")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"vipServices":[{"name":"svc:web","ports":["tcp:443"]}]}`)
	}))
	defer s.Close()
	var out bytes.Buffer
	err := Execute(context.Background(), []string{"get", "services", "--server", s.URL}, Options{}, strings.NewReader(""), &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "svc:web") {
		t.Fatalf("services: %s", out.String())
	}
}

type badExtension struct{}

func (badExtension) Command(*Runtime) (*cobra.Command, error) { return &cobra.Command{Use: "get"}, nil }
func TestRejectBuiltInShadowing(t *testing.T) {
	if _, err := NewRoot(Options{Extensions: []Extension{badExtension{}}}); err == nil {
		t.Fatal("shadowing permitted")
	}
}
func TestExplicitConfig(t *testing.T) {
	isolate(t)
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("tailnet: file.example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := Execute(context.Background(), []string{"config", "view", "--config", p, "-o", "json"}, Options{}, strings.NewReader(""), &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "file.example") {
		t.Fatalf("config: %s", out.String())
	}
}
