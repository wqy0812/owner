package mcpserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

type Schema = map[string]any

func Object(properties map[string]any, required ...string) Schema {
	s := Schema{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
func String(description string) Schema { return Schema{"type": "string", "description": description} }

// BodySchema describes the actual decoder type. Go zero values are not proof
// of requiredness: REST deliberately distinguishes omitted, null and [] fields.
// Operation metadata adds the required inputs; service validation remains final.
func BodySchema(t reflect.Type) Schema { return typeSchema(t, map[reflect.Type]bool{}) }
func typeSchema(t reflect.Type, stack map[reflect.Type]bool) Schema {
	if t == reflect.TypeFor[json.RawMessage]() {
		return Schema{}
	}
	if t == reflect.TypeFor[time.Time]() {
		return Schema{"type": "string", "format": "date-time"}
	}
	if t.Kind() == reflect.Pointer {
		return Schema{"anyOf": []any{typeSchema(t.Elem(), stack), Schema{"type": "null"}}}
	}
	if stack[t] {
		panic("recursive MCP request type: " + t.String())
	}
	stack[t] = true
	defer delete(stack, t)
	var s Schema
	switch t.Kind() {
	case reflect.Interface:
		s = Schema{}
	case reflect.String:
		s = Schema{"type": "string"}
	case reflect.Bool:
		s = Schema{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s = Schema{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		s = Schema{"type": "number"}
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			s = Schema{"type": []string{"string", "null"}, "contentEncoding": "base64"}
		} else {
			s = Schema{"type": []string{"array", "null"}, "items": typeSchema(t.Elem(), stack)}
		}
	case reflect.Map:
		s = Schema{"type": []string{"object", "null"}, "additionalProperties": typeSchema(t.Elem(), stack)}
	case reflect.Struct:
		props := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if f.PkgPath != "" || tag == "-" {
				continue
			}
			ft := f.Type
			if f.Anonymous && tag == "" {
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				embedded := typeSchema(ft, stack)
				for k, v := range embedded["properties"].(map[string]any) {
					props[k] = v
				}
				continue
			}
			if tag == "" {
				tag = f.Name
			}
			field := typeSchema(ft, stack)
			if d := f.Tag.Get("jsonschema"); d != "" {
				field["description"] = d
			}
			props[tag] = field
		}
		s = Object(props)
	default:
		panic("unsupported MCP request type: " + t.String())
	}
	// Domain enum values are shared by every operation that accepts this type.
	if t.PkgPath() == "codex/platform-demo/internal/domain" {
		if values := map[string][]string{
			"ComponentLayer":        {"host_foundation", "runtime_state", "orchestration_core", "cluster_service", "observability_management", "platform_extension"},
			"ParameterType":         {"string", "boolean", "integer", "number", "object", "array"},
			"ParameterVisibility":   {"internal", "public"},
			"ScenarioExecutionMode": {"install", "upgrade", "baseline_verify"},
			"ActionKind":            {"check", "inspect", "preflight", "install", "configure", "verify", "upgrade", "rollback", "uninstall"},
			"RiskLevel":             {"low", "medium", "high"},
			"ReleaseStatus":         {"draft", "released", "deprecated"},
			"ReleaseCompatibility":  {"not_applicable", "compatible", "breaking"},
		}[t.Name()]; len(values) > 0 {
			s["enum"] = values
		}
	}
	if t.PkgPath() == "codex/platform-demo/internal/service" {
		if values := map[string][]string{"ReleaseDraftMode": {"new_line", "evolution"}, "ComponentTestMode": {"install_verify", "rollback", "evolution_round_trip"}, "RollbackVerificationKind": {"target_release", "rollback_only"}}[t.Name()]; len(values) > 0 {
			s["enum"] = values
		}
	}
	return s
}
