package extract

import "golang.org/x/net/html"

// FindBaseHref returns the value of the first <base href="..."> element in
// doc, if any. The caller decides whether to trust it (e.g. only when
// same-origin) rather than always resolving relative URLs against the
// redirect-resolved response URL.
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
