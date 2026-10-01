package cli

import (
	"fmt"
	"strings"

	"github.com/davidcollom/tailctl/pkg/api"
	"github.com/davidcollom/tailctl/pkg/output"
	"github.com/spf13/cobra"
)

func describeCommand(runtime *Runtime) *cobra.Command {
	return &cobra.Command{Use: "describe OPERATION", Short: "Show an operation's parameters, bodies and referenced schemas (no credentials required)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		op, exists := api.Operations()[args[0]]
		if !exists {
			return fmt.Errorf("unknown operation %q; run tailctl api list", args[0])
		}
		catalog, err := newSchemaCatalog()
		if err != nil {
			return err
		}
		info := catalog.operation(op)
		components := schemaNode{}
		seen := map[string]bool{}
		var visit func(any)
		visit = func(value any) {
			switch value := value.(type) {
			case map[string]any:
				ref := stringValue(value, "$ref")
				if strings.HasPrefix(ref, "#/components/") && !seen[ref] {
					seen[ref] = true
					parts := strings.Split(ref, "/")
					if len(parts) == 4 {
						category, key := parts[2], strings.ReplaceAll(strings.ReplaceAll(parts[3], "~1", "/"), "~0", "~")
						node := object(object(catalog.document["components"])[category])[key]
						if components[category] == nil {
							components[category] = schemaNode{}
						}
						object(components[category])[key] = node
						visit(node)
					}
				}
				for _, item := range value {
					visit(item)
				}
			case []any:
				for _, item := range value {
					visit(item)
				}
			case []schemaNode:
				for _, item := range value {
					visit(item)
				}
			}
		}
		value := schemaNode{"operation": op.ID, "method": op.Method, "path": op.Path, "summary": op.Summary, "parameters": info.parameters, "requestBody": info.body, "responses": info.node["responses"]}
		for _, binding := range ResourceBindings() {
			if binding.Operation == op.ID {
				value["command"] = "tailctl " + binding.Command
			}
		}
		visit(value)
		value["components"] = components
		format := runtime.Config.Output
		if format == "table" || format == "wide" || format == "" {
			format = "yaml"
		}
		return output.Write(cmd.OutOrStdout(), value, output.Options{Format: format})
	}}
}
