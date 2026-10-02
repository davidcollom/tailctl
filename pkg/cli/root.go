// Package cli exposes a composable Cobra command tree for in-process extensions.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/davidcollom/tailctl/pkg/api"
	"github.com/davidcollom/tailctl/pkg/client"
	"github.com/davidcollom/tailctl/pkg/config"
	"github.com/davidcollom/tailctl/pkg/credentials"
	"github.com/davidcollom/tailctl/pkg/output"
	"github.com/davidcollom/tailctl/pkg/plugin"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Extension adds a Go command with access to the same configuration and client.
type Extension interface {
	Command(*Runtime) (*cobra.Command, error)
}
type Options struct {
	Version    string
	ConfigPath string
	Extensions []Extension
	// Credentials defaults to the system keychain; inject a fake for tests.
	Credentials credentials.Store
}
type Runtime struct {
	Config      config.Config
	Viper       *viper.Viper
	Credentials credentials.Store
	noHeaders   bool
	sortBy      string
}

func (r *Runtime) Client() (*client.Client, error) {
	token := r.Config.Token
	if token == "" {
		store := r.Credentials
		if store == nil {
			store = credentials.System{}
		}
		var err error
		token, err = store.Get(r.Config.Server)
		if errors.Is(err, credentials.ErrNotFound) {
			return nil, errors.New("API token required: run tailctl login or set TAILCTL_TOKEN")
		}
		if err != nil {
			return nil, errors.New("OS credential store unavailable; unlock or enable it, or supply TAILCTL_TOKEN for this process")
		}
	}
	return client.New(client.Options{Token: token, Tailnet: r.Config.Tailnet, Server: r.Config.Server, Timeout: r.Config.Timeout})
}
func (r *Runtime) Print(cmd *cobra.Command, value any, columns []output.Column) error {
	return output.Write(cmd.OutOrStdout(), value, output.Options{Format: r.Config.Output, NoHeaders: r.noHeaders, SortBy: r.sortBy, Columns: columns})
}
func NewRoot(o Options) (*cobra.Command, error) {
	store := o.Credentials
	if store == nil {
		store = credentials.System{}
	}
	r := &Runtime{Credentials: store}
	path := o.ConfigPath
	root := &cobra.Command{Use: "tailctl", Short: "Manage the Tailscale API with readable output and extensible commands", Version: o.Version, SilenceUsage: true, SilenceErrors: true}
	flags := root.PersistentFlags()
	flags.StringVar(&path, "config", path, "Config file (default ~/.config/tailctl/config.yaml)")
	flags.String("tailnet", "-", "Tailnet name or '-' for the token's tailnet")
	flags.String("server", client.DefaultServer, "Tailscale API base URL")
	flags.StringP("output", "o", "table", "Output: table, wide, json, yaml")
	flags.Duration("timeout", 30_000_000_000, "HTTP request timeout")
	flags.BoolVar(&r.noHeaders, "no-headers", false, "Omit table headers")
	flags.StringVar(&r.sortBy, "sort-by", "", "Sort table rows by JSON field")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if path == "" {
			path = os.Getenv("TAILCTL_CONFIG")
		}
		v, err := config.New(path)
		if err != nil {
			return err
		}
		for _, key := range []string{"tailnet", "server", "output", "timeout"} {
			if err := v.BindPFlag(key, flags.Lookup(key)); err != nil {
				return err
			}
		}
		c, err := config.Decode(v)
		if err != nil {
			return err
		}
		r.Config = c
		r.Viper = v
		return nil
	}
	root.AddCommand(getCommand(r), apiCommand(r), configCommand(r, &path), pluginCommand(r), loginCommand(r), logoutCommand(r))
	if err := addResourceCommands(root, r); err != nil {
		return nil, err
	}
	for _, ext := range o.Extensions {
		cmd, err := ext.Command(r)
		if err != nil {
			return nil, err
		}
		if cmd == nil || cmd.Name() == "" {
			return nil, errors.New("extension returned an empty command")
		}
		for _, existing := range root.Commands() {
			if existing.Name() == cmd.Name() || existing.HasAlias(cmd.Name()) {
				return nil, fmt.Errorf("extension shadows built-in command %q", cmd.Name())
			}
			for _, alias := range cmd.Aliases {
				if alias == existing.Name() || existing.HasAlias(alias) {
					return nil, fmt.Errorf("extension alias shadows command %q", alias)
				}
			}
		}
		if cmd.Name() == "help" || cmd.Name() == "completion" {
			return nil, fmt.Errorf("reserved command %q", cmd.Name())
		}
		root.AddCommand(cmd)
	}
	return root, nil
}

