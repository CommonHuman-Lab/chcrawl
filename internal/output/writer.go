package output

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
)

// Writer streams JSONL records to an underlying io.Writer, safe for
// concurrent use by multiple crawl workers.
type Writer struct {
	mu  sync.Mutex
	out io.Writer
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{out: w}
}

func (w *Writer) WritePage(e PageEvent) error {
	e.Type = "page"
	return w.write(e)
}

func (w *Writer) WriteError(e ErrorEvent) error {
	e.Type = "error"
	return w.write(e)
}

func (w *Writer) WriteSummary(e SummaryEvent) error {
	e.Type = "summary"
	return w.write(e)
}

func (w *Writer) WriteOpenAPI(e OpenAPIEvent) error {
	e.Type = "openapi"
	return w.write(e)
}

func (w *Writer) write(v interface{}) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.out.Write(buf.Bytes())
	return err
}
