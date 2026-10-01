// Command generate creates the operation catalogue from the pinned upstream schema.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	data, err := os.ReadFile("../../api/openapi.upstream.yaml")
	if err != nil {
		return err
	}
	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return err
	}
	entries := map[string]string{}
	for path, methods := range spec.Paths {
		for method, node := range methods {
			switch method {
			case "get", "post", "put", "patch", "delete", "head", "options":
			default:
				continue
			}
			var op struct {
				OperationID string `yaml:"operationId"`
				Summary     string `yaml:"summary"`
			}
			if err := node.Decode(&op); err != nil {
				return err
			}
			if op.OperationID == "" {
				continue
			}
			if _, ok := entries[op.OperationID]; ok {
				return fmt.Errorf("duplicate operation: %s", op.OperationID)
			}
			entries[op.OperationID] = fmt.Sprintf("%q: {ID: %q, Method: %q, Path: %q, Summary: %q},\n", op.OperationID, op.OperationID, strings.ToUpper(method), path, op.Summary)
		}
	}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	b.WriteString("// Code generated from api/openapi.upstream.yaml; DO NOT EDIT.\npackage api\n\n// Operation describes an upstream API endpoint.\ntype Operation struct { ID, Method, Path, Summary string }\n\nvar operations = map[string]Operation{\n")
	for _, k := range keys {
		b.WriteString(entries[k])
	}
	b.WriteString("}\n\n// Operations returns a copy so callers cannot mutate the catalogue.\nfunc Operations() map[string]Operation { out := make(map[string]Operation, len(operations)); for k, v := range operations { out[k] = v }; return out }\n")
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return err
	}
	if err := os.WriteFile("operations.gen.go", formatted, 0644); err != nil {
		return err
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	schema, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile("schema.gen.json", append(schema, '\n'), 0644)
}
