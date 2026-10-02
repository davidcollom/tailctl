package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/davidcollom/tailctl/pkg/api"
	"github.com/spf13/cobra"
)

const maxRequestBytes = 16 << 20

var camelWord = regexp.MustCompile(`([a-z0-9])([A-Z])`)
var acronymWord = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)

func flagName(name string) string {
	if name == "redirectURIs" {
		return "redirect-uris"
	}
	return strings.ToLower(camelWord.ReplaceAllString(acronymWord.ReplaceAllString(name, "${1}-${2}"), "${1}-${2}"))
}
func shortDescription(node schemaNode, fallback string) string {
	text := strings.TrimSpace(stringValue(node, "description"))
	if text == "" {
		text = fallback
	}
	text = strings.Split(text, "\n")[0]
	if choices, ok := node["enum"].([]any); ok && len(choices) <= 10 {
		text += fmt.Sprintf(" (choices: %v)", choices)
	}
	return text
}

type fieldBinding struct {
	key, flag, jsonFlag, kind string
	node, parameter           schemaNode
	nullable                  bool
}

func fieldType(node schemaNode) (string, bool) {
	switch value := node["type"].(type) {
	case string:
		return value, false
	case []any:
		kind := ""
		nullable := false
		for _, item := range value {
			candidate, ok := item.(string)
			if !ok {
				return "", nullable
			}
			if candidate == "null" {
				nullable = true
				continue
			}
			if kind != "" {
				return "", nullable
			}
			kind = candidate
		}
		return kind, nullable
	default:
		return "", false
	}
}

