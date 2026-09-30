// Package client provides an authenticated facade over the generated Tailscale API.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/davidcollom/tailctl/pkg/api"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const DefaultServer = "https://api.tailscale.com/api/v2"
const MaxResponseBytes = 16 << 20

type Options struct {
	Token      string
	Tailnet    string
	Server     string
	HTTPClient *http.Client
	Timeout    time.Duration
}
type Client struct {
	// API exposes every generated, typed endpoint for advanced callers.
	API     *api.ClientWithResponses
	tailnet string
	server  string
	http    *http.Client
}

// APIError preserves status without exposing response bodies, which can contain secrets.
type APIError struct {
	StatusCode int
	RequestID  string
	RetryAfter string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Tailscale API returned HTTP %d (request ID: %s)", e.StatusCode, e.RequestID)
}

type authTransport struct {
	base   http.RoundTripper
	token  string
	origin *url.URL
}

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != t.origin.Scheme || req.URL.Host != t.origin.Host {
		return nil, errors.New("refusing to send credentials to another origin")
	}
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	clone.Header.Set("User-Agent", "tailctl")
	resp, err := t.base.RoundTrip(clone)
	if err != nil {
		return nil, err
	}
	resp.Body = &boundedBody{ReadCloser: resp.Body, remaining: MaxResponseBytes}
	return resp, nil
}

type boundedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		var probe [1]byte
		n, err := b.ReadCloser.Read(probe[:])
		if n > 0 {
			return 0, errors.New("API response exceeds 16 MiB")
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}
func New(o Options) (*Client, error) {
	if strings.TrimSpace(o.Token) == "" {
		return nil, errors.New("API token required: set TAILCTL_TOKEN or token in config")
	}
	if strings.ContainsAny(o.Token, "\r\n") {
		return nil, errors.New("invalid token")
	}
	if o.Server == "" {
		o.Server = DefaultServer
	}
	if o.Tailnet == "" {
		o.Tailnet = "-"
	}
	u, err := url.Parse(o.Server)
	if err != nil {
		return nil, err
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
		return nil, errors.New("server must be an absolute URL without credentials, query or fragment")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return nil, errors.New("server must use HTTPS (HTTP allowed only for localhost tests)")
	}
	h := http.Client{Timeout: 30 * time.Second}
	if o.HTTPClient != nil {
		h = *o.HTTPClient
	}
	if o.Timeout < 0 {
		return nil, errors.New("timeout must be positive")
	}
	if o.Timeout > 0 {
		h.Timeout = o.Timeout
	}
	if h.Timeout == 0 {
		h.Timeout = 30 * time.Second
	}
	base := h.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	h.Transport = authTransport{base: base, token: o.Token, origin: u}
	// Never follow API redirects, including same-origin mutation redirects.
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	generated, err := api.NewClientWithResponses(strings.TrimRight(o.Server, "/"), api.WithHTTPClient(&h))
	if err != nil {
		return nil, err
	}
	return &Client{API: generated, tailnet: o.Tailnet, server: strings.TrimRight(o.Server, "/"), http: &h}, nil
}
func check(resp *http.Response) error {
	if resp == nil {
		return errors.New("missing API response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{StatusCode: resp.StatusCode, RequestID: resp.Header.Get("X-Request-ID"), RetryAfter: resp.Header.Get("Retry-After")}
	}
	return nil
}
func (c *Client) Devices(ctx context.Context) ([]api.Device, error) {
	r, err := c.API.ListTailnetDevicesWithResponse(ctx, c.tailnet, nil)
	if err != nil {
		return nil, err
	}
	if err := check(r.HTTPResponse); err != nil {
		return nil, err
	}
	if r.JSON200 == nil {
		return nil, errors.New("expected JSON devices response")
	}
	if r.JSON200.Devices == nil {
		return []api.Device{}, nil
	}
	return *r.JSON200.Devices, nil
}
func (c *Client) Device(ctx context.Context, id string) (*api.Device, error) {
	r, err := c.API.GetDeviceWithResponse(ctx, id, nil)
	if err != nil {
		return nil, err
	}
	if err := check(r.HTTPResponse); err != nil {
		return nil, err
	}
	if r.JSON200 == nil {
		return nil, errors.New("expected JSON device response")
	}
	return r.JSON200, nil
}
func (c *Client) Users(ctx context.Context) ([]api.User, error) {
	r, err := c.API.ListUsersWithResponse(ctx, c.tailnet, nil)
	if err != nil {
		return nil, err
	}
	if err := check(r.HTTPResponse); err != nil {
		return nil, err
	}
	if r.JSON200 == nil {
		return nil, errors.New("expected JSON users response")
	}
	if r.JSON200.Users == nil {
		return []api.User{}, nil
	}
	return *r.JSON200.Users, nil
}

var pathParameter = regexp.MustCompile(`\{([^}]+)\}`)

// Call invokes a schema-catalogued operation. Parameters are URL-escaped, never interpolated raw.
// Mutations are allowed here; the CLI applies its own --yes guard.
func (c *Client) Call(ctx context.Context, id string, params map[string]string, query url.Values, body io.Reader, contentType string) (json.RawMessage, error) {
	op, ok := api.Operations()[id]
	if !ok {
		return nil, fmt.Errorf("unknown API operation %q", id)
	}
	allowed := map[string]bool{}
	for _, match := range pathParameter.FindAllStringSubmatch(op.Path, -1) {
		allowed[match[1]] = true
	}
	for key := range params {
		if !allowed[key] {
			return nil, fmt.Errorf("unknown path parameter %q for %s", key, id)
		}
	}
	missing := ""
	path := pathParameter.ReplaceAllStringFunc(op.Path, func(s string) string {
		key := s[1 : len(s)-1]
		value := params[key]
		if key == "tailnet" && value == "" {
			value = c.tailnet
		}
		if value == "" {
			missing = key
		}
		return url.PathEscape(value)
	})
	if missing != "" {
		return nil, fmt.Errorf("missing path parameter %q", missing)
	}
	address := c.server + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, op.Method, address, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := check(resp); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return json.RawMessage("null"), nil
	}
	if !json.Valid(data) {
		return nil, errors.New("API returned non-JSON content; use the generated API client for HuJSON or binary responses")
	}
	return json.RawMessage(data), nil
}
