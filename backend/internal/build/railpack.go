package build

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/MogboPython/komo-sahvah/backend/internal/logs"
)

const buildTimeout = 15 * time.Minute

// RunRailpack invokes `railpack build` on sourceDir, tagging the resulting
// image as imageName. Every line of stdout and stderr is forwarded to bc so
// the user can watch the build in real time.
//
// Railpack detects the runtime automatically (Node, Python, Go, etc.) and
// produces an optimised Docker image without a user-supplied Dockerfile.
func RunRailpack(ctx context.Context, sourceDir, imageName string, bc *logs.Broadcaster) error {
	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	bc.Systemf("=== railpack build starting ===")
	bc.Systemf("source: %s", sourceDir)
	bc.Systemf("image:  %s", imageName)

	// railpack build --name <image> <path>
	cmd := exec.CommandContext(buildCtx, "railpack", "build",
		"--name", imageName,
		sourceDir,
	)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("creating stderr pipe: %w", err)
	}

	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting railpack: %w", err)
	}

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			bc.Publish(scanner.Text(), logs.StreamStdout)
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			bc.Publish(scanner.Text(), logs.StreamStderr)
		}
	}()

	<-done
	<-done

	if err := cmd.Wait(); err != nil {
		if buildCtx.Err() != nil {
			return fmt.Errorf("railpack build timed out after %s", buildTimeout)
		}
		return fmt.Errorf("railpack build failed: %w", err)
	}

	bc.Systemf("=== railpack build complete — image: %s ===", imageName)
	return nil
}

func ImageName(deploymentID string) string {
	return "ks-image-" + strings.ToLower(deploymentID)
}