func fieldValue(cmd *cobra.Command, binding fieldBinding) (any, error) {
	if binding.jsonFlag != "" && cmd.Flags().Changed(binding.jsonFlag) {
		if binding.flag != binding.jsonFlag && cmd.Flags().Changed(binding.flag) {
			return nil, fmt.Errorf("use either --%s or --%s", binding.flag, binding.jsonFlag)
		}
		raw, _ := cmd.Flags().GetString(binding.jsonFlag)
		return decodeJSON([]byte(raw))
	}
	switch binding.kind {
	case "boolean":
		return cmd.Flags().GetBool(binding.flag)
	case "integer":
		value, _ := cmd.Flags().GetString(binding.flag)
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return nil, fmt.Errorf("--%s must be an integer", binding.flag)
		}
		return json.Number(value), nil
	case "number":
		value, _ := cmd.Flags().GetString(binding.flag)
		parsed, err := decodeJSON([]byte(value))
		if err != nil {
			return nil, fmt.Errorf("--%s must be a number", binding.flag)
		}
		if _, ok := parsed.(json.Number); !ok {
			return nil, fmt.Errorf("--%s must be a number", binding.flag)
		}
		return parsed, nil
	case "string-array":
		return cmd.Flags().GetStringArray(binding.flag)
	case "string-map":
		values, _ := cmd.Flags().GetStringArray(binding.flag)
		result := map[string]string{}
		for _, value := range values {
			key, item, ok := strings.Cut(value, "=")
			if !ok || key == "" {
				return nil, fmt.Errorf("--%s expects KEY=VALUE", binding.flag)
			}
			if _, exists := result[key]; exists {
				return nil, fmt.Errorf("--%s contains duplicate key %q", binding.flag, key)
			}
			result[key] = item
		}
		return result, nil
	default:
		return cmd.Flags().GetString(binding.flag)
	}
}
func (binding fieldBinding) changed(cmd *cobra.Command) bool {
	return cmd.Flags().Changed(binding.flag) || (binding.jsonFlag != "" && cmd.Flags().Changed(binding.jsonFlag))
}
func addFieldFlag(cmd *cobra.Command, key, name string, node schemaNode, allowJSON bool) fieldBinding {
	kind, nullable := fieldType(node)
	binding := fieldBinding{key: key, flag: name, node: node, kind: kind, nullable: nullable}
	usage := shortDescription(node, key)
	if binding.kind == "array" && stringValue(object(node["items"]), "type") == "string" {
		binding.kind = "string-array"
	}
	if binding.kind == "object" && stringValue(object(node["additionalProperties"]), "type") == "string" {
		binding.kind = "string-map"
	}
	switch binding.kind {
	case "boolean":
		cmd.Flags().Bool(name, false, usage)
	case "string-array":
		cmd.Flags().StringArray(name, nil, usage+" (repeat for each value)")
	case "string-map":
		cmd.Flags().StringArray(name, nil, usage+" (KEY=VALUE; repeat for each entry)")
	case "string", "integer", "number":
		cmd.Flags().String(name, "", usage)
	default:
		binding.kind = "json"
		binding.jsonFlag = name + "-json"
		binding.flag = binding.jsonFlag
		cmd.Flags().String(binding.flag, "", usage+" (JSON value)")
		return binding
	}
	if allowJSON && binding.kind == "string-array" {
		binding.jsonFlag = name + "-json"
		cmd.Flags().String(binding.jsonFlag, "", "JSON array for "+key+"; use [] to clear it")
	}
	if allowJSON && binding.kind == "string-map" {
		binding.jsonFlag = name + "-json"
		cmd.Flags().String(binding.jsonFlag, "", "JSON object for "+key+"; use {} to clear it")
	}
	if allowJSON && binding.nullable && binding.kind != "" && binding.jsonFlag == "" {
		binding.jsonFlag = name + "-json"
		cmd.Flags().String(binding.jsonFlag, "", "JSON "+binding.kind+" or null; use null to clear it")
	}
	return binding
}
func safeBodyFlag(key string, cmd *cobra.Command) string {
	name := flagName(key)
	reserved := map[string]bool{"file": true, "yes": true, "query": true, "filter": true, "raw": true, "response-file": true, "content-type": true, "config": true, "tailnet": true, "server": true, "output": true, "timeout": true, "sort-by": true, "no-headers": true, "help": true}
	if reserved[name] || cmd.Flags().Lookup(name) != nil {
		name = "body-" + name
	}
	return name
}
func sensitiveField(key string, node schemaNode) bool {
	lower := strings.ToLower(key)
	return stringValue(node, "format") == "password" || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || lower == "token" || lower == "key" || lower == "gcscredentials"
}
func addResourceCommands(root *cobra.Command, runtime *Runtime) error {
	catalog, err := newSchemaCatalog()
	if err != nil {
		return err
	}
	operations := api.Operations()
	seen := map[string]bool{}
	for _, binding := range ResourceBindings() {
		op, ok := operations[binding.Operation]
		if !ok {
			return fmt.Errorf("command maps unknown operation %s", binding.Operation)
		}
		if seen[op.ID] {
			return fmt.Errorf("duplicate operation command %s", op.ID)
		}
		seen[op.ID] = true
		if err := addResourceBinding(root, runtime, catalog, binding, false); err != nil {
			return err
		}
	}
	for id := range operations {
		if !seen[id] {
			return fmt.Errorf("schema operation %s needs a resource command mapping", id)
		}
	}
	for _, binding := range DeprecatedResourceBindings() {
		if err := addResourceBinding(root, runtime, catalog, binding, true); err != nil {
			return err
		}
	}
	return nil
}

