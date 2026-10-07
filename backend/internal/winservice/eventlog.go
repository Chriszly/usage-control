package winservice

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
)

// eventWriter is the part of the Windows event log a program writes to.
type eventWriter interface {
	Warning(eid uint32, msg string) error
	Error(eid uint32, msg string) error
}

// eventLogHandler passes every record on to next, and writes warnings and
// errors to the event log too: the message, then a line with its
// attributes as key=value text, without the time and level, which the event log keeps
// itself.
type eventLogHandler struct {
	next slog.Handler
	log  eventWriter
	// text formats a record into buf; mu guards both, which every handler
	// made from this one by WithAttrs and WithGroup shares.
	text slog.Handler
	buf  *bytes.Buffer
	mu   *sync.Mutex
}

func newEventLogHandler(next slog.Handler, log eventWriter) *eventLogHandler {
	buf := &bytes.Buffer{}
	text := slog.NewTextHandler(buf, &slog.HandlerOptions{
		Level: slog.LevelWarn,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey || a.Key == slog.MessageKey) {
				return slog.Attr{}
			}
			return a
		},
	})
	return &eventLogHandler{next: next, log: log, text: text, buf: buf, mu: &sync.Mutex{}}
}

func (h *eventLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn || h.next.Enabled(ctx, level)
}

func (h *eventLogHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		h.mu.Lock()
		h.buf.Reset()
		if h.text.Handle(ctx, r) == nil {
			message := strings.TrimSpace(r.Message + "\r\n" + h.buf.String())
			if r.Level >= slog.LevelError {
				_ = h.log.Error(1, message)
			} else {
				_ = h.log.Warning(1, message)
			}
		}
		h.mu.Unlock()
	}
	if !h.next.Enabled(ctx, r.Level) {
		return nil
	}
	return h.next.Handle(ctx, r)
}

func (h *eventLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.next, c.text = h.next.WithAttrs(attrs), h.text.WithAttrs(attrs)
	return &c
}

func (h *eventLogHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.next, c.text = h.next.WithGroup(name), h.text.WithGroup(name)
	return &c
}
