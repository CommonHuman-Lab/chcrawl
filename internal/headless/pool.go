package headless

import (
	"context"

	"github.com/go-rod/rod"
)

// pagePool bounds concurrent browser pages in use. Acquire opens a fresh
// page each time and Release closes it (no reuse) to avoid state leaking
// between navigations. open/close are injectable so tests don't need a
// real browser.
type pagePool struct {
	open  func(ctx context.Context) (*rod.Page, error)
	close func(*rod.Page)
	sem   chan struct{}
}

func newPagePool(size int, open func(ctx context.Context) (*rod.Page, error), closeFn func(*rod.Page)) *pagePool {
	return &pagePool{open: open, close: closeFn, sem: make(chan struct{}, size)}
}

func (p *pagePool) Acquire(ctx context.Context) (*rod.Page, error) {
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	page, err := p.open(ctx)
	if err != nil {
		<-p.sem
		return nil, err
	}
	return page, nil
}

func (p *pagePool) Release(page *rod.Page) {
	if page != nil {
		p.close(page)
	}
	<-p.sem
}
