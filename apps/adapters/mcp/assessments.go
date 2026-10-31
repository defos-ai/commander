package missionsmcp

func assessmentSchema() map[string]any {
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	network := object(map[string]any{"id": stringProperty, "name": stringProperty, "cidrs": map[string]any{"type": "array", "minItems": 1, "items": stringProperty}}, "id", "name", "cidrs")
	domain := object(map[string]any{"hostname": stringProperty, "include_subdomains": map[string]any{"type": "boolean"}}, "hostname", "include_subdomains")
	scope := object(map[string]any{"networks": map[string]any{"type": "array", "items": network}, "domains": map[string]any{"type": "array", "items": domain}}, "networks", "domains")
	return object(map[string]any{
		"target_id": stringProperty, "target_name": stringProperty, "revision": map[string]any{"type": "integer", "minimum": 1},
		"network_id": map[string]any{"type": []string{"string", "null"}}, "scope": scope,
	}, "target_id", "target_name", "revision", "scope")
}
