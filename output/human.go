package output

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// HumanWriter renders a short, human-readable stream: the first few pages and errors, then a
// final summary. Full per-page detail belongs in JSONL via -output, not the terminal.
type HumanWriter struct {
	mu       sync.Mutex
	out      io.Writer
	color    bool
	maxLines int
	pages    int
	errs     int
}

// NewHumanWriter renders to w, showing at most the first maxLines page events before collapsing
// the rest into "...". Colors are used only when w is a terminal.
func NewHumanWriter(w io.Writer, maxLines int) *HumanWriter {
	return &HumanWriter{out: w, maxLines: maxLines, color: isTerminal(w)}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiBold   = "\x1b[1m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

func (h *HumanWriter) paint(code string, s string) string {
	if !h.color {
		return s
	}
	return code + s + ansiReset
}

func (h *HumanWriter) statusColor(status int) string {
	switch {
	case status >= 200 && status < 300:
		return ansiGreen
	case status >= 300 && status < 400:
		return ansiYellow
	default:
		return ansiRed
	}
}

func (h *HumanWriter) WritePage(e PageEvent) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pages++
	if h.pages > h.maxLines {
		return nil
	}
	status := h.paint(h.statusColor(e.Status), fmt.Sprintf("%3d", e.Status))
	line := fmt.Sprintf("  [%s] %s  %s, %d discoveries\n",
		status, e.URL, humanBytes(e.BytesRead), len(e.Discoveries))
	if _, err := io.WriteString(h.out, line); err != nil {
		return err
	}
	if h.pages == h.maxLines {
		io.WriteString(h.out, h.paint(ansiDim, "  ...\n"))
	}
	return nil
}

func (h *HumanWriter) WriteError(e ErrorEvent) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.errs++
	if h.errs > h.maxLines {
		return nil
	}
	line := fmt.Sprintf("  [%s] %s  %s: %s\n", h.paint(ansiRed, "ERR"), e.URL, e.Stage, e.Error)
	if _, err := io.WriteString(h.out, line); err != nil {
		return err
	}
	if h.errs == h.maxLines {
		io.WriteString(h.out, h.paint(ansiDim, "  ...\n"))
	}
	return nil
}

func (h *HumanWriter) WriteOpenAPI(e OpenAPIEvent) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	line := fmt.Sprintf("  [%s] %d endpoints from %s\n", h.paint(ansiYellow, "API"), len(e.Endpoints), e.SourceURL)
	_, err := io.WriteString(h.out, line)
	return err
}

func (h *HumanWriter) WriteSummary(e SummaryEvent) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	title := fmt.Sprintf("chcrawl summary — %s", e.Seed)
	if e.Partial {
		title += h.paint(ansiYellow, " (partial — stopped early)")
	}
	rule := h.paint(ansiDim, "────────────────────────────────────────")

	fmt.Fprintln(h.out)
	fmt.Fprintln(h.out, h.paint(ansiBold, title))
	fmt.Fprintln(h.out, rule)
	fmt.Fprintf(h.out, "  pages         %d ok, %d failed\n", e.ResponsesOK, e.ResponsesFailed)
	fmt.Fprintf(h.out, "  unique URLs   %d  (%d duplicates rejected)\n", e.URLsUnique, e.DuplicatesRejected)
	fmt.Fprintf(h.out, "  forms/params  %d / %d\n", e.Forms, e.Params)
	if e.JSFiles > 0 || e.JSRoutes > 0 {
		fmt.Fprintf(h.out, "  JS files/routes  %d / %d\n", e.JSFiles, e.JSRoutes)
	}
	if e.OpenAPIEndpoints > 0 {
		fmt.Fprintf(h.out, "  OpenAPI endpoints  %d\n", e.OpenAPIEndpoints)
	}
	if e.SitemapURLs > 0 {
		fmt.Fprintf(h.out, "  sitemap seeds   %d\n", e.SitemapURLs)
	}
	if e.SourceMapsRecovered > 0 {
		fmt.Fprintf(h.out, "  source maps   %d\n", e.SourceMapsRecovered)
	}
	fmt.Fprintf(h.out, "  duration      %s\n", e.DurationHuman)
	fmt.Fprintln(h.out, rule)
	return nil
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// MultiWriter fans every event out to each of its EventWriters in order (e.g. the human-readable
// console summary and a raw JSONL -output file from the same crawl).
type MultiWriter struct {
	writers []EventWriter
}

func NewMultiWriter(writers ...EventWriter) *MultiWriter {
	return &MultiWriter{writers: writers}
}

func (m *MultiWriter) WritePage(e PageEvent) error {
	for _, w := range m.writers {
		if err := w.WritePage(e); err != nil {
			return err
		}
	}
	return nil
}

func (m *MultiWriter) WriteError(e ErrorEvent) error {
	for _, w := range m.writers {
		if err := w.WriteError(e); err != nil {
			return err
		}
	}
	return nil
}

func (m *MultiWriter) WriteSummary(e SummaryEvent) error {
	for _, w := range m.writers {
		if err := w.WriteSummary(e); err != nil {
			return err
		}
	}
	return nil
}

func (m *MultiWriter) WriteOpenAPI(e OpenAPIEvent) error {
	for _, w := range m.writers {
		if err := w.WriteOpenAPI(e); err != nil {
			return err
		}
	}
	return nil
}
