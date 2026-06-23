package logs

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// thread-safe, history-replaying log broadcaster.
// Late-joining subscribers would automatically receive the full history before they
// start receiving live lines, so an SSE client that connects mid-build never
// misses output.

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
	StreamSystem Stream = "system"
)

// single log entry.
type Line struct {
	Text   string    `json:"text"`
	Stream Stream    `json:"stream"`
	Time   time.Time `json:"time"`
}

// carries the final outcome of a deployment pipeline.
type DoneEvent struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// fans out log lines to any number of concurrent subscribers.
type Broadcaster struct {
	mu     sync.Mutex
	lines  []Line
	notify chan struct{} // closed when a new line is appended; then replaced
	done   bool
	final  *DoneEvent
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		notify: make(chan struct{}),
	}
}

func (b *Broadcaster) Publish(text string, stream Stream) {
	b.mu.Lock()
	if b.done {
		b.mu.Unlock()
		return
	}
	b.lines = append(b.lines, Line{
		Text:   text,
		Stream: stream,
		Time:   time.Now(),
	})
	// Replace the notify channel and close the old one to wake all waiters
	old := b.notify
	b.notify = make(chan struct{})
	b.mu.Unlock()

	close(old)
}

// convenience wrapper that publishes a formatted system message
func (b *Broadcaster) Systemf(format string, args ...any) {
	b.Publish(fmt.Sprintf(format, args...), StreamSystem)
}

// marks the broadcaster as done, wakes all subscribers for a final drain,
// and stores the terminal event, further Publish calls are silently dropped.
func (b *Broadcaster) Close(final DoneEvent) {
	b.mu.Lock()
	if b.done {
		b.mu.Unlock()
		return
	}
	b.done = true
	b.final = &final
	old := b.notify
	b.notify = make(chan struct{})
	b.mu.Unlock()

	close(old)
}

func (b *Broadcaster) Final() (*DoneEvent, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.final, b.done
}

func (b *Broadcaster) Subscribe(ctx context.Context, fromOffset int) (<-chan Line, <-chan DoneEvent) {
	lineCh := make(chan Line, 64)
	doneCh := make(chan DoneEvent, 1)

	go func() {
		defer close(lineCh)
		offset := fromOffset

		for {
			b.mu.Lock()
			newLines := b.lines[offset:]
			notify := b.notify
			isDone := b.done
			final := b.final
			b.mu.Unlock()

			// Deliver buffered lines to the subscriber.
			for _, l := range newLines {
				select {
				case lineCh <- l:
				case <-ctx.Done():
					return
				}
			}
			offset += len(newLines)

			if isDone {
				if final != nil {
					doneCh <- *final
				}
				return
			}

			select {
			case <-notify:
			case <-ctx.Done():
				return
			}
		}
	}()

	return lineCh, doneCh
}

// LineCount returns the number of lines currently in the history.
func (b *Broadcaster) LineCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}
