package exec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/logutil"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/ws"
)

const (
	sinkFlushEvery  = 250 * time.Millisecond
	sinkFlushChunks = 64
	sinkFlushBytes  = 256 * 1024
)

// LogSink assigns monotonically increasing seq numbers, fans chunks out to
// the per-run websocket topic at once, and stores them in batches so a
// chatty job costs one insert per flush rather than one per write. Close
// flushes what is pending; storage uses its own context so the last lines
// of a canceled run still land.
type LogSink struct {
	store  *store.Store
	hub    *ws.Hub
	runID  uuid.UUID
	seq    atomic.Int64
	capped atomic.Bool
	redact *logutil.Redactor

	mu       sync.Mutex
	buf      []store.LogChunk
	bufBytes int
	closed   bool
	stop     chan struct{}
	done     chan struct{}
}

// NewLogSink builds a chunk-storing sink for one run. hub may be nil. The
// context only bounds how long a flush may wait on the database.
func NewLogSink(ctx context.Context, st *store.Store, runID uuid.UUID, hub *ws.Hub) *LogSink {
	s := &LogSink{
		store: st,
		runID: runID,
		hub:   hub,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go s.loop()
	return s
}

func (s *LogSink) loop() {
	defer close(s.done)
	t := time.NewTicker(sinkFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			s.flush()
			return
		case <-t.C:
			s.flush()
		}
	}
}

// Redact masks the given secret values in everything written afterwards.
func (s *LogSink) Redact(values []string) {
	s.redact = logutil.NewRedactor(values)
}

// WriteChunk assigns a sequence number, publishes, and queues for storage.
func (s *LogSink) WriteChunk(stream, data string) {
	if s.capped.Load() {
		return
	}
	data = s.redact.Apply(data)
	seq := s.seq.Add(1)
	s.Publish(seq, stream, data)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.buf = append(s.buf, store.LogChunk{Seq: seq, Stream: stream, Data: data})
	s.bufBytes += len(data)
	full := len(s.buf) >= sinkFlushChunks || s.bufBytes >= sinkFlushBytes
	s.mu.Unlock()
	if full {
		s.flush()
	}
}

// flush writes the pending chunks. Callers never hold mu.
func (s *LogSink) flush() {
	s.mu.Lock()
	batch := s.buf
	s.buf = nil
	s.bufBytes = 0
	s.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := s.store.AppendLogs(ctx, s.runID, batch); err != nil {
		if errors.Is(err, store.ErrLogCapped) {
			if s.capped.CompareAndSwap(false, true) {
				s.Publish(s.seq.Add(1), "system", "[kairos] log truncated: per-run cap reached")
			}
			return
		}
		slog.Warn("append logs", "run", s.runID, "chunks", len(batch), "err", err)
	}
}

// Close flushes pending chunks and stops the background writer. Writes
// after Close are published but not stored.
func (s *LogSink) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.stop)
	<-s.done
}

// Publish fans a chunk to subscribers without persisting (agent-upload path).
func (s *LogSink) Publish(seq int64, stream, data string) {
	if s.hub != nil {
		s.hub.Publish(ws.RunTopic(s.runID.String()), "log", map[string]any{
			"seq":    seq,
			"stream": stream,
			"data":   data,
		})
	}
}

// System logs a system-stamped message.
func (s *LogSink) System(format string, args ...any) {
	s.WriteChunk("system", fmt.Sprintf(format, args...))
}
