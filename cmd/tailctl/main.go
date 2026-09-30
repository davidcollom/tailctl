package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/davidcollom/tailctl/pkg/cli"
	"github.com/davidcollom/tailctl/pkg/plugin"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

func main() { os.Exit(run()) }
func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := cli.Execute(ctx, os.Args[1:], cli.Options{Version: version}, os.Stdin, os.Stdout, os.Stderr)
	if err == nil {
		return 0
	}
	var pluginExit *plugin.ExitError
	if errors.As(err, &pluginExit) {
		return pluginExit.Code
	}
	fmt.Fprintln(os.Stderr, "Error:", err)
	return 1
}
