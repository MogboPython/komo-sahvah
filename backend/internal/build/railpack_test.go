package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MogboPython/komo-sahvah/backend/internal/logs"
)

func TestImageName(t *testing.T) {
	tests := []struct {
		name         string
		deploymentID string
		want         string
	}{
		{name: "lowercase uuid", deploymentID: "ABC-123", want: "ks-image-abc-123"},
		{name: "already lowercase", deploymentID: "deploy-1", want: "ks-image-deploy-1"},
		{name: "mixed case", deploymentID: "Deploy-X", want: "ks-image-deploy-x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ImageName(tt.deploymentID); got != tt.want {
				t.Fatalf("ImageName(%q) = %q, want %q", tt.deploymentID, got, tt.want)
			}
		})
	}
}

func TestRunRailpack(t *testing.T) {
	sourceDir := t.TempDir()

	tests := []struct {
		name      string
		script    string
		setupPath func(t *testing.T, dir string)
		wantErr   string
		wantOut   string
		wantErrLn string
	}{
		{
			name: "success forwards stdout stderr and completes",
			script: `
			echo "step 1 complete"
			echo "warning: cache miss" >&2
			exit 0
			`,
			wantOut:   "step 1 complete",
			wantErrLn: "warning: cache miss",
		},
		{
			name: "build failure returns wrapped error",
			script: `
			echo "compile error" >&2
			exit 1
			`,
			wantErr: "railpack build failed",
		},
		{
			name: "missing railpack binary",
			setupPath: func(t *testing.T, dir string) {
				isolatePath(t, dir)
			},
			wantErr: "starting railpack",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := t.TempDir()
			if tt.setupPath != nil {
				tt.setupPath(t, binDir)
			} else {
				writeExecutable(t, binDir, "railpack", tt.script)
				prependPath(t, binDir)
			}

			bc := logs.NewBroadcaster()
			err := RunRailpack(context.Background(), sourceDir, "ks-image-test", bc)
			lines := collectBroadcasterLines(t, bc, 200*time.Millisecond)

			if tt.wantErr == "" && err != nil {
				t.Fatalf("RunRailpack() error = %v, want nil", err)
			}
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("RunRailpack() error = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("RunRailpack() error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}

			if !hasLineWithStream(lines, logs.StreamSystem) {
				t.Fatal("expected system log lines")
			}
			if !hasLineWithStream(lines, logs.StreamStdout) {
				t.Fatal("expected stdout log lines")
			}
			if !hasLineWithStream(lines, logs.StreamStderr) {
				t.Fatal("expected stderr log lines")
			}

			texts := strings.Join(lineTexts(lines), "\n")
			if tt.wantOut != "" && !strings.Contains(texts, tt.wantOut) {
				t.Fatalf("logs missing stdout %q:\n%s", tt.wantOut, texts)
			}
			if tt.wantErrLn != "" && !strings.Contains(texts, tt.wantErrLn) {
				t.Fatalf("logs missing stderr %q:\n%s", tt.wantErrLn, texts)
			}
			if !strings.Contains(texts, "railpack build complete") {
				t.Fatalf("logs missing completion message:\n%s", texts)
			}
		})
	}
}

func TestRunRailpackHonoursContextCancel(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "railpack", `
	sleep 5
	exit 0
	`)
	prependPath(t, binDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	bc := logs.NewBroadcaster()
	err := RunRailpack(ctx, t.TempDir(), "ks-image-test", bc)
	if err == nil {
		t.Fatal("RunRailpack() error = nil, want context cancellation error")
	}
}

func TestRunRailpackUsesExpectedArguments(t *testing.T) {
	binDir := t.TempDir()
	sourceDir := t.TempDir()
	argsFile := filepath.Join(binDir, "args.log")

	writeExecutable(t, binDir, "railpack", `
	printf '%s\n' "$@" > "`+argsFile+`"
	exit 0
	`)
	prependPath(t, binDir)

	bc := logs.NewBroadcaster()
	imageName := "ks-image-args-test"
	if err := RunRailpack(context.Background(), sourceDir, imageName, bc); err != nil {
		t.Fatalf("RunRailpack() error = %v", err)
	}

	args := readFileTrim(t, argsFile)
	want := []string{"build", "--name", imageName, sourceDir}
	for _, part := range want {
		if !strings.Contains(args, part) {
			t.Fatalf("railpack args = %q, want to contain %q", args, part)
		}
	}
}

func readFileTrim(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return strings.TrimSpace(string(data))
}
