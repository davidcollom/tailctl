package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDevicesAndAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/devices" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing bearer authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"devices":[{"nodeId":"n1","hostname":"worker","addresses":["100.64.0.1"]}]}`)
	}))
	defer server.Close()
	c, err := New(Options{Token: "secret", Tailnet: "example.com", Server: server.URL + "/api/v2"})
	if err != nil {
		t.Fatal(err)
	}
	ds, err := c.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || *ds[0].Hostname != "worker" {
		t.Fatalf("devices: %#v", ds)
	}
}
func TestCallEscapesPathAndRetainsQueries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v2/device/a%2Fb" {
			t.Errorf("escaped path: %s", r.URL.EscapedPath())
		}
		if got := r.URL.Query()["tags"]; len(got) != 2 {
			t.Errorf("query: %v", got)
		}
		fmt.Fprint(w, `{"nodeId":"a/b"}`)
	}))
	defer server.Close()
	c, err := New(Options{Token: "secret", Server: server.URL + "/api/v2"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Call(context.Background(), "getDevice", map[string]string{"deviceId": "a/b"}, url.Values{"tags": {"tag:a", "tag:b"}}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Call(context.Background(), "getDevice", nil, nil, nil, "")
	if err == nil {
		t.Fatal("missing path parameter accepted")
	}
	_, err = c.Call(context.Background(), "getDevice", map[string]string{"deviceId": "n1", "typo": "x"}, nil, nil, "")
	if err == nil {
		t.Fatal("unknown path parameter accepted")
	}
	_, err = c.Call(context.Background(), "imaginary", nil, nil, nil, "")
	if err == nil {
		t.Fatal("unknown operation accepted")
	}
}
func TestAPIErrorDoesNotLeakResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "request-1")
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(429)
		fmt.Fprint(w, `{"secret":"sensitive-response"}`)
	}))
	defer server.Close()
	c, err := New(Options{Token: "secret", Server: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Call(context.Background(), "getDevice", map[string]string{"deviceId": "n1"}, nil, nil, "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 || apiErr.RetryAfter != "10" {
		t.Fatalf("error: %v", err)
	}
	if strings.Contains(err.Error(), "sensitive-response") {
		t.Fatal("response leaked")
	}
}
func TestRedirectNotFollowed(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	c, err := New(Options{Token: "secret", Server: source.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Call(context.Background(), "getDevice", map[string]string{"deviceId": "n1"}, nil, nil, "")
	if err == nil || called {
		t.Fatalf("redirect followed: %v", err)
	}
}
func TestBoundedResponse(t *testing.T) {
	body := &boundedBody{ReadCloser: io.NopCloser(strings.NewReader("12345")), remaining: 4}
	if _, err := io.ReadAll(body); err == nil {
		t.Fatal("oversized response accepted")
	}
	exact := &boundedBody{ReadCloser: io.NopCloser(strings.NewReader("1234")), remaining: 4}
	if _, err := io.ReadAll(exact); err != nil {
		t.Fatal(err)
	}
}
func TestInvalidClientOptions(t *testing.T) {
	for _, o := range []Options{{}, {Token: "x", Server: "http://example.com"}, {Token: "x", Server: "https://user:pass@example.com"}, {Token: "x", Server: "https://example.com?x=y"}, {Token: "x\n"}, {Token: "x", Timeout: -time.Second}} {
		if _, err := New(o); err == nil {
			t.Fatalf("invalid options accepted: %#v", o)
		}
	}
}
func TestCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "{}") }))
	defer server.Close()
	c, err := New(Options{Token: "secret", Server: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Call(ctx, "getDevice", map[string]string{"deviceId": "n1"}, nil, nil, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
