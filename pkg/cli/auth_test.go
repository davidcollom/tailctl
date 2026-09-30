package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/davidcollom/tailctl/pkg/credentials"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCredentials struct {
	values                    map[string]string
	getErr, setErr, deleteErr error
	reads, writes, deletes    int
}

func (f *fakeCredentials) Get(server string) (string, error) {
	f.reads++
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.values[server]
	if !ok {
		return "", credentials.ErrNotFound
	}
	return v, nil
}
func (f *fakeCredentials) Set(server, token string) error {
	f.writes++
	if f.setErr != nil {
		return f.setErr
	}
	f.values[server] = token
	return nil
}
func (f *fakeCredentials) Delete(server string) error {
	f.deletes++
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.values[server]; !ok {
		return credentials.ErrNotFound
	}
	delete(f.values, server)
	return nil
}
func newFakeCredentials() *fakeCredentials { return &fakeCredentials{values: map[string]string{}} }
func authExecute(args []string, input string, store *fakeCredentials) (string, string, error) {
	var out, stderr bytes.Buffer
	err := Execute(context.Background(), args, Options{Credentials: store}, strings.NewReader(input), &out, &stderr)
	return out.String(), stderr.String(), err
}
func TestLoginStoresSecretWithoutWritingConfig(t *testing.T) {
	isolate(t)
	store := newFakeCredentials()
	token := "fixture-login-token"
	out, stderr, err := authExecute([]string{"login", "--token-stdin"}, token+"\n", store)
	if err != nil {
		t.Fatal(err)
	}
	if store.values["https://api.tailscale.com/api/v2"] != token {
		t.Fatal("token not saved")
	}
	if strings.Contains(out+stderr, token) {
		t.Fatal("token leaked in output")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, ".config", "tailctl", "config.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("login wrote plaintext config")
	}
}
func TestLoginRejectsInvalidInputAndNonterminal(t *testing.T) {
	isolate(t)
	for _, input := range []string{"", "a\nb", "tskey-auth-fixture", strings.Repeat("a", maxTokenBytes+1)} {
		store := newFakeCredentials()
		_, _, err := authExecute([]string{"login", "--token-stdin"}, input, store)
		if err == nil || store.writes != 0 {
			t.Fatal("invalid token was saved")
		}
	}
	store := newFakeCredentials()
	_, _, err := authExecute([]string{"login"}, "fixture", store)
	if err == nil || !strings.Contains(err.Error(), "--token-stdin") {
		t.Fatalf("nonterminal: %v", err)
	}
}
func TestKeychainFailureDoesNotLeakOrFallBack(t *testing.T) {
	isolate(t)
	store := newFakeCredentials()
	store.setErr = errors.New("backend contains fixture-secret")
	out, stderr, err := authExecute([]string{"login", "--token-stdin"}, "fixture-secret", store)
	if err == nil || strings.Contains(err.Error()+out+stderr, "fixture-secret") {
		t.Fatal("backend error or token leaked")
	}
	if len(store.values) != 0 {
		t.Fatal("failed write persisted data")
	}
}
func TestLogoutDeletesOnlySavedTokenAndWarnsOverrides(t *testing.T) {
	isolate(t)
	t.Setenv("TAILCTL_TOKEN", "environment-token")
	store := newFakeCredentials()
	store.values["https://api.tailscale.com/api/v2"] = "saved-token"
	store.values["https://other.example"] = "other-token"
	_, stderr, err := authExecute([]string{"logout"}, "", store)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.values) != 1 || store.values["https://other.example"] != "other-token" {
		t.Fatal("logout removed other server credentials")
	}
	if !strings.Contains(stderr, "still active") || strings.Contains(stderr, "environment-token") {
		t.Fatal("override warning missing or leaked token")
	}
	if _, _, err := authExecute([]string{"logout"}, "", store); err != nil {
		t.Fatal("logout was not idempotent")
	}
}
func TestAPIUsesSavedTokenAndEnvironmentTakesPrecedence(t *testing.T) {
	isolate(t)
	want := "saved-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+want {
			t.Error("wrong authentication source")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"devices":[]}`)
	}))
	defer server.Close()
	store := newFakeCredentials()
	store.values[server.URL] = "saved-token"
	if _, _, err := authExecute([]string{"get", "devices", "--server", server.URL}, "", store); err != nil {
		t.Fatal(err)
	}
	if store.reads != 1 {
		t.Fatal("keychain was not consulted")
	}
	t.Setenv("TAILCTL_TOKEN", "environment-token")
	want = "environment-token"
	store.getErr = errors.New("keychain locked")
	if _, _, err := authExecute([]string{"get", "devices", "--server", server.URL}, "", store); err != nil {
		t.Fatal(err)
	}
	if store.reads != 1 {
		t.Fatal("environment override touched keychain")
	}
}
func TestKeychainIsLazyForConfigAndCatalogue(t *testing.T) {
	isolate(t)
	store := newFakeCredentials()
	store.getErr = errors.New("keychain unavailable")
	for _, args := range [][]string{{"api", "list"}, {"config", "view", "-o", "json"}, {"plugin", "list"}, {"completion", "bash"}, {"--help"}} {
		if _, _, err := authExecute(args, "", store); err != nil {
			t.Fatal(err)
		}
	}
	if store.reads != 0 {
		t.Fatal("read-only metadata command touched keychain")
	}
}
func TestCancellationDoesNotSaveToken(t *testing.T) {
	isolate(t)
	store := newFakeCredentials()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := Execute(ctx, []string{"login", "--token-stdin"}, Options{Credentials: store}, strings.NewReader("fixture-token"), &out, &out)
	if !errors.Is(err, context.Canceled) || store.writes != 0 {
		t.Fatal("cancelled login saved a token")
	}
}
func TestExternalPluginsDoNotReceiveSavedToken(t *testing.T) {
	isolate(t)
	store := newFakeCredentials()
	store.values["https://api.tailscale.com/api/v2"] = "saved-token"
	// Unknown command discovery must not retrieve credentials for a subprocess.
	_, _, _ = authExecute([]string{"missing-plugin-for-test"}, "", store)
	if store.reads != 0 {
		t.Fatal("plugin dispatch retrieved the saved token")
	}
}

func TestYAMLTokenOverridesKeychain(t *testing.T) {
	isolate(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer yaml-token" {
			t.Error("YAML override not used")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"devices":[]}`)
	}))
	defer server.Close()
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("token: yaml-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store := newFakeCredentials()
	store.getErr = errors.New("keychain must remain untouched")
	if _, _, err := authExecute([]string{"get", "devices", "--config", configFile, "--server", server.URL}, "", store); err != nil {
		t.Fatal(err)
	}
	if store.reads != 0 {
		t.Fatal("YAML override accessed keychain")
	}
}
