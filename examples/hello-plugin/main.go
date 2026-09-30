// Build with: go build -o tailctl-hello ./examples/hello-plugin
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Printf("Hello, tailnet %s (plugin protocol %s)\n", os.Getenv("TAILCTL_TAILNET"), os.Getenv("TAILCTL_PLUGIN_PROTOCOL"))
	fmt.Printf("Arguments: %q\n", os.Args[1:])
}