func addResourceBinding(root *cobra.Command, runtime *Runtime, catalog *schemaCatalog, binding ResourceBinding, deprecated bool) error {
	op, ok := api.Operations()[binding.Operation]
	if !ok {
		return fmt.Errorf("command maps unknown operation %s", binding.Operation)
	}
	words := strings.Fields(binding.Command)
	parent := root
	for index, word := range words[:len(words)-1] {
		var found *cobra.Command
		for _, child := range parent.Commands() {
			if child.Name() == word {
				found = child
				break
			}
		}
		if found == nil {
			path := strings.Join(words[:index+1], " ")
			found = &cobra.Command{Use: word, Short: resourceGroupDescription(path), Hidden: deprecated}
			if word == "organisations" {
				found.Aliases = []string{"organizations"}
			}
			parent.AddCommand(found)
		}
		parent = found
	}
	cmd, err := resourceCommand(runtime, catalog, catalog.operation(op), words[len(words)-1])
	if err != nil {
		return err
	}
	for _, child := range parent.Commands() {
		if child.Name() == cmd.Name() {
			return fmt.Errorf("duplicate resource command %s", binding.Command)
		}
	}
	if deprecated {
		canonical := ""
		for _, current := range ResourceBindings() {
			if current.Operation == binding.Operation {
				canonical = current.Command
				break
			}
		}
		cmd.Deprecated = "use 'tailctl " + canonical + "' instead"
		cmd.Hidden = true
	}
	parent.AddCommand(cmd)
	return nil
}

