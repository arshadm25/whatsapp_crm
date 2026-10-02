package devportal

import (
	"net/http"

	"github.com/arshadm25/whatsapp_crm/api"
)

const docsPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Ecogo WhatsApp API</title></head>
<body><redoc spec-url="/v1/openapi.yaml"></redoc>
<script src="https://cdn.jsdelivr.net/npm/redoc@2/bundles/redoc.standalone.js"></script></body></html>`

// OpenAPISpec serves the public API description.
func OpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(api.OpenAPI)
}

// Docs serves the rendered API reference.
func Docs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src https://cdn.jsdelivr.net 'unsafe-inline' blob:; "+
		"worker-src blob:; style-src 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com data:; "+
		"img-src data: https:; connect-src 'self'; frame-ancestors 'none'")
	_, _ = w.Write([]byte(docsPage))
}
