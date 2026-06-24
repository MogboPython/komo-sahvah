package build

import (
	"context"
	"strings"
	"testing"
)

func TestContainerName(t *testing.T) {
	tests := []struct {
		name         string
		deploymentID string
		want         string
	}{
		{name: "simple id", deploymentID: "abc123", want: "ks-container-abc123"},
		{name: "uuid", deploymentID: "550e8400-e29b-41d4-a716-446655440000", want: "ks-container-550e8400-e29b-41d4-a716-446655440000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerName(tt.deploymentID); got != tt.want {
				t.Fatalf("containerName(%q) = %q, want %q", tt.deploymentID, got, tt.want)
			}
		})
	}
}

func TestStopContainer(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		wantErr string
	}{
		{
			name: "success stops and removes container",
			script: `
			case "$1" in
			stop) exit 0 ;;
			rm) exit 0 ;;
			*) echo "unexpected: $*" >&2; exit 1 ;;
			esac
			`,
		},
		{
			name: "stop failure returns error",
			script: `
			case "$1" in
			stop) echo "container not running" >&2; exit 1 ;;
			rm) exit 0 ;;
			*) exit 1 ;;
			esac
			`,
			wantErr: "docker stop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := t.TempDir()
			writeExecutable(t, binDir, "docker", tt.script)
			prependPath(t, binDir)

			err := StopContainer(context.Background(), "deploy-test")
			if tt.wantErr == "" && err != nil {
				t.Fatalf("StopContainer() error = %v, want nil", err)
			}
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("StopContainer() error = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("StopContainer() error = %q, want substring %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestBuildDockerImage(t *testing.T) {
	t.Run("docker build failure returns error", func(t *testing.T) {
		binDir := t.TempDir()
		writeExecutable(t, binDir, "docker", `
		echo "build failed" >&2
		exit 1
		`)
		prependPath(t, binDir)

		err := BuildDockerImage(context.Background(), "ks-image-local")
		if err == nil {
			t.Fatal("BuildDockerImage() error = nil, want build failure")
		}
	})
}
