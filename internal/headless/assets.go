package headless

import (
	"net/url"
	"path"
	"strings"
)

// assetExtensions routes these straight to the inner plain fetcher instead of the browser.
var assetExtensions = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".json": true, ".map": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
	".webp": true, ".ico": true, ".bmp": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true, ".otf": true,
	".pdf": true, ".zip": true, ".gz": true, ".tar": true,
	".mp3": true, ".mp4": true, ".webm": true, ".ogg": true, ".wav": true,
	".xml": true, ".txt": true, ".csv": true,
}

// isAsset reports whether reqURL should bypass the browser. Extensionless routes always go
// through the browser — a known false positive accepted over the cost of a HEAD pre-check.
func isAsset(reqURL string) bool {
	u, err := url.Parse(reqURL)
	if err != nil {
		return false
	}
	ext := strings.ToLower(path.Ext(u.Path))
	return assetExtensions[ext]
}
