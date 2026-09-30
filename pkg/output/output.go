// Package output renders consistent, script-friendly CLI output.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go.yaml.in/yaml/v3"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode"
)

type Column struct{ Header, Field string }
type Options struct {
	Format    string
	NoHeaders bool
	SortBy    string
	Columns   []Column
}

// Write uses explicit table columns. JSON/YAML preserve the complete response.
func Write(w io.Writer, value any, o Options) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	switch o.Format {
	case "json":
		var out bytes.Buffer
		if err := json.Indent(&out, data, "", "  "); err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, out.String())
		return err
	case "yaml":
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		if err := enc.Encode(decoded); err != nil {
			return err
		}
		return enc.Close()
	case "table", "wide", "":
	default:
		return fmt.Errorf("unsupported output %q", o.Format)
	}
	rows := []map[string]any{}
	switch v := decoded.(type) {
	case []any:
		for _, item := range v {
			if row, ok := item.(map[string]any); ok {
				rows = append(rows, row)
			} else {
				rows = append(rows, map[string]any{"value": item})
			}
		}
	case map[string]any:
		rows = append(rows, v)
	default:
		rows = append(rows, map[string]any{"value": v})
	}
	cols := o.Columns
	if len(cols) == 0 {
		fields := map[string]bool{}
		for _, row := range rows {
			for k := range row {
				fields[k] = true
			}
		}
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			cols = append(cols, Column{Header: strings.ToUpper(key), Field: key})
		}
	}
	if o.SortBy != "" {
		sort.SliceStable(rows, func(i, j int) bool { return cell(rows[i][o.SortBy]) < cell(rows[j][o.SortBy]) })
	}
	t := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if !o.NoHeaders {
		headers := []string{}
		for _, col := range cols {
			headers = append(headers, clean(col.Header))
		}
		if _, err := fmt.Fprintln(t, strings.Join(headers, "\t")); err != nil {
			return err
		}
	}
	for _, row := range rows {
		cells := []string{}
		for _, col := range cols {
			cells = append(cells, cell(row[col.Field]))
		}
		if _, err := fmt.Fprintln(t, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return t.Flush()
}
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
func cell(v any) string {
	if v == nil {
		return "<none>"
	}
	switch x := v.(type) {
	case string:
		if x == "" {
			return "<none>"
		}
		return clean(x)
	case []any:
		s := []string{}
		for _, item := range x {
			s = append(s, cell(item))
		}
		if len(s) == 0 {
			return "<none>"
		}
		return strings.Join(s, ",")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "<invalid>"
	}
	return clean(string(b))
}
