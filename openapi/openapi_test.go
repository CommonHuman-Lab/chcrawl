package openapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/commonhuman-lab/chcrawl/fetch"
)

func newTestFetcher(t *testing.T) fetch.Fetcher {
	t.Helper()
	f, err := fetch.New(fetch.Config{Timeout: 5 * time.Second, MaxBodyBytes: 1 << 20, AllowedContentTypes: nil})
	if err != nil {
		t.Fatalf("fetch.New: %v", err)
	}
	return f
}

func endpointByPath(spec *Spec, method, path string) *Endpoint {
	for i := range spec.Endpoints {
		e := &spec.Endpoints[i]
		if e.RawPath == path && e.Method == method {
			return e
		}
	}
	return nil
}

const v2Doc = `{
  "swagger": "2.0",
  "basePath": "/api",
  "definitions": {
    "PetBody": {
      "type": "object",
      "properties": {"name": {"type": "string"}, "tag": {"type": "string"}}
    }
  },
  "paths": {
    "/pets/{petId}": {
      "parameters": [{"name": "petId", "in": "path", "required": true, "type": "string"}],
      "get": {
        "parameters": [{"name": "verbose", "in": "query", "type": "boolean"}],
        "responses": {"200": {"description": "ok"}}
      },
      "put": {
        "parameters": [{"$ref": "#/parameters/PetBodyParam"}],
        "responses": {"200": {"description": "ok"}}
      }
    },
    "/pets": {
      "post": {
        "parameters": [{"name": "body", "in": "body", "schema": {"$ref": "#/definitions/PetBody"}}],
        "responses": {"201": {"description": "created"}}
      }
    },
    "/upload": {
      "post": {
        "parameters": [
          {"name": "file", "in": "formData", "type": "file"},
          {"name": "note", "in": "formData", "type": "string"}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    }
  },
  "parameters": {
    "PetBodyParam": {"name": "body", "in": "body", "schema": {"$ref": "#/definitions/PetBody"}}
  }
}`

