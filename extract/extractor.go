// Package extract discovers links, forms, and other crawl targets from a fetched page.
package extract

import (
	"context"
	"net/url"

	"github.com/commonhuman-lab/chcrawl/fetch"
	"golang.org/x/net/html"
)

// Param is a single form or query parameter.
type Param struct {
	Name  string
	Value string
}

// Discovery is one thing an Extractor found on a page: a link to follow, or a form/endpoint worth recording.
type Discovery struct {
	Kind   string // "link", "form", "code_path"
	URL    string
	Method string            // "GET"/"POST", relevant for forms
	Params []Param           // injectable params, for forms
	Base   map[string]string // hidden/submit fields replayed as-is, for forms
	Meta   map[string]string
}

// Input is the shared, already-parsed input handed to every Extractor, so HTML is parsed exactly
// once regardless of how many extractors run over it.
type Input struct {
	Resp    *fetch.Response
	BaseURL *url.URL
	Doc     *html.Node // nil for non-HTML content types
}

// Extractor mines one kind of Discovery out of a page.
type Extractor interface {
	Name() string
	Applies(resp *fetch.Response) bool
	Extract(ctx context.Context, in Input) ([]Discovery, error)
}

// Registry runs a set of Extractors over one Input.
type Registry struct {
	extractors []Extractor
}

func NewRegistry(extractors ...Extractor) *Registry {
	return &Registry{extractors: extractors}
}

func (r *Registry) RunAll(ctx context.Context, in Input) ([]Discovery, []error) {
	var all []Discovery
	var errs []error
	for _, e := range r.extractors {
		if !e.Applies(in.Resp) {
			continue
		}
		found, err := e.Extract(ctx, in)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		all = append(all, found...)
	}
	return all, errs
}
