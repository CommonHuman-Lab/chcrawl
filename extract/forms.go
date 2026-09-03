package extract

import (
	"context"
	"net/url"
	"strings"

	"github.com/commonhuman-lab/chcrawl/fetch"
	"golang.org/x/net/html"
)

// requiredDefaults gives required fields with no preset value a type-appropriate non-empty
// default, so replaying the form won't fail validation on a field that isn't the injection target.
var requiredDefaults = map[string]string{
	"text":     "test",
	"search":   "test",
	"email":    "test@example.com",
	"password": "Test1234!",
	"number":   "1",
	"range":    "1",
	"url":      "http://example.com",
	"tel":      "1234567890",
	"date":     "2024-01-01",
	"time":     "12:00",
	"color":    "#000000",
}

var skipInputTypes = map[string]bool{"button": true, "image": true, "reset": true}

// FormExtractor finds <form> elements and their injectable fields.
type FormExtractor struct{}

func (FormExtractor) Name() string { return "forms" }

func (FormExtractor) Applies(resp *fetch.Response) bool {
	return strings.Contains(strings.ToLower(resp.ContentType), "html")
}

func (FormExtractor) Extract(ctx context.Context, in Input) ([]Discovery, error) {
	if in.Doc == nil {
		return nil, nil
	}
	var out []Discovery
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			if d, ok := extractForm(n, in.BaseURL); ok {
				out = append(out, d)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(in.Doc)
	return out, nil
}

func extractForm(form *html.Node, base *url.URL) (Discovery, bool) {
	action := base.String()
	if raw := strings.TrimSpace(attrStr(form.Attr, "action")); raw != "" {
		if abs, err := resolve(base, raw); err == nil {
			action = abs
		}
	}
	method := strings.ToUpper(strings.TrimSpace(attrStr(form.Attr, "method")))
	if method == "" {
		method = "GET"
	}

	params := map[string]string{}
	baseData := map[string]string{}

	var walkFields func(n *html.Node)
	walkFields = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "input":
				inputType := strings.ToLower(attrStr(n.Attr, "type"))
				if inputType == "" {
					inputType = "text"
				}
				name := strings.TrimSpace(attrStr(n.Attr, "name"))
				if name == "" || skipInputTypes[inputType] {
					break
				}
				value := attrStr(n.Attr, "value")
				switch inputType {
				case "submit", "hidden":
					baseData[name] = value
				default:
					_, required := attrVal(n.Attr, "required")
					if value == "" && required {
						value = requiredDefaults[inputType]
						if value == "" {
							value = "test"
						}
					}
					params[name] = value
				}
			case "select":
				name := strings.TrimSpace(attrStr(n.Attr, "name"))
				if name != "" {
					params[name] = selectValue(n)
				}
			case "textarea":
				name := strings.TrimSpace(attrStr(n.Attr, "name"))
				if name != "" {
					value := ""
					if _, required := attrVal(n.Attr, "required"); required {
						value = "test"
					}
					params[name] = value
				}
			}
		}
		// A <form> cannot legally nest another <form>, so a plain depth-first walk is safe.
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walkFields(c)
		}
	}
	walkFields(form)

	if len(params) == 0 {
		return Discovery{}, false
	}

	paramList := make([]Param, 0, len(params))
	for k, v := range params {
		paramList = append(paramList, Param{Name: k, Value: v})
	}

	return Discovery{
		Kind:   "form",
		URL:    action,
		Method: method,
		Params: paramList,
		Base:   baseData,
	}, true
}

func selectValue(selectNode *html.Node) string {
	first, selected := "", ""
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "option" {
			v := attrStr(n.Attr, "value")
			if first == "" {
				first = v
			}
			if _, ok := attrVal(n.Attr, "selected"); ok {
				selected = v
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(selectNode)
	if selected != "" {
		return selected
	}
	return first
}
