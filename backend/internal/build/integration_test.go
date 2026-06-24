package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MogboPython/komo-sahvah/backend/internal/logs"
)

func TestIntegrationRunRailpackBuildOnATestProject(t *testing.T) {
	dockerAvailable(t)
	railpackAvailable(t)
	setBuildkitHost(t)

	buildDir, _ := prepareTestProjectDir(t)
	imageName := "ks-image-test-project-" + strings.ToLower(t.Name())

	t.Cleanup(func() {
		_ = exec.Command("docker", "rmi", "-f", imageName).Run()
	})

	bc := logs.NewBroadcaster()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	if err := RunRailpack(ctx, buildDir, imageName, bc); err != nil {
		lines := collectBroadcasterLines(t, bc, 500*time.Millisecond)
		t.Fatalf("RunRailpack() error = %v\nlogs:\n%s", err, strings.Join(lineTexts(lines), "\n"))
	}

	if out, err := exec.Command("docker", "image", "inspect", imageName, "--format", "{{.Id}}").CombinedOutput(); err != nil {
		t.Fatalf("docker image inspect %q: %v\n%s", imageName, err, out)
	}

	lines := collectBroadcasterLines(t, bc, 500*time.Millisecond)
	logText := strings.Join(lineTexts(lines), "\n")
	if !strings.Contains(logText, "railpack build complete") {
		t.Fatalf("expected build completion log, got:\n%s", logText)
	}
}

func dockerAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not available: %v", err)
	}
}

func railpackAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("railpack"); err != nil {
		t.Skip("railpack not installed")
	}
}

func setBuildkitHost(t *testing.T) {
	t.Helper()

	localSocket := "docker-container://buildkit"
	t.Setenv("BUILDKIT_HOST", localSocket)
}

func repoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..")
}

func testProjectDir(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(repoRoot(t), "test-project")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("test-project not available: %v", err)
	}
	return dir
}

func prepareTestProjectDir(t *testing.T) (buildDir string, cleanup func()) {
	t.Helper()

	source := testProjectDir(t)
	buildDir = t.TempDir()

	copyDirContents(t, source, buildDir)

	return buildDir, func() {}
}

func copyDirContents(t *testing.T, src, dst string) {
	t.Helper()

	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(rel, ".git"+string(filepath.Separator)) || rel == ".git" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "node_modules" || strings.HasPrefix(rel, "node_modules"+string(filepath.Separator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == ".next" || strings.HasPrefix(rel, ".next"+string(filepath.Separator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
	if err != nil {
		t.Fatalf("copyDirContents(%q -> %q): %v", src, dst, err)
	}
}