func TestLoad_V2_PathAndQueryParams(t *testing.T) {
	spec, err := Load([]byte(v2Doc), "https://api.example.com/swagger.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	get := endpointByPath(spec, "GET", "/pets/{petId}")
	if get == nil {
		t.Fatal("GET /pets/{petId} not found")
	}
	if get.URL != "https://api.example.com/api/pets/{petId}" {
		t.Errorf("URL = %q", get.URL)
	}
	if len(get.PathParams) != 1 || get.PathParams[0] != "petId" {
		t.Errorf("PathParams = %v, want [petId] (from the path-level parameter, merged into the GET op)", get.PathParams)
	}
	if len(get.QueryParams) != 1 || get.QueryParams[0] != "verbose" {
		t.Errorf("QueryParams = %v, want [verbose]", get.QueryParams)
	}
}

func TestLoad_V2_RefResolvedBodyParam(t *testing.T) {
	spec, err := Load([]byte(v2Doc), "https://api.example.com/swagger.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	post := endpointByPath(spec, "POST", "/pets")
	if post == nil {
		t.Fatal("POST /pets not found")
	}
	sort.Strings(post.BodyParams)
	if len(post.BodyParams) != 2 || post.BodyParams[0] != "name" || post.BodyParams[1] != "tag" {
		t.Errorf("BodyParams = %v, want [name tag] (resolved through schema $ref to definitions/PetBody)", post.BodyParams)
	}
}

func TestLoad_V2_RefResolvedParameter(t *testing.T) {
	spec, err := Load([]byte(v2Doc), "https://api.example.com/swagger.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	put := endpointByPath(spec, "PUT", "/pets/{petId}")
	if put == nil {
		t.Fatal("PUT /pets/{petId} not found")
	}
	sort.Strings(put.BodyParams)
	if len(put.BodyParams) != 2 || put.BodyParams[0] != "name" {
		t.Errorf("BodyParams = %v, want [name tag] (parameter itself was a $ref to #/parameters/PetBodyParam)", put.BodyParams)
	}
	// petId is still merged in from the path-level parameter.
	if len(put.PathParams) != 1 || put.PathParams[0] != "petId" {
		t.Errorf("PathParams = %v, want [petId]", put.PathParams)
	}
}

func TestLoad_V2_FormDataIsBodyEquivalent(t *testing.T) {
	spec, err := Load([]byte(v2Doc), "https://api.example.com/swagger.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	upload := endpointByPath(spec, "POST", "/upload")
	if upload == nil {
		t.Fatal("POST /upload not found")
	}
	sort.Strings(upload.BodyParams)
	if len(upload.BodyParams) != 2 || upload.BodyParams[0] != "file" || upload.BodyParams[1] != "note" {
		t.Errorf("BodyParams = %v, want [file note]", upload.BodyParams)
	}
}

const v3Doc = `{
  "openapi": "3.0.1",
  "servers": [{"url": "https://api.example.com/v2"}],
  "components": {
    "schemas": {
      "Order": {
        "type": "object",
        "properties": {"item": {"type": "string"}, "qty": {"type": "integer"}}
      }
    }
  },
  "paths": {
    "/orders/{orderId}": {
      "get": {
        "parameters": [
          {"name": "orderId", "in": "path", "required": true, "schema": {"type": "string"}},
          {"name": "expand", "in": "query", "schema": {"type": "boolean"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    },
    "/orders": {
      "post": {
        "requestBody": {
          "content": {
            "application/json": {"schema": {"$ref": "#/components/schemas/Order"}}
          }
        },
        "responses": {"201": {"description": "created"}}
      }
    }
  }
}`

func TestLoad_V3_ServerURLBecomesBasePath(t *testing.T) {
	spec, err := Load([]byte(v3Doc), "https://api.example.com/openapi.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	get := endpointByPath(spec, "GET", "/orders/{orderId}")
	if get == nil {
		t.Fatal("GET /orders/{orderId} not found")
	}
	if get.URL != "https://api.example.com/v2/orders/{orderId}" {
		t.Errorf("URL = %q, want origin + server path + raw path", get.URL)
	}
	if len(get.PathParams) != 1 || get.PathParams[0] != "orderId" {
		t.Errorf("PathParams = %v", get.PathParams)
	}
	if len(get.QueryParams) != 1 || get.QueryParams[0] != "expand" {
		t.Errorf("QueryParams = %v", get.QueryParams)
	}
}

func TestLoad_V3_RequestBodyJSONSchema(t *testing.T) {
	spec, err := Load([]byte(v3Doc), "https://api.example.com/openapi.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	post := endpointByPath(spec, "POST", "/orders")
	if post == nil {
		t.Fatal("POST /orders not found")
	}
	sort.Strings(post.BodyParams)
	if len(post.BodyParams) != 2 || post.BodyParams[0] != "item" || post.BodyParams[1] != "qty" {
		t.Errorf("BodyParams = %v, want [item qty]", post.BodyParams)
	}
}

func TestLoad_YAML(t *testing.T) {
	yamlDoc := `
openapi: "3.0.0"
paths:
  /ping:
    get:
      responses:
        "200":
          description: ok
`
	spec, err := Load([]byte(yamlDoc), "https://api.example.com/openapi.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if endpointByPath(spec, "GET", "/ping") == nil {
		t.Errorf("expected GET /ping from YAML doc, got %+v", spec.Endpoints)
	}
}

func TestLoad_InvalidDocument(t *testing.T) {
	if _, err := Load([]byte("not a spec at all"), "https://example.com/x"); err == nil {
		t.Error("expected an error for a non-OpenAPI document")
	}
}

func TestDiscover_FindsSpecAtCanonicalPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(v3Doc))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	spec, err := Discover(context.Background(), newTestFetcher(t), srv.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if spec == nil {
		t.Fatal("expected a spec to be found")
	}
	if len(spec.Endpoints) == 0 {
		t.Error("expected endpoints to be parsed")
	}
}

func TestDiscover_FollowsEmbeddedSpecURLFromHTMLPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/swagger-ui.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><script>url: "/api/openapi-spec.json"</script></html>`))
	})
	mux.HandleFunc("/api/openapi-spec.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(v3Doc))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	spec, err := Discover(context.Background(), newTestFetcher(t), srv.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if spec == nil {
		t.Fatal("expected a spec to be found via the embedded URL in the Swagger UI page")
	}
}

func TestDiscover_NoSpecFoundReturnsNilNotError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	spec, err := Discover(context.Background(), newTestFetcher(t), srv.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if spec != nil {
		t.Errorf("expected nil spec when nothing is found, got %+v", spec)
	}
}