func resourceGroupDescription(path string) string {
	switch path {
	case "invites":
		return "Manage invitations"
	case "invites users":
		return "Manage user invitations"
	case "invites devices":
		return "Manage device invitations"
	default:
		words := strings.Fields(path)
		return "Manage " + strings.ReplaceAll(words[len(words)-1], "-", " ")
	}
}
func resourceCommand(runtime *Runtime, catalog *schemaCatalog, info operationSchema, name string) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: name, Short: info.operation.Summary, Annotations: map[string]string{"operation": info.operation.ID}}
	if name == "authorise" {
		cmd.Aliases = []string{"authorize"}
	}
	cmd.Long = info.operation.Summary + "\n\n" + stringValue(info.node, "description") + "\n\nOperation: " + info.operation.ID + " (" + info.operation.Method + " " + info.operation.Path + "). JSON/YAML preserve the full response."
	var pathParameters, queryBindings, headerBindings, bodyBindings []fieldBinding
	dynamicFilters := false
	for _, parameter := range info.parameters {
		key := stringValue(parameter, "name")
		node := catalog.resolve(object(parameter["schema"]))
		binding := fieldBinding{key: key, node: node, parameter: parameter}
		switch stringValue(parameter, "in") {
		case "path":
			if key != "tailnet" {
				pathParameters = append(pathParameters, binding)
				cmd.Use += " " + strings.ToUpper(flagName(key))
			}
		case "query":
			if strings.ContainsAny(key, "<>= ") {
				dynamicFilters = true
				continue
			}
			binding = addFieldFlag(cmd, key, flagName(key), node, false)
			binding.parameter = parameter
			if info.operation.ID == "listTailnetKeys" && key == "all" {
				cmd.Flags().Lookup(binding.flag).DefValue = "true"
				if err := cmd.Flags().Lookup(binding.flag).Value.Set("true"); err != nil {
					return nil, err
				}
			}
			if description := stringValue(parameter, "description"); description != "" {
				usage := schemaNode{}
				for k, v := range node {
					usage[k] = v
				}
				usage["description"] = description
				cmd.Flags().Lookup(binding.flag).Usage = shortDescription(usage, key)
			}
			if boolValue(parameter, "required") {
				cmd.Flags().Lookup(binding.flag).Usage += " (required)"
			}
			queryBindings = append(queryBindings, binding)
		case "header":
			binding = addFieldFlag(cmd, key, flagName(key), node, false)
			binding.parameter = parameter
			headerBindings = append(headerBindings, binding)
		}
	}
	cmd.Args = cobra.ExactArgs(len(pathParameters))
	cmd.Flags().StringArray("query", nil, "Additional schema query KEY=VALUE (repeatable; required query flags can also be supplied here)")
	if dynamicFilters {
		cmd.Flags().StringArray("filter", nil, "Device filter FIELD=VALUE (repeatable)")
	}
	cmd.Flags().Bool("raw", false, "Write the exact response bytes, including HuJSON")
	cmd.Flags().String("response-file", "", "Save the exact response to a new file with mode 0600 (no overwrite)")
	if info.operation.Method != http.MethodGet && info.operation.Method != http.MethodHead {
		cmd.Flags().Bool("yes", false, "Explicitly allow this API action")
	}
	contents := object(info.body["content"])
	mediaTypes := sortedKeys(contents)
	defaultMedia := "application/json"
	singleItemArrayBody := false
	splitDNSBody := info.operation.ID == "setSplitDns" || info.operation.ID == "updateSplitDns"
	if len(mediaTypes) > 0 {
		if contents[defaultMedia] == nil {
			defaultMedia = mediaTypes[0]
		}
		cmd.Flags().StringP("file", "f", "", "Request body file, or '-' for stdin; JSON is validated against the pinned schema")
		cmd.Flags().String("content-type", defaultMedia, "Request media type: "+strings.Join(mediaTypes, ", "))
		bodySchema := catalog.resolve(object(object(contents["application/json"])["schema"]))
		if stringValue(bodySchema, "type") == "array" {
			itemSchema := catalog.objectShape(object(bodySchema["items"]))
			if stringValue(itemSchema, "type") == "object" && len(object(itemSchema["properties"])) > 0 {
				bodySchema = itemSchema
				singleItemArrayBody = true
			}
		} else {
			bodySchema = catalog.objectShape(bodySchema)
		}
		for _, key := range sortedKeys(object(bodySchema["properties"])) {
			node := catalog.resolve(object(object(bodySchema["properties"])[key]))
			if boolValue(node, "readOnly") || sensitiveField(key, node) {
				continue
			}
			binding := addFieldFlag(cmd, key, safeBodyFlag(key, cmd), node, true)
			bodyBindings = append(bodyBindings, binding)
		}
		if splitDNSBody {
			cmd.Flags().StringArray("route", nil, "Split DNS route DOMAIN=NAMESERVER (repeat for additional nameservers or domains)")
			cmd.Flags().StringArray("clear-domain", nil, "Set a domain's nameservers to null (repeatable)")
			if info.operation.ID == "setSplitDns" {
				cmd.Flags().Bool("clear-all", false, "Replace split DNS with an empty map")
			}
		}
		if singleItemArrayBody {
			cmd.Long += "\nBody flags create one item. Use --file for multiple items or the complete JSON body. Body flags and --file are mutually exclusive."
		} else {
			cmd.Long += "\nUse --file for complete bodies, policy documents, nested batches and secret fields. Body flags and --file are mutually exclusive."
		}
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Lookup("yes") != nil {
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				return fmt.Errorf("%s %s requires --yes", info.operation.Method, info.operation.Path)
			}
		}
		params := map[string]string{}
		for i, binding := range pathParameters {
			if args[i] == "" {
				return fmt.Errorf("%s must not be empty", binding.key)
			}
			if err := catalog.validateNode(binding.node, args[i]); err != nil {
				return fmt.Errorf("invalid %s: %w", binding.key, err)
			}
			params[binding.key] = args[i]
		}
		rawQuery, _ := cmd.Flags().GetStringArray("query")
		query, err := queryPairs(rawQuery)
		if err != nil {
			return err
		}
		allowed := map[string]bool{}
		for _, binding := range queryBindings {
			allowed[binding.key] = true
			if binding.changed(cmd) {
				if _, duplicate := query[binding.key]; duplicate {
					return fmt.Errorf("query %s is supplied by both a flag and --query", binding.key)
				}
				value, err := fieldValue(cmd, binding)
				if err != nil {
					return err
				}
				if values, ok := value.([]string); ok {
					query[binding.key] = values
				} else {
					query.Set(binding.key, fmt.Sprint(value))
				}
			}
			values, present := query[binding.key]
			if !present && info.operation.ID == "listTailnetKeys" && binding.key == "all" {
				query.Set(binding.key, "true")
				values, present = query[binding.key], true
			}
			if !present && boolValue(binding.parameter, "required") {
				return fmt.Errorf("required query: --%s", binding.flag)
			}
			if present {
				var value any
				if binding.kind == "string-array" {
					items := []any{}
					for _, v := range values {
						items = append(items, v)
					}
					value = items
				} else {
					if len(values) != 1 {
						return fmt.Errorf("query %s takes one value", binding.key)
					}
					value = values[0]
					if binding.kind == "boolean" {
						v, err := strconv.ParseBool(values[0])
						if err != nil {
							return fmt.Errorf("query %s must be boolean", binding.key)
						}
						value = v
					}
					if binding.kind == "integer" {
						v, err := strconv.ParseInt(values[0], 10, 64)
						if err != nil {
							return fmt.Errorf("query %s must be integer", binding.key)
						}
						value = json.Number(strconv.FormatInt(v, 10))
					}
				}
				if err := catalog.validateNode(binding.node, value); err != nil {
					return fmt.Errorf("invalid query %s: %w", binding.key, err)
				}
				if binding.kind == "string-array" && binding.parameter["explode"] == false {
					query.Set(binding.key, strings.Join(values, ","))
				}
			}
		}
		for key := range query {
			if !allowed[key] {
				return fmt.Errorf("unknown query parameter %q", key)
			}
		}
		if dynamicFilters {
			filters, _ := cmd.Flags().GetStringArray("filter")
			filtered, err := queryPairs(filters)
			if err != nil {
				return err
			}
			for key, values := range filtered {
				if !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9.]*$`).MatchString(key) || allowed[key] {
					return fmt.Errorf("invalid device filter field")
				}
				query[key] = values
			}
		}
		headers := http.Header{}
		for _, binding := range headerBindings {
			if binding.changed(cmd) {
				value, err := fieldValue(cmd, binding)
				if err != nil {
					return err
				}
				headers.Set(binding.key, fmt.Sprint(value))
			}
		}
		var body io.Reader
		contentType := ""
		if len(mediaTypes) > 0 {
			contentType, _ = cmd.Flags().GetString("content-type")
			if contents[contentType] == nil {
				return fmt.Errorf("unsupported request media type; choose %s", strings.Join(mediaTypes, ", "))
			}
			file, _ := cmd.Flags().GetString("file")
			fields := map[string]any{}
			for _, binding := range bodyBindings {
				if binding.changed(cmd) {
					value, err := fieldValue(cmd, binding)
					if err != nil {
						return fmt.Errorf("invalid --%s: %w", binding.flag, err)
					}
					fields[binding.key] = value
				}
			}
			if file != "" && len(fields) > 0 {
				return errors.New("--file cannot be combined with request body flags")
			}
			hasSplitDNSFlags := false
			if splitDNSBody {
				routes, _ := cmd.Flags().GetStringArray("route")
				cleared, _ := cmd.Flags().GetStringArray("clear-domain")
				hasSplitDNSFlags = len(routes) > 0 || len(cleared) > 0
				if info.operation.ID == "setSplitDns" {
					clearAll, _ := cmd.Flags().GetBool("clear-all")
					hasSplitDNSFlags = hasSplitDNSFlags || clearAll
				}
				if file != "" && hasSplitDNSFlags {
					return errors.New("--file cannot be combined with split DNS flags")
				}
			}
			var data []byte
			if file != "" {
				var reader io.Reader = cmd.InOrStdin()
				if file != "-" {
					f, err := os.Open(file)
					if err != nil {
						return err
					}
					defer f.Close()
					reader = f
				}
				data, err = io.ReadAll(io.LimitReader(reader, maxRequestBytes+1))
				if err != nil {
					return err
				}
			} else if splitDNSBody {
				data, err = splitDNSRequestBody(cmd, info.operation.ID)
				if err != nil {
					return err
				}
			} else if singleItemArrayBody {
				data, err = json.Marshal([]any{fields})
				if err != nil {
					return err
				}
			} else {
				schema := catalog.objectShape(object(object(contents[contentType])["schema"]))
				if stringValue(schema, "type") != "object" || (len(object(schema["properties"])) == 0 && len(fields) == 0) {
					return errors.New("this operation needs a request body: use --file PATH or --file -")
				}
				data, err = json.Marshal(fields)
				if err != nil {
					return err
				}
			}
			if len(data) > maxRequestBytes {
				return errors.New("request body exceeds 16 MiB")
			}
			if contentType == "application/json" {
				value, err := decodeJSON(data)
				if err != nil {
					return err
				}
				if err := catalog.validate(info, "requestBody/content/"+pointerPart(contentType)+"/schema", value); err != nil {
					return err
				}
			} else if file == "" {
				return errors.New("non-JSON request bodies require --file")
			}
			body = bytes.NewReader(data)
		}
		raw, _ := cmd.Flags().GetBool("raw")
		destination, _ := cmd.Flags().GetString("response-file")
		if raw && destination != "" {
			return errors.New("use either --raw or --response-file")
		}
		// Validate output before performing an irreversible API action.
		if !raw && destination == "" && !validFormat(runtime.Config.Output) {
			return fmt.Errorf("unsupported output %q", runtime.Config.Output)
		}
		var responseFile *os.File
		saved := false
		if destination != "" {
			responseFile, err = os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			defer func() {
				responseFile.Close()
				if !saved {
					os.Remove(destination)
				}
			}()
		}
		c, err := runtime.Client()
		if err != nil {
			return err
		}
		response, err := c.CallRaw(cmd.Context(), info.operation.ID, params, query, body, contentType, headers)
		if err != nil {
			return err
		}
		if responseFile != nil {
			if _, err := responseFile.Write(response.Data); err != nil {
				return err
			}
			if err := responseFile.Close(); err != nil {
				return err
			}
			saved = true
			_, err = fmt.Fprintf(cmd.ErrOrStderr(), "Response saved to %s\n", destination)
			return err
		}
		if raw {
			_, err := cmd.OutOrStdout().Write(response.Data)
			return err
		}
		if len(response.Data) == 0 {
			return runtime.Print(cmd, map[string]any{"operation": info.operation.ID, "status": http.StatusText(response.StatusCode)}, nil)
		}
		var value any
		if err := json.Unmarshal(response.Data, &value); err != nil {
			return errors.New("response is not JSON; use --raw or --response-file for HuJSON")
		}
		return printResource(runtime, cmd, info.operation, value)
	}
	return cmd, nil
}

func splitDNSRequestBody(cmd *cobra.Command, operation string) ([]byte, error) {
	routes, _ := cmd.Flags().GetStringArray("route")
	cleared, _ := cmd.Flags().GetStringArray("clear-domain")
	clearAll := false
	if operation == "setSplitDns" {
		clearAll, _ = cmd.Flags().GetBool("clear-all")
	}
	if clearAll && (len(routes) > 0 || len(cleared) > 0) {
		return nil, errors.New("--clear-all cannot be combined with --route or --clear-domain")
	}
	if clearAll {
		return []byte("{}"), nil
	}

	body := map[string]any{}
	for _, route := range routes {
		domain, nameserver, ok := strings.Cut(route, "=")
		if !ok || domain == "" || nameserver == "" {
			return nil, errors.New("--route expects DOMAIN=NAMESERVER")
		}
		if body[domain] == nil {
			body[domain] = []string{}
		}
		body[domain] = append(body[domain].([]string), nameserver)
	}
	for _, domain := range cleared {
		if domain == "" {
			return nil, errors.New("--clear-domain must not be empty")
		}
		if _, exists := body[domain]; exists {
			return nil, fmt.Errorf("domain %q cannot be both routed and cleared", domain)
		}
		body[domain] = nil
	}
	if len(body) == 0 {
		return nil, errors.New("split DNS requires --route, --clear-domain or --file")
	}
	return json.Marshal(body)
}
func validFormat(format string) bool {
	return format == "table" || format == "wide" || format == "json" || format == "yaml" || format == ""
}
