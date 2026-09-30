// Package credentials stores API tokens in the operating system's credential store.
package credentials

import (
	"errors"
	"net/url"
	"strings"

	"github.com/zalando/go-keyring"
)

// ErrNotFound indicates no saved token for the requested API server.
var ErrNotFound = keyring.ErrNotFound

// Store allows callers to inject a fake without touching a real keychain.
// Tokens are scoped to the API server so an alternate --server cannot reuse them.
type Store interface {
	Get(server string) (string, error)
	Set(server, token string) error
	Delete(server string) error
}

// System uses macOS Keychain, Windows Credential Manager or Linux Secret Service.
// It never falls back to a plaintext file.
type System struct{}

const service = "tailctl"

// Account returns the canonical URL used as the credential account name.
func Account(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid API server for credential storage")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return "", errors.New("credential server must use HTTPS (HTTP allowed only on localhost)")
	}
	if u.Scheme == "https" && u.Port() == "443" {
		u.Host = strings.TrimSuffix(u.Host, ":443")
	}
	if u.Scheme == "http" && u.Port() == "80" {
		u.Host = strings.TrimSuffix(u.Host, ":80")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = strings.TrimRight(u.RawPath, "/")
	return u.String(), nil
}

func (System) Get(server string) (string, error) {
	account, err := Account(server)
	if err != nil {
		return "", err
	}
	return keyring.Get(service, account)
}
func (System) Set(server, token string) error {
	account, err := Account(server)
	if err != nil {
		return err
	}
	return keyring.Set(service, account, token)
}
func (System) Delete(server string) error {
	account, err := Account(server)
	if err != nil {
		return err
	}
	return keyring.Delete(service, account)
}
