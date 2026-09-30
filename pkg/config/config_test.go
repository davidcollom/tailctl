package config

import (
	"github.com/spf13/pflag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(p, []byte("tailnet: file.example\ntimeout: 15s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILCTL_TAILNET", "env.example")
	v, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Decode(v)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tailnet != "env.example" || c.Timeout != 15*time.Second {
		t.Fatalf("config: %#v", c)
	}
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("tailnet", "-", "")
	if err := flags.Set("tailnet", "flag.example"); err != nil {
		t.Fatal(err)
	}
	if err := v.BindPFlag("tailnet", flags.Lookup("tailnet")); err != nil {
		t.Fatal(err)
	}
	c, err = Decode(v)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tailnet != "flag.example" {
		t.Fatalf("flag precedence: %s", c.Tailnet)
	}
}
func TestMissingDefaultAndExplicitConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := New(""); err != nil {
		t.Fatalf("missing default: %v", err)
	}
	if _, err := New(filepath.Join(home, "missing.yaml")); err == nil {
		t.Fatal("missing explicit config accepted")
	}
}
