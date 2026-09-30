// Demonstrates composing a new binary with an in-process Go extension.
package main

import (
	"context"
	"fmt"
	"github.com/davidcollom/tailctl/pkg/cli"
	"github.com/davidcollom/tailctl/pkg/output"
	"github.com/spf13/cobra"
	"os"
)

type inventory struct{}

func (inventory) Command(r *cli.Runtime) (*cobra.Command, error) {
	return &cobra.Command{Use: "inventory", Short: "Example extension using the shared client", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		c, err := r.Client()
		if err != nil {
			return err
		}
		devices, err := c.Devices(cmd.Context())
		if err != nil {
			return err
		}
		return r.Print(cmd, devices, []output.Column{{Header: "HOSTNAME", Field: "hostname"}, {Header: "OS", Field: "os"}})
	}}, nil
}
func main() {
	if err := cli.Execute(context.Background(), os.Args[1:], cli.Options{Version: "example", Extensions: []cli.Extension{inventory{}}}, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
