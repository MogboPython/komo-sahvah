package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MogboPython/komo-sahvah/backend/internal/logs"
)

func writeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	content := "#!/bin/sh\n" + body
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

func prependPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func isolatePath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir)
}

func collectBroadcasterLines(t *testing.T, bc *logs.Broadcaster, idle time.Duration) []logs.Line {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lineCh, _ := bc.Subscribe(ctx, 0)
	return collectLinesFromChannel(t, lineCh, idle)
}

func collectLinesFromChannel(t *testing.T, ch <-chan logs.Line, idle time.Duration) []logs.Line {
	t.Helper()

	var lines []logs.Line
	idleTimer := time.NewTimer(idle)
	defer idleTimer.Stop()

	for {
		select {
		case line, ok := <-ch:
			if !ok {
				return lines
			}
			lines = append(lines, line)
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(idle)
		case <-idleTimer.C:
			return lines
		}
	}
}

func lineTexts(lines []logs.Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}

func hasLineWithStream(lines []logs.Line, stream logs.Stream) bool {
	for _, l := range lines {
		if l.Stream == stream {
			return true
		}
	}
	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..")
}

func tempFantWebsiteDir(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(repoRoot(t), "temp-fant-website")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("temp-fant-website not available: %v", err)
	}
	return dir
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()

	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", dst, err)
	}
}
