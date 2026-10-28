package host

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
)

func CORSRequest(ctx context.Context, handler http.Handler, method, path, body string, configure ...func(*http.Request)) Response {
	request := httptest.NewRequestWithContext(ctx, method, "http://localhost"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for _, apply := range configure {
		apply(request)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return Response{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}
}
