package logs

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewBroadcaster(t *testing.T) {
	bc := NewBroadcaster()
	if bc == nil {
		t.Fatal("NewBroadcaster returned nil")
	}
	if bc.LineCount() != 0 {
		t.Fatalf("LineCount() = %d, want 0", bc.LineCount())
	}
	final, done := bc.Final()
	if done {
		t.Fatal("new broadcaster should not be done")
	}
	if final != nil {
		t.Fatalf("Final() = %v, want nil", final)
	}
}

func TestBroadcasterPublish(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		stream Stream
	}{
		{"stdout", "hello stdout", StreamStdout},
		{"stderr", "hello stderr", StreamStderr},
		{"system", "hello system", StreamSystem},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bc := NewBroadcaster()
			bc.Publish(tt.text, tt.stream)

			if bc.LineCount() != 1 {
				t.Fatalf("LineCount() = %d, want 1", bc.LineCount())
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			lineCh, _ := bc.Subscribe(ctx, 0)
			line, ok := <-lineCh
			if !ok {
				t.Fatal("line channel closed before delivering published line")
			}
			if line.Text != tt.text {
				t.Fatalf("line.Text = %q, want %q", line.Text, tt.text)
			}
			if line.Stream != tt.stream {
				t.Fatalf("line.Stream = %q, want %q", line.Stream, tt.stream)
			}
			if line.Time.IsZero() {
				t.Fatal("line.Time should be set")
			}
		})
	}
}

func TestBroadcasterSystemf(t *testing.T) {
	bc := NewBroadcaster()
	bc.Systemf("deploy %s started", "abc123")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	lineCh, _ := bc.Subscribe(ctx, 0)
	line := <-lineCh

	if line.Text != "deploy abc123 started" {
		t.Fatalf("line.Text = %q, want %q", line.Text, "deploy abc123 started")
	}
	if line.Stream != StreamSystem {
		t.Fatalf("line.Stream = %q, want %q", line.Stream, StreamSystem)
	}
}

func TestBroadcasterPublishAfterClose(t *testing.T) {
	bc := NewBroadcaster()
	bc.Publish("before close", StreamStdout)
	bc.Close(DoneEvent{Status: "success"})

	bc.Publish("after close", StreamStdout)

	if bc.LineCount() != 1 {
		t.Fatalf("LineCount() = %d, want 1 (post-close publish ignored)", bc.LineCount())
	}
}

func TestBroadcasterClose(t *testing.T) {
	tests := []struct {
		name  string
		final DoneEvent
	}{
		{
			name:  "success",
			final: DoneEvent{Status: "success"},
		},
		{
			name:  "failure with error",
			final: DoneEvent{Status: "failed", Error: "build exited 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bc := NewBroadcaster()
			bc.Close(tt.final)

			final, done := bc.Final()
			if !done {
				t.Fatal("Final() done = false, want true")
			}
			if final == nil {
				t.Fatal("Final() returned nil event")
			}
			if *final != tt.final {
				t.Fatalf("Final() = %+v, want %+v", *final, tt.final)
			}
		})
	}
}

func TestBroadcasterCloseIdempotent(t *testing.T) {
	bc := NewBroadcaster()
	first := DoneEvent{Status: "success"}
	second := DoneEvent{Status: "failed", Error: "should be ignored"}

	bc.Close(first)
	bc.Close(second)

	final, done := bc.Final()
	if !done {
		t.Fatal("Final() done = false, want true")
	}
	if *final != first {
		t.Fatalf("Final() = %+v, want %+v (second Close ignored)", *final, first)
	}
}

func TestBroadcasterSubscribeHistoryReplay(t *testing.T) {
	bc := NewBroadcaster()
	bc.Publish("line 1", StreamStdout)
	bc.Publish("line 2", StreamStderr)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	lineCh, doneCh := bc.Subscribe(ctx, 0)

	got := collectLines(t, lineCh, 2, time.Second)
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(got), got)
	}
	if got[0].Text != "line 1" || got[1].Text != "line 2" {
		t.Fatalf("unexpected replay order: %+v", got)
	}

	select {
	case _, open := <-doneCh:
		if !open {
			t.Fatal("done channel closed before Close()")
		}
	case <-time.After(50 * time.Millisecond):
		// expected: still open until Close
	}
}

