package benchlab

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Tool wraps a crawler CLI — chcrawl itself, or an external tool — for
// head-to-head comparison against the same local synthetic target. All
// four tools (chcrawl, katana, hakrawler, gospider) are run through this
// one interface so the comparison is apples-to-apples: same process-exec
// model, same output-to-found-set scoring.
type Tool struct {
	Name string
	// Binary is looked up via exec.LookPath, which also accepts an
	// absolute path directly (used for the chcrawl binary built by the
	// comparison harness).
	Binary string
	// Build constructs the command to run. seedURL is the full URL to
	// crawl (e.g. "http://127.0.0.1:PORT/").
	Build func(ctx context.Context, binary, seedURL string, maxDepth int) *exec.Cmd
	// Stdin returns the process's stdin content, or "" for none.
	Stdin func(seedURL string) string
	// Parse extracts the set of absolute URLs the tool reported from its
	// captured stdout.
	Parse func(seedURL string, stdout []byte) map[string]bool
}

// Available reports whether the tool's binary can be found (via PATH, or
// directly if Binary is an absolute/relative path).
func (t Tool) Available() bool {
	_, err := exec.LookPath(t.Binary)
	return err == nil
}

// ExternalTools returns the third-party crawlers benchmarked against
// chcrawl. Each is scored only if its binary is present on PATH — a
// missing tool is reported as "not installed", not a failure.
func ExternalTools() []Tool {
	return []Tool{katanaTool(), hakrawlerTool(), gospiderTool()}
}

// ChcrawlTool wraps a pre-built chcrawl binary as a Tool, using the same
// coverage-scoring path as the external tools it's compared against.
func ChcrawlTool(binaryPath string) Tool {
	return Tool{
		Name:   "chcrawl",
		Binary: binaryPath,
		Build: func(ctx context.Context, binary, seedURL string, maxDepth int) *exec.Cmd {
			return exec.CommandContext(ctx, binary,
				"-max-depth", strconv.Itoa(maxDepth),
				"-max-pages", "5000",
				"-timeout", "5s",
				seedURL)
		},
		Parse: parseChcrawl,
	}
}

func parseChcrawl(seedURL string, stdout []byte) map[string]bool {
	found := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Type == "page" && rec.URL != "" {
			found[rec.URL] = true
		}
	}
	return found
}

func katanaTool() Tool {
	return Tool{
		Name:   "katana",
		Binary: "katana",
		Build: func(ctx context.Context, binary, seedURL string, maxDepth int) *exec.Cmd {
			return exec.CommandContext(ctx, binary,
				"-u", seedURL,
				"-jc", "-silent", "-jsonl",
				"-d", strconv.Itoa(maxDepth),
				"-timeout", "5",
				"-retry", "1",
				"-c", "10",
				"-duc", // disable katana's automatic update check — every benchmark tool call must stay 127.0.0.1-only
			)
		},
		Parse: parseKatana,
	}
}

func parseKatana(seedURL string, stdout []byte) map[string]bool {
	found := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Request struct {
				Endpoint string `json:"endpoint"`
			} `json:"request"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Request.Endpoint != "" {
			found[rec.Request.Endpoint] = true
		}
	}
	return found
}

func hakrawlerTool() Tool {
	return Tool{
		Name:   "hakrawler",
		Binary: "hakrawler",
		Stdin:  func(seedURL string) string { return seedURL + "\n" },
		Build: func(ctx context.Context, binary, seedURL string, maxDepth int) *exec.Cmd {
			return exec.CommandContext(ctx, binary,
				"-json", "-u",
				"-d", strconv.Itoa(maxDepth),
				"-t", "10",
				"-timeout", "5",
			)
		},
		Parse: parseHakrawler,
	}
}

func parseHakrawler(seedURL string, stdout []byte) map[string]bool {
	// hakrawler never re-emits the seed URL itself, only what it found
	// from it — the seed was still genuinely fetched, so it counts.
	found := map[string]bool{seedURL: true}
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec struct {
			URL string `json:"URL"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.URL != "" {
			found[rec.URL] = true
		}
	}
	return found
}

func gospiderTool() Tool {
	return Tool{
		Name:   "gospider",
		Binary: "gospider",
		Build: func(ctx context.Context, binary, seedURL string, maxDepth int) *exec.Cmd {
			return exec.CommandContext(ctx, binary,
				"-s", seedURL,
				"-d", strconv.Itoa(maxDepth),
				"-t", "1",
				"-c", "5",
				"-m", "5",
			)
		},
		Parse: parseGospider,
	}
}

// gospiderLineRE matches gospider's plain-text output lines, e.g.:
//
//	[url] - [code-200] - http://host/path
//	[javascript] - http://host/app.js
//	[linkfinder] - [from: http://host/app.js] - /api/users/1
//
// Only url/javascript/linkfinder are counted as discovered URLs; [form]
// lines report the page the form was found on, not the form's action, so
// they carry no additional URL information.
var gospiderLineRE = regexp.MustCompile(`^\[(?:url|javascript|linkfinder)\]\s*-\s*(?:\[code-\d+\]\s*-\s*)?(?:\[from:[^\]]*\]\s*-\s*)?(.+)$`)

func parseGospider(seedURL string, stdout []byte) map[string]bool {
	found := map[string]bool{seedURL: true}
	base, err := url.Parse(seedURL)
	if err != nil {
		return found
	}
	for _, line := range strings.Split(string(stdout), "\n") {
		line = strings.TrimSpace(line)
		m := gospiderLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		raw := strings.TrimSpace(m[1])
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		found[base.ResolveReference(u).String()] = true
	}
	return found
}
