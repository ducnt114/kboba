// Package logmux merges many log streams into one channel (fan-in), the
// way `stern` or `kubectl logs -l app=web --prefix` show the logs of
// several pods at once. It knows nothing about Kubernetes or the UI.
package logmux

import (
	"context"
	"sync"
)

// Line is one log line, or the end of a source.
type Line struct {
	Source string // e.g. the pod name
	Text   string
	// Ended marks the last message of a source: its stream closed. Err is
	// why, if it ended with an error.
	Ended bool
	Err   error
}

// Mux merges sources into Lines(). Add may be called at any time until
// Close; Close stops every forwarder and then closes Lines().
type Mux struct {
	ctx    context.Context
	cancel context.CancelFunc
	out    chan Line
	wg     sync.WaitGroup

	mu     sync.Mutex
	closed bool
	once   sync.Once
}

// New returns a Mux. Cancelling ctx has the same effect as Close.
func New(ctx context.Context) *Mux {
	ctx, cancel := context.WithCancel(ctx)
	m := &Mux{ctx: ctx, cancel: cancel, out: make(chan Line, 256)}
	go func() {
		<-ctx.Done()
		m.Close()
	}()
	return m
}

// Lines delivers the merged lines. It is closed after Close, once every
// forwarder has stopped, so a reader blocked on it is released.
func (m *Mux) Lines() <-chan Line { return m.out }

// Add forwards lines from one source until it is closed or the Mux stops.
// When lines closes, a Line{Ended: true} is sent, with the error from errs
// if there is one (errs may be nil). Add never blocks.
func (m *Mux) Add(source string, lines <-chan string, errs <-chan error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.wg.Add(1) // under mu, so Close can't start waiting before we're counted
	go func() {
		defer m.wg.Done()
		for {
			select {
			case text, ok := <-lines:
				if !ok {
					end := Line{Source: source, Ended: true}
					select {
					case end.Err = <-errs: // a nil errs channel just never fires
					default:
					}
					m.send(end)
					return
				}
				if !m.send(Line{Source: source, Text: text}) {
					return
				}
			case <-m.ctx.Done():
				return
			}
		}
	}()
}

// send delivers l unless the Mux is stopping; it reports whether it did.
func (m *Mux) send(l Line) bool {
	select {
	case m.out <- l:
		return true
	case <-m.ctx.Done():
		return false
	}
}

// Close stops all forwarders, waits for them, then closes Lines(). It is
// safe to call more than once and from any goroutine; it does not block.
func (m *Mux) Close() {
	m.once.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		m.cancel()
		go func() {
			m.wg.Wait()
			close(m.out)
		}()
	})
}
