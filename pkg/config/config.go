// Package config loads isolated Viper configuration for each invocation.
package config

import (
	"errors"
	"fmt"
	"github.com/spf13/viper"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Tailnet    string        `mapstructure:"tailnet"`
	Token      string        `mapstructure:"token"`
	Server     string        `mapstructure:"server"`
	Output     string        `mapstructure:"output"`
	Timeout    time.Duration `mapstructure:"timeout"`
	PluginDirs []string      `mapstructure:"plugin_dirs"`
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tailctl", "config.yaml"), nil
}

// New uses flags > environment > file > defaults. Bind Cobra flags to this instance.
func New(path string) (*viper.Viper, error) {
	v := viper.New()
	v.SetDefault("tailnet", "-")
	v.SetDefault("server", "https://api.tailscale.com/api/v2")
	v.SetDefault("output", "table")
	v.SetDefault("timeout", "30s")
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	v.SetDefault("plugin_dirs", []string{filepath.Join(home, ".config", "tailctl", "plugins")})
	v.SetEnvPrefix("TAILCTL")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	for _, key := range []string{"tailnet", "token", "server", "output", "timeout", "plugin_dirs"} {
		if err := v.BindEnv(key); err != nil {
			return nil, err
		}
	}
	explicit := path != ""
	if !explicit {
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return v, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return v, nil
}
func Decode(v *viper.Viper) (Config, error) {
	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return c, err
	}
	if c.Timeout <= 0 {
		return c, fmt.Errorf("timeout must be positive")
	}
	if c.Tailnet == "" {
		return c, fmt.Errorf("tailnet must not be empty")
	}
	switch c.Output {
	case "table", "wide", "json", "yaml":
	default:
		return c, fmt.Errorf("unsupported output %q", c.Output)
	}
	return c, nil
}
