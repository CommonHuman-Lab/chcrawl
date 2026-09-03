package openapi

import (
	"sort"
	"strings"
)

// resolveRef walks a "#/a/b/c" JSON pointer against root; an unresolvable or external reference
// returns ok=false, and the caller falls back to the original unresolved object.
func resolveRef(root map[string]interface{}, ref string) (map[string]interface{}, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	var cur interface{} = root
	for _, p := range parts {
		p = strings.NewReplacer("~1", "/", "~0", "~").Replace(p)
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	m, ok := cur.(map[string]interface{})
	return m, ok
}

// resolveObj resolves obj["$ref"] against root if present, else falls back to obj itself.
func resolveObj(root map[string]interface{}, obj map[string]interface{}) map[string]interface{} {
	if obj == nil {
		return nil
	}
	ref := asString(obj["$ref"])
	if ref == "" {
		return obj
	}
	if resolved, ok := resolveRef(root, ref); ok {
		return resolved
	}
	return obj
}

type paramKey struct{ name, in string }

// mergeParams combines path-level and operation-level parameter lists (resolving $ref entries),
// with operation-level parameters overriding a path-level one of the same (name, in).
func mergeParams(root map[string]interface{}, pathLevel, opLevel []interface{}) []map[string]interface{} {
	merged := map[paramKey]map[string]interface{}{}
	var order []paramKey
	add := func(list []interface{}) {
		for _, raw := range list {
			obj := resolveObj(root, asMap(raw))
			if obj == nil {
				continue
			}
			key := paramKey{asString(obj["name"]), asString(obj["in"])}
			if _, exists := merged[key]; !exists {
				order = append(order, key)
			}
			merged[key] = obj
		}
	}
	add(pathLevel)
	add(opLevel)

	result := make([]map[string]interface{}, 0, len(order))
	for _, k := range order {
		result = append(result, merged[k])
	}
	return result
}

// schemaPropertyNames resolves a $ref schema if needed and returns its property names, sorted.
func schemaPropertyNames(root map[string]interface{}, schema map[string]interface{}) []string {
	schema = resolveObj(root, schema)
	if schema == nil {
		return nil
	}
	props := asMap(schema["properties"])
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// bucketParams splits a merged parameter list into path/query params, plus (v2-only) formData params.
func bucketParams(params []map[string]interface{}) (pathParams, queryParams, formParams []string) {
	for _, p := range params {
		name := asString(p["name"])
		if name == "" {
			continue
		}
		switch asString(p["in"]) {
		case "path":
			pathParams = append(pathParams, name)
		case "query":
			queryParams = append(queryParams, name)
		case "formData":
			formParams = append(formParams, name)
		}
	}
	return
}

// parseV2 handles Swagger 2.0 documents: body params come from an "in": "body" parameter's schema, or from formData directly.
func parseV2(doc map[string]interface{}, sourceURL, origin string) (*Spec, error) {
	basePath := asString(doc["basePath"])
	paths := asMap(doc["paths"])

	spec := &Spec{SourceURL: sourceURL}
	for _, rawPath := range sortedPathKeys(paths) {
		pathItem := asMap(paths[rawPath])
		pathLevelParams := asList(pathItem["parameters"])

		for _, method := range httpMethods {
			op := asMap(pathItem[method])
			if op == nil {
				continue
			}
			merged := mergeParams(doc, pathLevelParams, asList(op["parameters"]))
			pathParams, queryParams, formParams := bucketParams(merged)

			var bodyParams []string
			bodyParams = append(bodyParams, formParams...)
			for _, p := range merged {
				if asString(p["in"]) == "body" {
					bodyParams = append(bodyParams, schemaPropertyNames(doc, asMap(p["schema"]))...)
				}
			}

			spec.Endpoints = append(spec.Endpoints, Endpoint{
				URL:         origin + basePath + rawPath,
				Method:      strings.ToUpper(method),
				PathParams:  pathParams,
				QueryParams: queryParams,
				BodyParams:  bodyParams,
				RawPath:     rawPath,
			})
		}
	}
	return spec, nil
}

// parseV3 handles OpenAPI 3.x documents: body params come from requestBody.content, preferring
// application/json and falling back to the first available content type.
func parseV3(doc map[string]interface{}, sourceURL, origin string) (*Spec, error) {
	basePath := firstServerPath(asList(doc["servers"]))
	paths := asMap(doc["paths"])

	spec := &Spec{SourceURL: sourceURL}
	for _, rawPath := range sortedPathKeys(paths) {
		pathItem := asMap(paths[rawPath])
		pathLevelParams := asList(pathItem["parameters"])

		for _, method := range httpMethods {
			op := asMap(pathItem[method])
			if op == nil {
				continue
			}
			merged := mergeParams(doc, pathLevelParams, asList(op["parameters"]))
			pathParams, queryParams, _ := bucketParams(merged)

			var bodyParams []string
			if rb := resolveObj(doc, asMap(op["requestBody"])); rb != nil {
				schema := preferredBodySchema(asMap(rb["content"]))
				bodyParams = schemaPropertyNames(doc, schema)
			}

			spec.Endpoints = append(spec.Endpoints, Endpoint{
				URL:         origin + basePath + rawPath,
				Method:      strings.ToUpper(method),
				PathParams:  pathParams,
				QueryParams: queryParams,
				BodyParams:  bodyParams,
				RawPath:     rawPath,
			})
		}
	}
	return spec, nil
}

func firstServerPath(servers []interface{}) string {
	if len(servers) == 0 {
		return ""
	}
	server := asMap(servers[0])
	rawURL := asString(server["url"])
	// A server URL may be a bare path ("/v1") or a full URL — either way we only want the path.
	if i := strings.Index(rawURL, "://"); i != -1 {
		rest := rawURL[i+3:]
		if j := strings.IndexByte(rest, '/'); j != -1 {
			return rest[j:]
		}
		return ""
	}
	return rawURL
}

func preferredBodySchema(content map[string]interface{}) map[string]interface{} {
	if mt := asMap(content["application/json"]); mt != nil {
		return asMap(mt["schema"])
	}
	for _, mt := range content {
		m := asMap(mt)
		if schema := asMap(m["schema"]); schema != nil {
			return schema
		}
	}
	return nil
}
