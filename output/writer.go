package output

import (
	"io"
	"sync"
)

type EventWriter interface {
	WritePage(PageEvent) error
	WriteError(ErrorEvent) error
	WriteSummary(SummaryEvent) error
	WriteOpenAPI(OpenAPIEvent) error
}

// Writer streams JSONL records to an underlying io.Writer, safe for
// concurrent use by multiple crawl workers.
type Writer struct {
	mu  sync.Mutex
	out io.Writer
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{out: w}
}

// bufPool holds *[]byte (a pointer, not a slice value) so that Put never
// itself allocates by boxing a slice header into the pool's any. 512 bytes
// covers a typical PageEvent line without regrowth.
var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 512)
		return &b
	},
}

func (w *Writer) WritePage(e PageEvent) error {
	e.Type = "page"
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	b, err := appendPageEventJSON((*bp)[:0], &e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	*bp = b
	return w.flush(b)
}

func (w *Writer) WriteError(e ErrorEvent) error {
	e.Type = "error"
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	b := append(appendErrorEventJSON((*bp)[:0], &e), '\n')
	*bp = b
	return w.flush(b)
}

func (w *Writer) WriteSummary(e SummaryEvent) error {
	e.Type = "summary"
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	b, err := appendSummaryEventJSON((*bp)[:0], &e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	*bp = b
	return w.flush(b)
}

func (w *Writer) WriteOpenAPI(e OpenAPIEvent) error {
	e.Type = "openapi"
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	b := append(appendOpenAPIEventJSON((*bp)[:0], &e), '\n')
	*bp = b
	return w.flush(b)
}

// flush is the only code that touches w.out, guarded by w.mu. Encoding
// always happens before this call, preserving the invariant that JSON
// encoding never happens inside the critical section.
func (w *Writer) flush(b []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.out.Write(b)
	return err
}
