package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/davidcollom/tailctl/pkg/api"
)

func schemaFixture(catalog *schemaCatalog, node schemaNode) any {
	node = catalog.resolve(node)
	if parts, ok := node["allOf"].([]any); ok {
		value := map[string]any{}
		for _, part := range parts {
			for key, item := range object(schemaFixture(catalog, object(part))) {
				value[key] = item
			}
		}
		return value
	}
	if values, ok := node["enum"].([]any); ok && len(values) > 0 {
		return values[0]
	}
	for _, union := range []string{"oneOf", "anyOf"} {
		if values, ok := node[union].([]any); ok && len(values) > 0 {
			return schemaFixture(catalog, object(values[0]))
		}
	}
	switch stringValue(node, "type") {
	case "array":
		return []any{}
	case "boolean":
		return true
	case "number", "integer":
		return json.Number("1")
	case "null":
		return nil
	case "object":
		value := map[string]any{}
		required, _ := node["required"].([]any)
		for _, name := range required {
			key := name.(string)
			value[key] = schemaFixture(catalog, object(object(node["properties"])[key]))
		}
		return value
	default:
		return "fixture"
	}
}
func commandArgs(catalog *schemaCatalog, binding ResourceBinding) ([]string, operationSchema, map[string]string) {
	info := catalog.operation(api.Operations()[binding.Operation])
	args := strings.Fields(binding.Command)
	params := map[string]string{}
	for _, parameter := range info.parameters {
		key := stringValue(parameter, "name")
		node := catalog.resolve(object(parameter["schema"]))
		if parameter["in"] == "path" && key != "tailnet" {
			value := fmt.Sprint(schemaFixture(catalog, node))
			args = append(args, value)
			params[key] = value
		}
		if parameter["in"] == "query" && boolValue(parameter, "required") && key != "all" {
			args = append(args, "--"+flagName(key), fmt.Sprint(schemaFixture(catalog, node)))
		}
	}
	return args, info, params
}
func TestEverySchemaOperationHasExecutableResourceCommand(t *testing.T) {
	isolate(t)
	t.Setenv("TAILCTL_TOKEN", "fixture-token")
	catalog, err := newSchemaCatalog()
	if err != nil {
		t.Fatal(err)
	}
	bindings := ResourceBindings()
	if len(bindings) != len(api.Operations()) {
		t.Fatalf("coverage: %d/%d", len(bindings), len(api.Operations()))
	}
	observed := map[string]bool{}
	for _, binding := range bindings {
		t.Run(binding.Operation, func(t *testing.T) {
			args, info, params := commandArgs(catalog, binding)
			expectedPath := info.operation.Path
			expectedPath = strings.ReplaceAll(expectedPath, "{tailnet}", "test.example")
			for key, value := range params {
				expectedPath = strings.ReplaceAll(expectedPath, "{"+key+"}", value)
			}
			contents := object(info.body["content"])
			var expectedBody []byte
			if len(contents) > 0 {
				expectedBody, _ = json.Marshal(schemaFixture(catalog, object(object(contents["application/json"])["schema"])))
				args = append(args, "--file", "-")
			}
			if info.operation.Method != "GET" {
				args = append(args, "--yes")
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				observed[binding.Operation] = true
				if r.Method != info.operation.Method || r.URL.Path != "/api/v2"+expectedPath {
					t.Errorf("request: %s %s, want %s %s", r.Method, r.URL.Path, info.operation.Method, expectedPath)
				}
				if r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("authentication missing")
				}
				body, _ := io.ReadAll(r.Body)
				if !bytes.Equal(body, expectedBody) {
					t.Errorf("body mismatch: got %s want %s", body, expectedBody)
				}
				for _, parameter := range info.parameters {
					if parameter["in"] == "query" && boolValue(parameter, "required") {
						if _, ok := r.URL.Query()[stringValue(parameter, "name")]; !ok {
							t.Errorf("missing required query %v", parameter["name"])
						}
					}
				}
				fmt.Fprint(w, `{"ok":true}`)
			}))
			defer server.Close()
			args = append(args, "--server", server.URL+"/api/v2", "--tailnet", "test.example", "-o", "json")
			var out bytes.Buffer
			err := Execute(context.Background(), args, Options{}, bytes.NewReader(expectedBody), &out, &out)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("requests: %d", calls)
			}
		})
	}
	if len(observed) != len(api.Operations()) {
		t.Fatalf("exercised %d operations", len(observed))
	}
}
func TestEveryActionRequiresYesBeforeCredentialLookup(t *testing.T) {
	isolate(t)
	catalog, _ := newSchemaCatalog()
	for _, binding := range ResourceBindings() {
		if api.Operations()[binding.Operation].Method == "GET" {
			continue
		}
		t.Run(binding.Operation, func(t *testing.T) {
			args, _, _ := commandArgs(catalog, binding)
			store := &fakeCredentials{}
			var out bytes.Buffer
			err := Execute(context.Background(), args, Options{Credentials: store}, strings.NewReader(""), &out, &out)
			if err == nil || !strings.Contains(err.Error(), "--yes") {
				t.Fatalf("guard: %v", err)
			}
			if store.reads != 0 {
				t.Fatal("looked up credentials before action guard")
			}
		})
	}
}
func TestResourceFlagsEncodeBodiesAndQueries(t *testing.T) {
	cases := []struct {
		args  []string
		body  string
		query string
	}{
		{[]string{"devices", "authorise", "id", "--authorized=false"}, `{"authorized":false}`, ""},
		{[]string{"devices", "rename", "id", "--name="}, `{"name":""}`, ""},
		{[]string{"devices", "routes", "set", "id", "--routes", "10.0.0.0/8", "--routes", "192.168.0.0/16"}, `{"routes":["10.0.0.0/8","192.168.0.0/16"]}`, ""},
		{[]string{"devices", "tags", "set", "id", "--tags-json", "[]"}, `{"tags":[]}`, ""},
		{[]string{"dns", "preferences", "set", "--magic-dns=false"}, `{"magicDNS":false}`, ""},
		{[]string{"keys", "create", "--key-type", "client", "--scopes", "devices:read", "--expiry-seconds", "3600"}, `{"expirySeconds":3600,"keyType":"client","scopes":["devices:read"]}`, ""},
		{[]string{"users", "list", "--type", "shared", "--role", "admin"}, "", "role=admin&type=shared"},
		{[]string{"devices", "list", "--fields", "all", "--filter", "hostname=worker"}, "", "fields=all&hostname=worker"},
		{[]string{"keys", "list", "--all=false"}, "", "all=false"},
		{[]string{"keys", "list", "--query", "all=false"}, "", "all=false"},
		{[]string{"services", "approval", "set", "svc:web", "id", "--approved=false"}, `{"approved":false}`, ""},
		{[]string{"services", "set", "svc:web", "--ports", "tcp:443", "--display-name", "Web"}, `{"displayName":"Web","ports":["tcp:443"]}`, ""},
		{[]string{"devices", "attributes", "set", "id", "custom:healthy", "--value-json", "true"}, `{"value":true}`, ""},
		{[]string{"oauth-apps", "create", "--name", "test", "--redirect-uris", "https://example.com/callback", "--scopes", "auth_keys:create:once"}, `{"name":"test","redirectURIs":["https://example.com/callback"],"scopes":["auth_keys:create:once"]}`, ""},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			isolate(t)
			t.Setenv("TAILCTL_TOKEN", "fixture")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if string(body) != tc.body {
					t.Errorf("body: %s want %s", body, tc.body)
				}
				if r.URL.RawQuery != tc.query {
					t.Errorf("query: %s want %s", r.URL.RawQuery, tc.query)
				}
				w.WriteHeader(204)
			}))
			defer server.Close()
			var out bytes.Buffer
			args := append(append([]string{}, tc.args...), "--server", server.URL)
			if tc.body != "" {
				args = append(args, "--yes")
			}
			if err := Execute(context.Background(), args, Options{}, nil, &out, &out); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestInvalidInputsFailBeforeNetworkAndCredentialLookup(t *testing.T) {
	cases := []struct {
		args  []string
		input string
	}{
		{[]string{"users", "list", "--role", "invalid"}, ""},
		{[]string{"organisations", "tailnets", "list", "org", "--limit", "101"}, ""},
		{[]string{"logs", "audit", "list"}, ""},
		{[]string{"contacts", "update", "invalid", "--email", "a@example.com", "--yes"}, ""},
		{[]string{"devices", "authorise", "id", "--yes", "--file", "-"}, `{"authorized":"yes","clientSecret":"must-not-leak"}`},
		{[]string{"devices", "rename", "id", "--yes"}, ""},
		{[]string{"devices", "rename", "id", "--yes", "--name", "test", "--file", "-"}, `{"name":"other"}`},
		{[]string{"devices", "tags", "set", "id", "--yes", "--tags", "one", "--tags-json", "[]"}, ""},
		{[]string{"dns", "set", "--yes", "--file", "-"}, `{} {}`},
		{[]string{"devices", "delete", "id", "--yes", "-o", "invalid"}, ""},
		{[]string{"devices", "get", "id", "--query", "unknown=x"}, ""},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			isolate(t)
			store := &fakeCredentials{}
			var out bytes.Buffer
			err := Execute(context.Background(), tc.args, Options{Credentials: store}, strings.NewReader(tc.input), &out, &out)
			if err == nil {
				t.Fatal("invalid input accepted")
			}
			if store.reads != 0 {
				t.Fatal("looked up credentials for invalid request")
			}
			if strings.Contains(err.Error()+out.String(), "must-not-leak") {
				t.Fatal("validation error leaked body")
			}
		})
	}
}
func TestRawPolicyAndOptimisticConcurrency(t *testing.T) {
	isolate(t)
	t.Setenv("TAILCTL_TOKEN", "fixture")
	policy := []byte("// comment\n{\"acls\": []}\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/hujson" || r.Header.Get("If-Match") != "revision-1" {
			t.Error("headers missing")
		}
		if r.Header.Get("Content-Type") != "application/hujson" {
			t.Error("media type missing")
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, policy) {
			t.Error("HuJSON changed")
		}
		w.Header().Set("Content-Type", "application/hujson")
		w.Write(policy)
	}))
	defer server.Close()
	var out bytes.Buffer
	err := Execute(context.Background(), []string{"policy", "set", "--yes", "--file", "-", "--content-type", "application/hujson", "--accept", "application/hujson", "--if-match", "revision-1", "--raw", "--server", server.URL}, Options{}, bytes.NewReader(policy), &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), policy) {
		t.Fatal("raw response changed")
	}
}
func TestResponseFileIsReservedBeforeMutationAndPrivate(t *testing.T) {
	isolate(t)
	t.Setenv("TAILCTL_TOKEN", "fixture")
	path := filepath.Join(t.TempDir(), "key.json")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{"id":"k1","key":"sensitive"}`) }))
	defer server.Close()
	args := []string{"keys", "create", "--yes", "--key-type", "auth", "--response-file", path, "--server", server.URL}
	var out, stderr bytes.Buffer
	if err := Execute(context.Background(), args, Options{}, nil, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String()+stderr.String(), "sensitive") {
		t.Fatal("secret echoed")
	}
	data, _ := os.ReadFile(path)
	if string(data) != `{"id":"k1","key":"sensitive"}` {
		t.Fatal("response not preserved")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("response permissions: %v, %v", info, err)
		}
	}
	if err := Execute(context.Background(), args, Options{}, nil, &out, &stderr); err == nil {
		t.Fatal("overwrite accepted")
	}
	if calls != 1 {
		t.Fatal("mutated before discovering occupied response path")
	}
}
func TestResourceTablesUnwrapButJSONPreservesPaginationAndSecrets(t *testing.T) {
	isolate(t)
	t.Setenv("TAILCTL_TOKEN", "fixture")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tailnets":[{"id":"t1","displayName":"Production","oauthClient":{"clientSecret":"sensitive"}}],"cursor":"next","totalCount":2}`)
	}))
	defer server.Close()
	for _, format := range []string{"table", "json"} {
		var out bytes.Buffer
		err := Execute(context.Background(), []string{"organisations", "tailnets", "list", "org", "--server", server.URL, "-o", format}, Options{}, nil, &out, &out)
		if err != nil {
			t.Fatal(err)
		}
		if format == "table" {
			if !strings.Contains(out.String(), "Production") || strings.Contains(out.String(), "sensitive") {
				t.Fatal("bad table")
			}
		} else {
			if !strings.Contains(out.String(), "next") || !strings.Contains(out.String(), "sensitive") {
				t.Fatal("full response lost")
			}
		}
	}
}
func TestResourceHelpAndCompletionDoNotUseCredentials(t *testing.T) {
	isolate(t)
	store := &fakeCredentials{}
	var out bytes.Buffer
	for _, args := range [][]string{{"devices", "routes", "set", "--help"}, {"completion", "bash"}, {"api", "list", "-o", "json"}, {"api", "describe", "createWebhook", "-o", "json"}} {
		if err := Execute(context.Background(), args, Options{Credentials: store}, nil, &out, &out); err != nil {
			t.Fatal(err)
		}
	}
	if store.reads != 0 {
		t.Fatal("credential lookup during discovery")
	}
}