func TestBroadcasterSubscribeFromOffset(t *testing.T) {
	tests := []struct {
		name       string
		published  []string
		fromOffset int
		want       []string
	}{
		{
			name:       "skip first line",
			published:  []string{"a", "b", "c"},
			fromOffset: 1,
			want:       []string{"b", "c"},
		},
		{
			name:       "start at end",
			published:  []string{"a", "b"},
			fromOffset: 2,
			want:       nil,
		},
		{
			name:       "from beginning",
			published:  []string{"only"},
			fromOffset: 0,
			want:       []string{"only"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bc := NewBroadcaster()
			for _, text := range tt.published {
				bc.Publish(text, StreamStdout)
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			lineCh, _ := bc.Subscribe(ctx, tt.fromOffset)
			got := collectLines(t, lineCh, len(tt.want)+1, 100*time.Millisecond)

			if len(got) != len(tt.want) {
				t.Fatalf("got %d lines %+v, want %d %+v", len(got), textsOf(got), len(tt.want), tt.want)
			}
			for i, want := range tt.want {
				if got[i].Text != want {
					t.Fatalf("line[%d].Text = %q, want %q", i, got[i].Text, want)
				}
			}
		})
	}
}

func TestBroadcasterSubscribeLiveUpdates(t *testing.T) {
	bc := NewBroadcaster()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	lineCh, _ := bc.Subscribe(ctx, 0)

	bc.Publish("live line", StreamStdout)

	select {
	case line := <-lineCh:
		if line.Text != "live line" {
			t.Fatalf("line.Text = %q, want %q", line.Text, "live line")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live line")
	}
}

func TestBroadcasterSubscribeDoneEvent(t *testing.T) {
	tests := []struct {
		name  string
		final DoneEvent
	}{
		{
			name:  "success",
			final: DoneEvent{Status: "success"},
		},
		{
			name:  "failure",
			final: DoneEvent{Status: "failed", Error: "timeout"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bc := NewBroadcaster()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			_, doneCh := bc.Subscribe(ctx, 0)
			bc.Close(tt.final)

			select {
			case ev := <-doneCh:
				if ev != tt.final {
					t.Fatalf("done event = %+v, want %+v", ev, tt.final)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for done event")
			}
		})
	}
}

func TestBroadcasterSubscribeContextCancel(t *testing.T) {
	bc := NewBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())

	lineCh, doneCh := bc.Subscribe(ctx, 0)
	cancel()

	waitForChannelClose(t, lineCh, time.Second)

	select {
	case ev, ok := <-doneCh:
		if ok {
			t.Fatalf("unexpected done event after context cancel: %+v", ev)
		}
	case <-time.After(50 * time.Millisecond):
		// doneCh stays open until Close(); no event should be sent on cancel.
	}
}

func TestBroadcasterSubscribeConcurrentPublish(t *testing.T) {
	bc := NewBroadcaster()
	const n = 50

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lineCh, _ := bc.Subscribe(ctx, 0)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			bc.Publish("line", StreamStdout)
			_ = i
		}(i)
	}
	wg.Wait()

	got := collectLines(t, lineCh, n, 2*time.Second)
	if len(got) != n {
		t.Fatalf("got %d lines, want %d", len(got), n)
	}
	if bc.LineCount() != n {
		t.Fatalf("LineCount() = %d, want %d", bc.LineCount(), n)
	}
}

func collectLines(t *testing.T, ch <-chan Line, max int, idle time.Duration) []Line {
	t.Helper()

	var lines []Line
	idleTimer := time.NewTimer(idle)
	defer idleTimer.Stop()

	for len(lines) < max {
		select {
		case line, ok := <-ch:
			if !ok {
				return lines
			}
			lines = append(lines, line)
			if !idleTimer.Stop() {
				<-idleTimer.C
			}
			idleTimer.Reset(idle)
		case <-idleTimer.C:
			return lines
		}
	}
	return lines
}

func textsOf(lines []Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}

func waitForChannelClose[T any](t *testing.T, ch <-chan T, timeout time.Duration) {
	t.Helper()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed")
		}
	case <-time.After(timeout):
		t.Fatal("timed out waiting for channel close")
	}
}
