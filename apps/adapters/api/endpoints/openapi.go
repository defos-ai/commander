package endpoints

func OpenAPIQueryOperation(id, summary, tag string, schema any) map[string]any {
	return map[string]any{
		"operationId": id, "summary": summary, "tags": []string{tag},
		"responses": map[string]any{
			"200": OpenAPIResponse("Success", schema),
			"400": OpenAPIResponse("invalid_request_origin", OpenAPIRef("Error")),
			"401": OpenAPIResponse("authentication_required", OpenAPIRef("Error")),
			"403": OpenAPIResponse("host_not_allowed or cross_origin_request", OpenAPIRef("Error")),
			"500": OpenAPIResponse("internal_error", OpenAPIRef("Error")),
			"504": OpenAPIResponse("request_timeout", OpenAPIRef("Error")),
		},
	}
}

func OpenAPIResponse(description string, schema any) map[string]any {
	return map[string]any{
		"description": description,
		"content":     map[string]any{"application/json": map[string]any{"schema": schema}},
	}
}

func OpenAPIRef(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}