// Execute dispatches built-ins first, then a longest-prefix executable plugin.
// For external plugins, flags after the command are passed through untouched.
func Execute(ctx context.Context, args []string, o Options, in io.Reader, out, stderr io.Writer) error {
	root, err := NewRoot(o)
	if err != nil {
		return err
	}
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(stderr)
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		builtin := args[0] == "help" || args[0] == "completion"
		for _, cmd := range root.Commands() {
			if cmd.Name() == args[0] || cmd.HasAlias(args[0]) {
				builtin = true
			}
		}
		if !builtin {
			path := o.ConfigPath
			if path == "" {
				path = os.Getenv("TAILCTL_CONFIG")
			}
			v, err := config.New(path)
			if err != nil {
				return err
			}
			c, err := config.Decode(v)
			if err != nil {
				return err
			}
			plugins, err := plugin.Discover(c.PluginDirs)
			if err != nil {
				return err
			}
			if p, rest, ok := plugin.Resolve(plugins, args); ok {
				env := map[string]string{"TAILCTL_TAILNET": c.Tailnet, "TAILCTL_OUTPUT": c.Output, "TAILCTL_SERVER": c.Server, "TAILCTL_TIMEOUT": c.Timeout.String()}
				if path != "" {
					env["TAILCTL_CONFIG"] = path
				}
				return plugin.Run(ctx, p, rest, in, out, stderr, env)
			}
		}
	}
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}
func getCommand(r *Runtime) *cobra.Command {
	get := &cobra.Command{
		Use:        "get",
		Short:      "List and inspect resources",
		Deprecated: "use the resource-first commands instead",
		Hidden:     true,
	}
	devices := &cobra.Command{Use: "devices [id]", Aliases: []string{"device", "nodes"}, Short: "List devices or get one device", Deprecated: "use 'tailctl devices list' or 'tailctl devices get DEVICE-ID' instead", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, err := r.Client()
		if err != nil {
			return err
		}
		cols := []output.Column{{Header: "ID", Field: "nodeId"}, {Header: "NAME", Field: "hostname"}, {Header: "OS", Field: "os"}, {Header: "ADDRESSES", Field: "addresses"}, {Header: "AUTHORISED", Field: "authorized"}}
		if r.Config.Output == "wide" {
			cols = append(cols, output.Column{Header: "USER", Field: "user"}, output.Column{Header: "LAST SEEN", Field: "lastSeen"}, output.Column{Header: "VERSION", Field: "clientVersion"})
		}
		if len(args) > 0 {
			d, err := c.Device(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return r.Print(cmd, d, cols)
		}
		ds, err := c.Devices(cmd.Context())
		if err != nil {
			return err
		}
		return r.Print(cmd, ds, cols)
	}}
	users := &cobra.Command{Use: "users", Aliases: []string{"user"}, Short: "List users", Deprecated: "use 'tailctl users list' instead", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		c, err := r.Client()
		if err != nil {
			return err
		}
		v, err := c.Users(cmd.Context())
		if err != nil {
			return err
		}
		return r.Print(cmd, v, []output.Column{{Header: "ID", Field: "id"}, {Header: "LOGIN", Field: "loginName"}, {Header: "NAME", Field: "displayName"}, {Header: "ROLE", Field: "role"}, {Header: "STATUS", Field: "status"}})
	}}
	get.AddCommand(devices, users)
	for _, resource := range []struct {
		name, operation, envelope string
		columns                   []output.Column
	}{
		{"keys", "listTailnetKeys", "keys", []output.Column{{Header: "ID", Field: "id"}, {Header: "CREATED", Field: "created"}, {Header: "EXPIRES", Field: "expires"}}},
		{"services", "listServices", "vipServices", []output.Column{{Header: "NAME", Field: "name"}, {Header: "COMMENT", Field: "comment"}, {Header: "PORTS", Field: "ports"}}},
		{"dns", "getDnsConfiguration", "", nil}, {"settings", "getTailnetSettings", "", nil}, {"policy", "getPolicyFile", "", nil},
	} {
		resource := resource
		canonical := "tailctl " + resource.name + " get"
		if resource.name == "keys" || resource.name == "services" {
			canonical = "tailctl " + resource.name + " list"
		}
		get.AddCommand(&cobra.Command{Use: resource.name, Short: "Get " + resource.name, Deprecated: "use '" + canonical + "' instead", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			c, err := r.Client()
			if err != nil {
				return err
			}
			raw, err := c.Call(cmd.Context(), resource.operation, nil, nil, nil, "")
			if err != nil {
				return err
			}
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			if resource.envelope != "" {
				if obj, ok := value.(map[string]any); ok {
					value = obj[resource.envelope]
					if value == nil {
						value = []any{}
					}
				}
			}
			return r.Print(cmd, value, resource.columns)
		}})
	}
	return get
}
func apiCommand(r *Runtime) *cobra.Command {
	command := &cobra.Command{Use: "api", Short: "Discover and call all schema-defined API operations"}
	command.AddCommand(describeCommand(r))
	command.AddCommand(&cobra.Command{Use: "list", Short: "List generated operation IDs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		operations := api.Operations()
		rows := []map[string]string{}
		commands := map[string]string{}
		for _, binding := range ResourceBindings() {
			commands[binding.Operation] = "tailctl " + binding.Command
		}
		keys := []string{}
		for k := range operations {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			op := operations[k]
			rows = append(rows, map[string]string{"id": op.ID, "method": op.Method, "path": op.Path, "summary": op.Summary, "command": commands[k]})
		}
		return r.Print(cmd, rows, []output.Column{{Header: "OPERATION", Field: "id"}, {Header: "METHOD", Field: "method"}, {Header: "PATH", Field: "path"}, {Header: "COMMAND", Field: "command"}})
	}})
	var params, queries []string
	var file, contentType string
	var yes bool
	call := &cobra.Command{Use: "call OPERATION", Short: "Call an operation (--yes required for writes)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		op, ok := api.Operations()[args[0]]
		if !ok {
			return fmt.Errorf("unknown operation %q; run tailctl api list", args[0])
		}
		if op.Method != http.MethodGet && op.Method != http.MethodHead && !yes {
			return fmt.Errorf("%s %s changes resources; pass --yes to proceed", op.Method, op.Path)
		}
		p, err := pairs(params)
		if err != nil {
			return err
		}
		q, err := queryPairs(queries)
		if err != nil {
			return err
		}
		var body io.Reader
		if file != "" {
			if file == "-" {
				body = cmd.InOrStdin()
			} else {
				f, err := os.Open(file)
				if err != nil {
					return err
				}
				defer f.Close()
				body = f
			}
		}
		c, err := r.Client()
		if err != nil {
			return err
		}
		raw, err := c.Call(cmd.Context(), args[0], p, q, body, contentType)
		if err != nil {
			return err
		}
		return r.Print(cmd, raw, nil)
	}}
	call.Flags().StringArrayVar(&params, "param", nil, "Path parameter KEY=VALUE (repeatable)")
	call.Flags().StringArrayVar(&queries, "query", nil, "Query parameter KEY=VALUE (repeatable)")
	call.Flags().StringVarP(&file, "file", "f", "", "Body file, or '-' for stdin")
	call.Flags().StringVar(&contentType, "content-type", "application/json", "Request body media type")
	call.Flags().BoolVar(&yes, "yes", false, "Explicitly allow an API mutation")
	command.AddCommand(call)
	return command
}
func pairs(values []string) (map[string]string, error) {
	out := map[string]string{}
	for _, s := range values {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("expected KEY=VALUE, got %q", s)
		}
		if _, exists := out[k]; exists {
			return nil, fmt.Errorf("duplicate path parameter %q", k)
		}
		out[k] = v
	}
	return out, nil
}
func queryPairs(values []string) (url.Values, error) {
	out := url.Values{}
	for _, s := range values {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("expected KEY=VALUE, got %q", s)
		}
		out.Add(k, v)
	}
	return out, nil
}
func configCommand(r *Runtime, path *string) *cobra.Command {
	command := &cobra.Command{Use: "config", Short: "Inspect configuration"}
	command.AddCommand(&cobra.Command{Use: "path", Short: "Show selected configuration file", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		p := *path
		if p == "" {
			var err error
			p, err = config.DefaultPath()
			if err != nil {
				return err
			}
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), p)
		return err
	}})
	command.AddCommand(&cobra.Command{Use: "view", Short: "Show effective configuration with token redacted", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		token := "<keychain lookup deferred>"
		if r.Config.Token != "" {
			token = "<redacted>"
		}
		return r.Print(cmd, map[string]any{"tailnet": r.Config.Tailnet, "server": r.Config.Server, "token": token, "output": r.Config.Output, "timeout": r.Config.Timeout.String(), "plugin_dirs": r.Config.PluginDirs}, nil)
	}})
	return command
}
func pluginCommand(r *Runtime) *cobra.Command {
	command := &cobra.Command{Use: "plugin", Short: "Inspect external executable plugins"}
	command.AddCommand(&cobra.Command{Use: "list", Short: "List discovered plugins without executing them", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		ps, err := plugin.Discover(r.Config.PluginDirs)
		if err != nil {
			return err
		}
		rows := []map[string]string{}
		for _, p := range ps {
			rows = append(rows, map[string]string{"name": p.Name, "path": p.Path})
		}
		return r.Print(cmd, rows, []output.Column{{Header: "NAME", Field: "name"}, {Header: "PATH", Field: "path"}})
	}})
	return command
}
