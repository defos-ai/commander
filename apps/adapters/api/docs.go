package api

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	swaggerfiles "github.com/swaggo/files/v2"
)

const documentationHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Commander API</title>
  <link rel="stylesheet" href="/docs/swagger-ui.css">
</head>
<body>
  <main id="swagger-ui"></main>
  <script src="/docs/swagger-ui-bundle.js"></script>
  <script src="/docs/initializer.js"></script>
</body>
</html>
`

const documentationInitializer = `window.ui = SwaggerUIBundle({
  url: "/openapi.json",
  dom_id: "#swagger-ui",
  deepLinking: true,
  validatorUrl: null,
  persistAuthorization: false,
  queryConfigEnabled: false,
  supportedSubmitMethods: ["get", "post"],
  presets: [SwaggerUIBundle.presets.apis]
});
`

// Swagger UI uses inline style attributes. Only its HTML page receives this
// policy; scripts, API responses and errors keep the default restrictive policy.
const documentationCSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

func registerDocumentation(mux *http.ServeMux) error {
	document, err := openAPIDocument()
	if err != nil {
		return err
	}
	files := []struct {
		path        string
		contentType string
		content     []byte
	}{
		{"/docs/{$}", "text/html; charset=utf-8", []byte(documentationHTML)},
		{"/docs/initializer.js", "text/javascript; charset=utf-8", []byte(documentationInitializer)},
		{"/openapi.json", "application/json", document},
	}
	for _, asset := range []struct{ name, contentType string }{
		{"swagger-ui.css", "text/css; charset=utf-8"},
		{"swagger-ui-bundle.js", "text/javascript; charset=utf-8"},
	} {
		content, err := fs.ReadFile(swaggerfiles.FS, asset.name)
		if err != nil {
			return fmt.Errorf("load API documentation asset: %w", err)
		}
		files = append(files, struct {
			path        string
			contentType string
			content     []byte
		}{"/docs/" + asset.name, asset.contentType, content})
	}
	for _, file := range files {
		mux.HandleFunc("GET "+file.path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", file.contentType)
			if file.path == "/docs/{$}" {
				w.Header().Set("Content-Security-Policy", documentationCSP)
			}
			http.ServeContent(w, r, file.path, time.Time{}, bytes.NewReader(file.content))
		})
	}
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/", http.StatusTemporaryRedirect)
	})
	return nil
}
