package credentials

import (
	"errors"
	"github.com/zalando/go-keyring"
	"testing"
)

func TestAccountCanonicalisationAndValidation(t *testing.T) {
	got, err := Account("https://API.TAILSCALE.COM:443/api/v2/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://api.tailscale.com/api/v2" {
		t.Fatalf("account: %q", got)
	}
	for _, bad := range []string{"http://example.com/api/v2", "https://user:password@example.com", "https://example.com?token=secret", "https://example.com/#fragment", "example.com"} {
		if _, err := Account(bad); err == nil {
			t.Fatal("invalid server accepted")
		}
	}
}
func TestSystemRoundTripIsScopedAndDeleteIsIdempotent(t *testing.T) {
	keyring.MockInit()
	store := System{}
	if err := store.Set("https://api.tailscale.com/api/v2/", "fixture-token"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("https://API.TAILSCALE.COM:443/api/v2")
	if err != nil || got != "fixture-token" {
		t.Fatal("saved token not found through canonical server")
	}
	if _, err := store.Get("https://another.example/api/v2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("token leaked to another server")
	}
	if err := store.Delete("https://api.tailscale.com/api/v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("https://api.tailscale.com/api/v2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("token remained after deletion")
	}
}
