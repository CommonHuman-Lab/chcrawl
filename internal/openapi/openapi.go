// Package openapi discovers and parses OpenAPI/Swagger specs. It is a
// standalone utility — never wired into the core crawl loop — that a
// caller invokes explicitly against a target origin.
package openapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/commonhuman-lab/chcrawl/internal/fetch"
	"gopkg.in/yaml.v3"
)

// specPaths are canonical locations probed for an OpenAPI/Swagger document
// or a Swagger UI / Redoc page embedding one.
var specPaths = []string{
	"/openapi.json", "/openapi.yaml", "/openapi.yml",
	"/swagger.json", "/swagger.yaml", "/swagger.yml",
	"/v2/api-docs", "/v3/api-docs", "/api-docs",
	"/api/openapi.json", "/api/swagger.json",
	"/api/v1/openapi.json", "/v1/openapi.json", "/v1/swagger.json",
	"/swagger/v1/swagger.json", "/docs/openapi.json", "/spec/openapi.json",
	"/swagger-ui.html", "/redoc",
}

// specURLRe finds an embedded spec URL referenced from a Swagger UI /
// Redoc HTML page.
var specURLRe = regexp.MustCompile(`["']((?:https?://[^"']+)?/[^"']*(?:openapi|swagger)[^"']*\.(?:json|yaml))["']`)

// Endpoint is one discovered API operation.
type Endpoint struct {
	URL         string
	Method      string
	PathParams  []string
	QueryParams []string
	BodyParams  []string
	RawPath     string
}

// Spec is a parsed OpenAPI/Swagger document.
type Spec struct {
	SourceURL string
	Endpoints []Endpoint
}

// Discover probes baseURL's canonical spec locations and returns the first
// valid spec found, or nil if none of them yielded one.
func Discover(ctx context.Context, fetcher fetch.Fetcher, baseURL string) (*Spec, error) {
	base := strings.TrimRight(baseURL, "/")
	for _, p := range specPaths {
		u := base + p
		resp, err := fetcher.Fetch(ctx, fetch.Request{URL: u, Method: "GET"})
		if err != nil || resp.StatusCode >= 400 {
			continue
		}

		if strings.Contains(strings.ToLower(resp.ContentType), "html") {
			specURL, ok := findEmbeddedSpecURL(resp.Body, u)
			if !ok {
				continue
			}
			specResp, err := fetcher.Fetch(ctx, fetch.Request{URL: specURL, Method: "GET"})
			if err != nil || specResp.StatusCode >= 400 {
				continue
			}
			if spec, err := Load(specResp.Body, specURL); err == nil {
				return spec, nil
			}
			continue
		}

		if spec, err := Load(resp.Body, u); err == nil {
			return spec, nil
		}
	}
	return nil, nil
}

func findEmbeddedSpecURL(html []byte, pageURL string) (string, bool) {
	m := specURLRe.FindSubmatch(html)
	if m == nil {
		return "", false
	}
	raw := string(m[1])
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw, true
	}
	base, err := url.Parse(pageURL)
	if err != nil {
		return "", false
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	return base.ResolveReference(ref).String(), true
}

// Load parses spec bytes (JSON or YAML) into a Spec. sourceURL is used to
// derive the origin (and, if present, the spec's declared base path) that
// endpoint URLs are built against.
func Load(data []byte, sourceURL string) (*Spec, error) {
	doc, err := decode(data)
	if err != nil {
		return nil, err
	}

	origin, err := originOf(sourceURL)
	if err != nil {
		return nil, err
	}

	switch {
	case asString(doc["swagger"]) != "":
		return parseV2(doc, sourceURL, origin)
	case strings.HasPrefix(asString(doc["openapi"]), "3."):
		return parseV3(doc, sourceURL, origin)
	case doc["paths"] != nil:
		// No explicit version field but a "paths" map exists — best-effort
		// parse as v3, the more common modern convention.
		return parseV3(doc, sourceURL, origin)
	default:
		return nil, fmt.Errorf("openapi: not a recognizable OpenAPI/Swagger document")
	}
}

func decode(data []byte) (map[string]interface{}, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err == nil {
		return doc, nil
	}
	var yamlDoc map[string]interface{}
	if err := yaml.Unmarshal(data, &yamlDoc); err == nil && yamlDoc != nil {
		return normalizeYAML(yamlDoc), nil
	}
	return nil, fmt.Errorf("openapi: could not parse as JSON or YAML")
}

// normalizeYAML converts yaml.v3's map[string]interface{} decoding (which
// can produce map[interface{}]interface{} for nested maps in some
// configurations) into a consistently map[string]interface{}-typed tree,
// so downstream code only has one shape to handle.
func normalizeYAML(v interface{}) map[string]interface{} {
	out, _ := normalizeYAMLValue(v).(map[string]interface{})
	return out
}

func normalizeYAMLValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[k] = normalizeYAMLValue(val)
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = normalizeYAMLValue(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			out[i] = normalizeYAMLValue(val)
		}
		return out
	default:
		return v
	}
}

func originOf(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("openapi: invalid source URL: %w", err)
	}
	return u.Scheme + "://" + u.Host, nil
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func asMap(v interface{}) map[string]interface{} {
	m, _ := v.(map[string]interface{})
	return m
}

func asList(v interface{}) []interface{} {
	l, _ := v.([]interface{})
	return l
}

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

func sortedPathKeys(paths map[string]interface{}) []string {
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