func TestDescribeIncludesTransitiveReferences(t *testing.T) {
	isolate(t)
	var out bytes.Buffer
	if err := Execute(context.Background(), []string{"api", "describe", "updateService", "-o", "json"}, Options{}, nil, &out, &out); err != nil {
		t.Fatal(err)
	}
	var description map[string]any
	if err := json.Unmarshal(out.Bytes(), &description); err != nil {
		t.Fatal(err)
	}
	schemas := object(object(description["components"])["schemas"])
	if schemas["VIPServiceInfoPut"] == nil || schemas["VIPServiceInfo"] == nil {
		t.Fatal("transitive service schemas missing")
	}
	if description["command"] != "tailctl services set" {
		t.Fatal("resource mapping missing")
	}
}

func TestResponseFileRemovedOnAPIError(t *testing.T) {
	isolate(t)
	t.Setenv("TAILCTL_TOKEN", "fixture")
	path := filepath.Join(t.TempDir(), "response.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"secret":"must-not-leak"}`)
	}))
	defer server.Close()
	var out bytes.Buffer
	err := Execute(context.Background(), []string{"keys", "create", "--yes", "--key-type", "auth", "--response-file", path, "--server", server.URL}, Options{}, nil, &out, &out)
	if err == nil || strings.Contains(err.Error()+out.String(), "must-not-leak") {
		t.Fatalf("API error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed response file left behind")
	}
}

func TestGenericResourceTableRedactsNestedCredentials(t *testing.T) {
	isolate(t)
	t.Setenv("TAILCTL_TOKEN", "fixture")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"credentials":{"clientSecret":"must-not-leak"},"enabled":true}`)
	}))
	defer server.Close()
	var out bytes.Buffer
	if err := Execute(context.Background(), []string{"logs", "stream", "get", "configuration", "--server", server.URL}, Options{}, nil, &out, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "must-not-leak") || !strings.Contains(out.String(), "<redacted>") {
		t.Fatal("credential material in table")
	}
}
