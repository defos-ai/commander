package fakes

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func WriteModelDelta(w http.ResponseWriter, delta map[string]any, finish any) {
	w.Header().Set("Content-Type", "text/event-stream")
	encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
	fmt.Fprintf(w, "data: %s\n\n", encoded)
	if finish != nil {
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}
