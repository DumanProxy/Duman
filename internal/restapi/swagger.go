package restapi

import (
	"net/http"
)

// handleSwaggerJSON serves the OpenAPI 3.0 spec at /docs/openapi.json.
func (s *Server) handleSwaggerJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(openAPISpec))
}

// handleSwaggerUI serves an inline Swagger UI page at /docs.
func (s *Server) handleSwaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(swaggerUIHTML))
}

const openAPISpec = `{
  "openapi": "3.0.3",
  "info": {
    "title": "ShopAPI - E-Commerce Platform",
    "description": "RESTful API for the ShopAPI e-commerce platform. Provides product catalog, order management, and analytics.",
    "version": "2.4.1",
    "contact": {
      "name": "API Support",
      "email": "api-support@shopapi.example.com"
    }
  },
  "servers": [
    {
      "url": "/api/v2",
      "description": "Current API version"
    }
  ],
  "security": [
    {
      "BearerAuth": []
    }
  ],
  "paths": {
    "/health": {
      "get": {
        "summary": "Health check",
        "tags": ["System"],
        "security": [],
        "responses": {
          "200": {
            "description": "Service is healthy",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "ok": { "type": "boolean" },
                    "timestamp": { "type": "string", "format": "date-time" }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/status": {
      "get": {
        "summary": "Server status",
        "tags": ["System"],
        "responses": {
          "200": {
            "description": "Server status information",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "version": { "type": "string" },
                    "uptime": { "type": "string" },
                    "status": { "type": "string" }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/products": {
      "get": {
        "summary": "List products",
        "tags": ["Products"],
        "parameters": [
          { "name": "page", "in": "query", "schema": { "type": "integer", "default": 1 } },
          { "name": "limit", "in": "query", "schema": { "type": "integer", "default": 20 } },
          { "name": "category_id", "in": "query", "schema": { "type": "integer" } }
        ],
        "responses": {
          "200": {
            "description": "Paginated product list",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "data": {
                      "type": "array",
                      "items": { "$ref": "#/components/schemas/ProductSummary" }
                    },
                    "page": { "type": "integer" },
                    "limit": { "type": "integer" },
                    "total": { "type": "integer" }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/products/{id}": {
      "get": {
        "summary": "Get product by ID",
        "tags": ["Products"],
        "parameters": [
          { "name": "id", "in": "path", "required": true, "schema": { "type": "integer" } }
        ],
        "responses": {
          "200": {
            "description": "Product details",
            "content": {
              "application/json": {
                "schema": { "$ref": "#/components/schemas/Product" }
              }
            }
          },
          "404": { "description": "Product not found" }
        }
      }
    },
    "/categories": {
      "get": {
        "summary": "List categories",
        "tags": ["Categories"],
        "responses": {
          "200": {
            "description": "Category list",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": { "$ref": "#/components/schemas/Category" }
                }
              }
            }
          }
        }
      }
    },
    "/dashboard/stats": {
      "get": {
        "summary": "Dashboard statistics",
        "tags": ["Dashboard"],
        "responses": {
          "200": {
            "description": "Aggregated statistics",
            "content": {
              "application/json": {
                "schema": { "$ref": "#/components/schemas/DashboardStats" }
              }
            }
          }
        }
      }
    },
    "/analytics/events": {
      "post": {
        "summary": "Submit analytics event",
        "tags": ["Analytics"],
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": { "$ref": "#/components/schemas/AnalyticsEvent" }
            }
          }
        },
        "responses": {
          "202": { "description": "Event accepted" }
        }
      }
    },
    "/analytics/sync": {
      "get": {
        "summary": "Sync analytics data",
        "tags": ["Analytics"],
        "parameters": [
          { "name": "session_id", "in": "query", "required": true, "schema": { "type": "string" } }
        ],
        "responses": {
          "200": {
            "description": "Pending analytics data",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "session_id": { "type": "string" },
                    "chunks": {
                      "type": "array",
                      "items": {
                        "type": "object",
                        "properties": {
                          "payload": { "type": "string", "format": "byte" },
                          "stream_id": { "type": "integer" },
                          "seq": { "type": "integer" }
                        }
                      }
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
  },
  "components": {
    "securitySchemes": {
      "BearerAuth": {
        "type": "http",
        "scheme": "bearer"
      }
    },
    "schemas": {
      "ProductSummary": {
        "type": "object",
        "properties": {
          "id": { "type": "integer" },
          "name": { "type": "string" },
          "price": { "type": "number" },
          "stock": { "type": "integer" }
        }
      },
      "Product": {
        "type": "object",
        "properties": {
          "id": { "type": "integer" },
          "name": { "type": "string" },
          "price": { "type": "number" },
          "stock": { "type": "integer" },
          "description": { "type": "string" },
          "category_id": { "type": "integer" }
        }
      },
      "Category": {
        "type": "object",
        "properties": {
          "id": { "type": "integer" },
          "name": { "type": "string" }
        }
      },
      "DashboardStats": {
        "type": "object",
        "properties": {
          "total_products": { "type": "integer" },
          "total_orders": { "type": "integer" },
          "revenue": { "type": "number" },
          "active_users": { "type": "integer" }
        }
      },
      "AnalyticsEvent": {
        "type": "object",
        "required": ["session_id", "event_type"],
        "properties": {
          "session_id": { "type": "string" },
          "event_type": { "type": "string" },
          "page_url": { "type": "string" },
          "user_agent": { "type": "string" },
          "metadata": {
            "type": "object",
            "additionalProperties": { "type": "string" }
          },
          "payload": { "type": "string", "format": "byte" }
        }
      }
    }
  }
}`

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>ShopAPI - API Documentation</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #fafafa; color: #3b4151; }
    .header { background: #1b1b1b; color: #fff; padding: 16px 24px; }
    .header h1 { font-size: 20px; font-weight: 700; }
    .header p { font-size: 13px; color: #aaa; margin-top: 4px; }
    .container { max-width: 960px; margin: 24px auto; padding: 0 16px; }
    .endpoint { background: #fff; border: 1px solid #e0e0e0; border-radius: 4px; margin-bottom: 8px; overflow: hidden; }
    .endpoint-header { padding: 10px 16px; cursor: pointer; display: flex; align-items: center; gap: 12px; }
    .endpoint-header:hover { background: #f5f5f5; }
    .method { padding: 4px 10px; border-radius: 3px; font-size: 12px; font-weight: 700; color: #fff; min-width: 60px; text-align: center; }
    .get { background: #61affe; } .post { background: #49cc90; }
    .path { font-family: monospace; font-size: 14px; font-weight: 600; }
    .desc { font-size: 13px; color: #666; margin-left: auto; }
    .tag-group { margin-bottom: 24px; }
    .tag-title { font-size: 18px; font-weight: 600; margin-bottom: 8px; padding-bottom: 6px; border-bottom: 1px solid #e0e0e0; }
    a.spec-link { display: inline-block; margin-top: 16px; color: #4990e2; text-decoration: none; font-size: 14px; }
    a.spec-link:hover { text-decoration: underline; }
  </style>
</head>
<body>
  <div class="header">
    <h1>ShopAPI v2.4.1</h1>
    <p>E-Commerce Platform REST API</p>
  </div>
  <div class="container">
    <div class="tag-group">
      <div class="tag-title">System</div>
      <div class="endpoint"><div class="endpoint-header"><span class="method get">GET</span><span class="path">/api/v2/health</span><span class="desc">Health check</span></div></div>
      <div class="endpoint"><div class="endpoint-header"><span class="method get">GET</span><span class="path">/api/v2/status</span><span class="desc">Server status</span></div></div>
    </div>
    <div class="tag-group">
      <div class="tag-title">Products</div>
      <div class="endpoint"><div class="endpoint-header"><span class="method get">GET</span><span class="path">/api/v2/products</span><span class="desc">List products</span></div></div>
      <div class="endpoint"><div class="endpoint-header"><span class="method get">GET</span><span class="path">/api/v2/products/{id}</span><span class="desc">Get product by ID</span></div></div>
    </div>
    <div class="tag-group">
      <div class="tag-title">Categories</div>
      <div class="endpoint"><div class="endpoint-header"><span class="method get">GET</span><span class="path">/api/v2/categories</span><span class="desc">List categories</span></div></div>
    </div>
    <div class="tag-group">
      <div class="tag-title">Dashboard</div>
      <div class="endpoint"><div class="endpoint-header"><span class="method get">GET</span><span class="path">/api/v2/dashboard/stats</span><span class="desc">Dashboard statistics</span></div></div>
    </div>
    <div class="tag-group">
      <div class="tag-title">Analytics</div>
      <div class="endpoint"><div class="endpoint-header"><span class="method post">POST</span><span class="path">/api/v2/analytics/events</span><span class="desc">Submit analytics event</span></div></div>
      <div class="endpoint"><div class="endpoint-header"><span class="method get">GET</span><span class="path">/api/v2/analytics/sync</span><span class="desc">Sync analytics data</span></div></div>
    </div>
    <a class="spec-link" href="/docs/openapi.json">View raw OpenAPI specification (JSON)</a>
  </div>
</body>
</html>`
