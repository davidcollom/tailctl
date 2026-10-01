package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/davidcollom/tailctl/pkg/api"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type schemaNode = map[string]any

func object(value any) schemaNode                    { node, _ := value.(map[string]any); return node }
func stringValue(node schemaNode, key string) string { value, _ := node[key].(string); return value }
func boolValue(node schemaNode, key string) bool     { value, _ := node[key].(bool); return value }
func sortedKeys(node schemaNode) []string {
	keys := []string{}
	for key := range node {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func pointerPart(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

type operationSchema struct {
	operation  api.Operation
	node       schemaNode
	parameters []schemaNode
	body       schemaNode
}
type schemaCatalog struct {
	document schemaNode
	compiler *jsonschema.Compiler
	mu       sync.Mutex
}

func newSchemaCatalog() (*schemaCatalog, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(api.SchemaDocument()))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(noExternalSchema{})
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource("https://tailctl.invalid/schema", doc); err != nil {
		return nil, err
	}
	return &schemaCatalog{document: object(doc), compiler: compiler}, nil
}

type noExternalSchema struct{}

func (noExternalSchema) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema loading is disabled")
}
func (catalog *schemaCatalog) resolve(node schemaNode) schemaNode {
	for depth := 0; depth < 64; depth++ {
		ref := stringValue(node, "$ref")
		if ref == "" {
			return node
		}
		if !strings.HasPrefix(ref, "#/") {
			return nil
		}
		value := any(catalog.document)
		for _, key := range strings.Split(ref[2:], "/") {
			key = strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")
			value = object(value)[key]
		}
		node = object(value)
	}
	return nil
}
func (catalog *schemaCatalog) operation(op api.Operation) operationSchema {
	path := object(object(catalog.document["paths"])[op.Path])
	node := object(path[strings.ToLower(op.Method)])
	info := operationSchema{operation: op, node: node, body: catalog.resolve(object(node["requestBody"]))}
	for _, source := range []schemaNode{path, node} {
		ps, _ := source["parameters"].([]any)
		for _, p := range ps {
			parameter := catalog.resolve(object(p))
			replaced := false
			for i, existing := range info.parameters {
				if existing["name"] == parameter["name"] && existing["in"] == parameter["in"] {
					info.parameters[i] = parameter
					replaced = true
					break
				}
			}
			if !replaced {
				info.parameters = append(info.parameters, parameter)
			}
		}
	}
	return info
}

// objectShape collects composed object properties for flags and help only.
// Validation always uses the original schema, preserving allOf constraints.
func (catalog *schemaCatalog) objectShape(node schemaNode) schemaNode {
	return catalog.objectShapeAt(node, 0)
}
func (catalog *schemaCatalog) objectShapeAt(node schemaNode, depth int) schemaNode {
	if depth > 64 {
		return nil
	}
	node = catalog.resolve(node)
	shape := schemaNode{}
	for key, value := range node {
		shape[key] = value
	}
	properties := schemaNode{}
	for key, value := range object(node["properties"]) {
		properties[key] = value
	}
	parts, _ := node["allOf"].([]any)
	for _, part := range parts {
		child := catalog.objectShapeAt(object(part), depth+1)
		if child["type"] == "object" {
			shape["type"] = "object"
		}
		for key, value := range object(child["properties"]) {
			if _, exists := properties[key]; !exists {
				properties[key] = value
			}
		}
	}
	if len(properties) > 0 {
		shape["properties"] = properties
	}
	return shape
}
func (catalog *schemaCatalog) validate(info operationSchema, part string, value any) error {
	// Local fragments resolve through the same pinned document; no network access.
	location := "https://tailctl.invalid/schema#/paths/" + pointerPart(info.operation.Path) + "/" + strings.ToLower(info.operation.Method) + "/" + part
	catalog.mu.Lock()
	compiled, err := catalog.compiler.Compile(location)
	catalog.mu.Unlock()
	if err != nil {
		return fmt.Errorf("cannot compile pinned request schema for %s", info.operation.ID)
	}
	if err := compiled.Validate(value); err != nil {
		// Validator messages can echo sensitive input. Return schema identity only.
		return fmt.Errorf("invalid %s request: %s does not match the pinned schema (check required fields, types, enums and bounds)", info.operation.ID, part)
	}
	return nil
}
func (catalog *schemaCatalog) validateNode(node schemaNode, value any) error {
	// Parameter schemas may live behind component references; a temporary document
	// keeps those references valid without sharing/mutating the pinned catalogue.
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(noExternalSchema{})
	doc := map[string]any{"components": catalog.document["components"], "value": node}
	if err := compiler.AddResource("https://tailctl.invalid/parameter", doc); err != nil {
		return err
	}
	schema, err := compiler.Compile("https://tailctl.invalid/parameter#/value")
	if err != nil {
		return fmt.Errorf("cannot compile pinned parameter schema")
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("parameter does not match its schema (check type, enum and bounds)")
	}
	return nil
}
func decodeJSON(data []byte) (any, error) {
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid JSON request body")
	}
	// UnmarshalJSON consumes a single document; reject trailing JSON/text too.
	if !json.Valid(data) {
		return nil, fmt.Errorf("request body must contain one JSON document")
	}
	return value, nil
}
