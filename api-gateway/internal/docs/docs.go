// Package docs serve a especificação OpenAPI e o Swagger UI.
package docs

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed openapi.yaml
var spec []byte

const swaggerHTML = `<!doctype html>
<html lang="pt-BR">
<head>
  <meta charset="utf-8">
  <title>FleetTrack API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>window.ui = SwaggerUIBundle({ url: '/openapi.yaml', dom_id: '#swagger-ui' });</script>
</body>
</html>`

// UI devolve a página do Swagger UI.
func UI(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(swaggerHTML))
}

// Spec devolve o arquivo OpenAPI.
func Spec(c *gin.Context) {
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", spec)
}
