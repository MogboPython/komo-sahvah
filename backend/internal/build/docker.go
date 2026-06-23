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

const (
	startTimeout     = 30 * time.Second
	logStreamTimeout = 5 * time.Minute
)

// runtime details of a successfully started container.
type ContainerInfo struct {
	ID        string
	IP        string // IP on the shared Docker network
	ImageName string
}

func BuildDockerImage(ctx context.Context, imageName string) error {
	cmd := exec.CommandContext(ctx, "docker", "build", "-t", imageName, ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func RunContainer(
	ctx context.Context,
	deploymentID string,
	imageName string,
	network string,
	bc *logs.Broadcaster,
) (*ContainerInfo, error) {
	containerName := containerName(deploymentID)

	bc.Systemf("=== starting container ===")
	bc.Systemf("image:     %s", imageName)
	bc.Systemf("container: %s", containerName)
	bc.Systemf("network:   %s", network)

	// Remove any stale container with the same name (e.g. from a previous failed deployment).
	_ = removeContainer(containerName)

	runArgs := []string{
		"run", "-d",
		"--name", containerName,
		"--network", network,
		"--restart", "unless-stopped",
		"--memory", "512m",
		"--memory-swap", "512m",
		imageName,
	}

	runCmd := exec.CommandContext(ctx, "docker", runArgs...)
	runCmd.Env = os.Environ()

	out, err := runCmd.Output()
	if err != nil {
		exitErr, _ := err.(*exec.ExitError)
		detail := ""
		if exitErr != nil {
			detail = strings.TrimSpace(string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("docker run failed: %w\n%s", err, detail)
	}

	containerID := strings.TrimSpace(string(out))
	bc.Systemf("container id: %s", shortID(containerID))

	// wait for the container to actually reach a running state.
	if err := waitForRunning(ctx, containerID, bc); err != nil {
		return nil, err
	}

	ip, err := containerIP(ctx, containerID, network)
	if err != nil {
		bc.Systemf("warning: could not resolve container IP: %s", err)
		ip = containerName
	}

	info := &ContainerInfo{
		ID:        containerID,
		IP:        ip,
		ImageName: imageName,
	}

	bc.Systemf("container running — ip: %s", ip)
	bc.Systemf("=== deploy complete ===")

	// Stream container logs in a separate goroutine so the SSE client can see
	// runtime output. We stop after logStreamTimeout; the container keeps running.
	go streamContainerLogs(ctx, containerID, bc)

	return info, nil
}

func waitForRunning(ctx context.Context, containerID string, bc *logs.Broadcaster) error {
	bc.Systemf("waiting for container to start...")

	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		status, err := inspectField(ctx, containerID, "{{.State.Status}}")
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		switch status {
		case "running":
			return nil
		case "exited", "dead":
			// grab the exit code and last few log lines for a useful error.
			exitCode, _ := inspectField(ctx, containerID, "{{.State.ExitCode}}")
			tail := containerLogTail(containerID, 20)
			return fmt.Errorf(
				"container exited immediately (code %s)\n--- last logs ---\n%s",
				exitCode, tail,
			)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("container did not reach running state within %s", startTimeout)
}

func streamContainerLogs(ctx context.Context, containerID string, bc *logs.Broadcaster) {
	streamCtx, cancel := context.WithTimeout(ctx, logStreamTimeout)
	defer cancel()

	cmd := exec.CommandContext(streamCtx, "docker", "logs", "--follow", "--timestamps", containerID)
	cmd.Env = os.Environ()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		bc.Systemf("log stream error: %s", err)
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		bc.Systemf("log stream error: %s", err)
		return
	}

	if err := cmd.Start(); err != nil {
		bc.Systemf("could not start log stream: %s", err)
		return
	}

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			bc.Publish("[container] "+scanner.Text(), logs.StreamStdout)
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			bc.Publish("[container] "+scanner.Text(), logs.StreamStderr)
		}
	}()

	<-done
	<-done
	_ = cmd.Wait()
}

func StopContainer(ctx context.Context, deploymentID string) error {
	name := containerName(deploymentID)

	stopCmd := exec.CommandContext(ctx, "docker", "stop", name)
	stopCmd.Env = os.Environ()
	if out, err := stopCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker stop %s: %w\n%s", name, err, out)
	}

	return removeContainer(name)
}

func containerName(deploymentID string) string {
	return "ks-container-" + deploymentID
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// TODO: what if container fails to be removed?
func removeContainer(containerName string) error {
	cmd := exec.Command("docker", "rm", "-f", containerName)
	cmd.Env = os.Environ()
	_, err := cmd.CombinedOutput()
	return err
}

func inspectField(ctx context.Context, containerID, template string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect",
		"--format", template,
		containerID,
	)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func containerIP(ctx context.Context, containerID, network string) (string, error) {
	template := fmt.Sprintf(`{{.NetworkSettings.Networks.%s.IPAddress}}`, network)
	ip, err := inspectField(ctx, containerID, template)
	if err != nil {
		return "", err
	}
	if ip == "" {
		return "", fmt.Errorf("container not attached to network %q", network)
	}
	return ip, nil
}

func containerLogTail(containerID string, n int) string {
	cmd := exec.Command("docker", "logs", "--tail", fmt.Sprintf("%d", n), containerID)
	cmd.Env = os.Environ()
	out, _ := cmd.CombinedOutput()
	return strings.TrimSpace(string(out))
}
