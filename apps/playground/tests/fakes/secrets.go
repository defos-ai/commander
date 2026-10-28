package fakes

import (
	"encoding/json"
	"net/http"
)

func ToolDelta(name string, args map[string]any) map[string]any {
	encoded, _ := json.Marshal(args)
	return map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": name, "type": "function", "function": map[string]any{"name": name, "arguments": string(encoded)}}}}
}

func WriteSecretFormMetadata(w http.ResponseWriter, name string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"id": "da378c90-63eb-46da-b186-552e1e144510", "agent_type": "builder-agent", "name": name, "version": 1, "status": "available"})
}

func MissingSecretFormMetadata(w http.ResponseWriter, r *http.Request, names ...string) bool {
	for _, name := range names {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/secrets/builder-agent/"+name {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "secret_not_found"})
			return true
		}
	}
	return false
}
