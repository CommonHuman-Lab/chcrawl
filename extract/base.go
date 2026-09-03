package extract

import "golang.org/x/net/html"

// FindBaseHref returns the first <base href="..."> value in doc, if any; the caller decides
// whether to trust it (e.g. only when same-origin) over the redirect-resolved response URL.
func FindBaseHref(doc *html.Node) (string, bool) {
	var href string
	var found bool
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found || n == nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "base" {
			for _, a := range n.Attr {
				if a.Key == "href" && a.Val != "" {
					href = a.Val
					found = true
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
			if found {
				return
			}
		}
	}
	walk(doc)
	return href, found
}
