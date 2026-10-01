package cli

import (
	"strings"

	"github.com/davidcollom/tailctl/pkg/api"
	"github.com/davidcollom/tailctl/pkg/output"
	"github.com/spf13/cobra"
)

func columns(fields ...string) []output.Column {
	result := []output.Column{}
	for _, field := range fields {
		result = append(result, output.Column{Header: strings.ToUpper(flagName(field)), Field: field})
	}
	return result
}
func printResource(runtime *Runtime, cmd *cobra.Command, op api.Operation, value any) error {
	if runtime.Config.Output == "json" || runtime.Config.Output == "yaml" {
		return runtime.Print(cmd, value, nil)
	}
	// Tables suppress credential material; full structured/raw output is explicit.
	value = redactTable(value)
	family := strings.Fields(cmd.CommandPath())[1]
	cols := []output.Column(nil)
	switch family {
	case "devices":
		if op.ID == "listDeviceInvites" || op.ID == "createDeviceInvites" {
			cols = columns("id", "deviceId", "email", "multiUse", "accepted")
		}
		if op.ID == "listTailnetDevices" || op.ID == "getDevice" {
			cols = columns("nodeId", "hostname", "os", "addresses", "authorized")
			if runtime.Config.Output == "wide" {
				cols = append(cols, columns("user", "lastSeen", "clientVersion", "tags")...)
			}
		}
	case "users":
		cols = columns("id", "loginName", "displayName", "role", "status")
		if runtime.Config.Output == "wide" {
			cols = append(cols, columns("deviceCount", "lastSeen", "currentlyConnected")...)
		}
	case "user-invites":
		cols = columns("id", "email", "role", "lastEmailSentAt")
	case "device-invites":
		cols = columns("id", "deviceId", "email", "multiUse", "accepted")
	case "keys":
		cols = columns("id", "keyType", "description", "created", "expires", "revoked")
		if runtime.Config.Output == "wide" {
			cols = append(cols, columns("scopes", "tags")...)
		}
	case "webhooks":
		cols = columns("endpointId", "endpointUrl", "subscriptions")
		if runtime.Config.Output == "wide" {
			cols = append(cols, columns("providerType", "creatorLoginName", "lastModified")...)
		}
	case "services":
		if op.ID == "listServices" || op.ID == "getService" || op.ID == "updateService" {
			cols = columns("name", "displayName", "ports", "comment")
			if runtime.Config.Output == "wide" {
				cols = append(cols, columns("addrs", "tags")...)
			}
		}
	case "oauth-apps":
		cols = columns("id", "name", "description", "scopes")
		if runtime.Config.Output == "wide" {
			cols = append(cols, columns("redirectURIs", "created")...)
		}
	case "posture":
		cols = columns("id", "provider", "status", "configUpdated")
	case "organisations":
		cols = columns("id", "displayName", "createdAt")
		if runtime.Config.Output == "wide" {
			cols = append(cols, columns("orgId")...)
		}
	case "logs":
		if op.ID == "listConfigurationAuditLogs" {
			cols = columns("eventTime", "action", "actor", "target", "error")
			if runtime.Config.Output == "wide" {
				cols = append(cols, columns("type", "origin", "eventGroupID")...)
			}
		}
		if op.ID == "listNetworkFlowLogs" {
			cols = columns("nodeId", "start", "end", "logged")
			if runtime.Config.Output == "wide" {
				cols = append(cols, columns("virtualTraffic", "subnetTraffic", "exitTraffic", "physicalTraffic")...)
			}
		}
	}
	if obj, ok := value.(map[string]any); ok {
		for _, envelope := range []string{"devices", "users", "keys", "vipServices", "webhooks", "integrations", "oauthApps", "tailnets", "hosts", "logs"} {
			if nested, ok := obj[envelope].([]any); ok {
				value = nested
				break
			}
		}
		if op.ID == "getContacts" {
			rows := []map[string]any{}
			for _, key := range sortedKeys(obj) {
				row := map[string]any{"type": key}
				for name, item := range object(obj[key]) {
					row[name] = item
				}
				rows = append(rows, row)
			}
			value = rows
			cols = columns("type", "email", "needsVerification")
			if runtime.Config.Output == "wide" {
				cols = append(cols, columns("fallbackEmail")...)
			}
		}
		if op.ID == "getSplitDns" || op.ID == "updateSplitDns" || op.ID == "setSplitDns" {
			value = mapRows(obj, "domain", "nameservers")
			cols = columns("domain", "nameservers")
		}
		if op.ID == "getDevicePostureAttributes" {
			if attrs, ok := obj["attributes"].(map[string]any); ok {
				rows := mapRows(attrs, "attribute", "value")
				for _, row := range rows {
					row["expiry"] = object(obj["expiries"])[row["attribute"].(string)]
				}
				value = rows
				cols = columns("attribute", "value")
				if runtime.Config.Output == "wide" {
					cols = append(cols, columns("expiry")...)
				}
			}
		}
	}
	return runtime.Print(cmd, value, cols)
}
func mapRows(obj map[string]any, name, value string) []map[string]any {
	rows := []map[string]any{}
	for _, key := range sortedKeys(obj) {
		rows = append(rows, map[string]any{name: key, value: obj[key]})
	}
	return rows
}
func redactTable(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := map[string]any{}
		for key, item := range value {
			lower := strings.ToLower(key)
			if sensitiveField(key, nil) || strings.Contains(lower, "credential") || lower == "inviteurl" {
				result[key] = "<redacted>"
			} else {
				result[key] = redactTable(item)
			}
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = redactTable(item)
		}
		return result
	default:
		return value
	}
}
