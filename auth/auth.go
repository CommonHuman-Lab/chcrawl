// Package auth builds cookies/headers for authenticated crawls: form login, OAuth2 bearer login, and HTTP Basic.
package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/commonhuman-lab/chcrawl/fetch"
	"golang.org/x/net/html"
)

// Result is a portable credentials bag a caller merges into subsequent crawl requests.
type Result struct {
	Cookies string
	Headers map[string]string
}

func (r Result) IsEmpty() bool {
	return r.Cookies == "" && len(r.Headers) == 0
}

// FormLogin submits loginURL's form (username/password plus any hidden fields, e.g. a CSRF
// token) and returns the resulting session cookies, plus an Authorization: Bearer header if the
// response is JSON with a token/access_token/jwt/id_token field.
func FormLogin(ctx context.Context, fetcher fetch.Fetcher, loginURL, usernameField, username, passwordField, password string, extraFields map[string]string) (Result, error) {
	getResp, err := fetcher.Fetch(ctx, fetch.Request{URL: loginURL, Method: "GET"})
	if err != nil {
		return Result{}, fmt.Errorf("auth: fetching login page: %w", err)
	}

	action, method, hidden, err := parseLoginForm(getResp.Body, getResp.FinalURL)
	if err != nil {
		return Result{}, fmt.Errorf("auth: parsing login form: %w", err)
	}

	form := url.Values{}
	for k, v := range hidden {
		form.Set(k, v)
	}
	for k, v := range extraFields {
		form.Set(k, v)
	}
	form.Set(usernameField, username)
	form.Set(passwordField, password)

	if method == "" {
		method = "POST"
	}
	postResp, err := fetcher.Fetch(ctx, fetch.Request{
		URL:         action,
		Method:      strings.ToUpper(method),
		Body:        []byte(form.Encode()),
		ContentType: "application/x-www-form-urlencoded",
	})
	if err != nil {
		return Result{}, fmt.Errorf("auth: submitting login form: %w", err)
	}

	result := Result{Headers: map[string]string{}}
	// Read from the jar, not the raw Set-Cookie header: a login POST usually redirects, and the
	// final hop rarely re-sends the cookie the redirect's own 302 just set.
	if postResp.Cookies != "" {
		result.Cookies = postResp.Cookies
	}
	if token, ok := extractTokenFromJSON(postResp.Body); ok {
		result.Headers["Authorization"] = "Bearer " + token
	}
	return result, nil
}

// BearerLogin performs an OAuth2 grant POST and returns an Authorization: Bearer header from the JSON response.
func BearerLogin(ctx context.Context, fetcher fetch.Fetcher, tokenURL, clientID, clientSecret, grantType string) (Result, error) {
	if grantType == "" {
		grantType = "client_credentials"
	}
	form := url.Values{
		"grant_type":    {grantType},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	resp, err := fetcher.Fetch(ctx, fetch.Request{
		URL:         tokenURL,
		Method:      "POST",
		Body:        []byte(form.Encode()),
		ContentType: "application/x-www-form-urlencoded",
	})
	if err != nil {
		return Result{}, fmt.Errorf("auth: bearer login request: %w", err)
	}
	token, ok := extractTokenFromJSON(resp.Body)
	if !ok {
		return Result{}, fmt.Errorf("auth: no token found in bearer login response")
	}
	return Result{Headers: map[string]string{"Authorization": "Bearer " + token}}, nil
}

// BasicAuthHeader builds an HTTP Basic header for "user:password" (only the first colon splits).
func BasicAuthHeader(cred string) (map[string]string, error) {
	i := strings.IndexByte(cred, ':')
	if i < 0 {
		return nil, fmt.Errorf("auth: malformed basic auth credential, expected \"user:password\"")
	}
	user, pass := cred[:i], cred[i+1:]
	encoded := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	return map[string]string{"Authorization": "Basic " + encoded}, nil
}

func extractTokenFromJSON(body []byte) (string, bool) {
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", false
	}
	for _, key := range []string{"token", "access_token", "accessToken", "jwt", "id_token"} {
		if v, ok := data[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s, true
			}
		}
	}
	return "", false
}

func parseLoginForm(body []byte, baseURL string) (action, method string, hidden map[string]string, err error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", "", nil, err
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", "", nil, err
	}

	hidden = map[string]string{}
	action = baseURL
	method = "POST"
	found := false

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if found || n == nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "form" {
			found = true
			attrs := attrsOf(n)
			if raw := strings.TrimSpace(attrs["action"]); raw != "" {
				if u, err := url.Parse(raw); err == nil {
					action = base.ResolveReference(u).String()
				}
			}
			if m := strings.TrimSpace(attrs["method"]); m != "" {
				method = m
			}
			collectHidden(n, hidden)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
			if found {
				return
			}
		}
	}
	walk(doc)
	return action, method, hidden, nil
}

func collectHidden(form *html.Node, hidden map[string]string) {
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			attrs := attrsOf(n)
			if strings.EqualFold(attrs["type"], "hidden") {
				name := strings.TrimSpace(attrs["name"])
				if name != "" {
					hidden[name] = attrs["value"]
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(form)
}

func attrsOf(n *html.Node) map[string]string {
	m := make(map[string]string, len(n.Attr))
	for _, a := range n.Attr {
		m[strings.ToLower(a.Key)] = a.Val
	}
	return m
}
