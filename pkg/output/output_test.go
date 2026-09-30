package output

import (
	"bytes"
	"encoding/json"
	"go.yaml.in/yaml/v3"
	"strings"
	"testing"
)

func TestTableSortAndSanitise(t *testing.T) {
	var b bytes.Buffer
	rows := []map[string]any{{"name": "z\n\x1bworker", "addresses": []string{"100.64.0.1", "::1"}}, {"name": "alpha"}}
	err := Write(&b, rows, Options{Format: "table", SortBy: "name", NoHeaders: true, Columns: []Column{{Header: "NAME", Field: "name"}, {Header: "IPS", Field: "addresses"}}})
	if err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.HasPrefix(got, "alpha") || !strings.Contains(got, "100.64.0.1,::1") || strings.Contains(got, "\x1b") {
		t.Fatalf("table: %q", got)
	}
}
func TestStructuredOutputPreservesFields(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			var b bytes.Buffer
			err := Write(&b, map[string]any{"id": "node-1", "authorised": false}, Options{Format: format, Columns: []Column{{Header: "ID", Field: "id"}}})
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if format == "json" {
				err = json.Unmarshal(b.Bytes(), &got)
			} else {
				err = yaml.Unmarshal(b.Bytes(), &got)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got["authorised"] != false {
				t.Fatalf("output: %v", got)
			}
		})
	}
}
