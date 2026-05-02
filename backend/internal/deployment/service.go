package deployment

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// TODO: keep work dir in a more permanent location.
	baseWorkDir    = "/tmp/deployments"
	cloneTimeout   = 3 * time.Minute
	maxUploadBytes = 500 << 20
)

type ErrRepoInaccessible struct {
	URL    string
	Detail string
}

func (e *ErrRepoInaccessible) Error() string {
	return fmt.Sprintf("repository %q is inaccessible: %s", e.URL, e.Detail)
}

func PrepareWorkDir(deploymentID string) (string, error) {
	dir := filepath.Join(baseWorkDir, deploymentID)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating work dir: %w", err)
	}

	return dir, nil
}

func CloneRepo(ctx context.Context, repoURL, workDir string) error {
	cloneCtx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	// --depth 1 gets the latest snapshot of the repository.
	cmd := exec.CommandContext(cloneCtx, "git", "clone", "--depth", "1", repoURL, workDir)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard

	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=echo",
	)

	err := cmd.Run()
	if err == nil {
		return nil
	}

	if errors.Is(cloneCtx.Err(), context.DeadlineExceeded) {
		return &ErrRepoInaccessible{
			URL:    repoURL,
			Detail: "clone timed out after 3 minutes — the repository may be very large or unreachable",
		}
	}

	stderrStr := stderr.String()

	switch {
	case containsAny(stderrStr,
		"Repository not found",
		"not found",
		"does not exist",
		"Could not read from remote",
		"could not read Username",
		"Authentication failed",
		"403",
		"404",
	):
		// TODO: work on them adding deployment key to the repo settings.
		return &ErrRepoInaccessible{
			URL: repoURL,
			Detail: "the repository could not be accessed. " +
				"Make sure it is public, or grant us access by adding our deploy key to the repo settings.",
		}

	case containsAny(stderrStr, "invalid url", "not a git repository"):
		return &ErrRepoInaccessible{
			URL:    repoURL,
			Detail: "the URL does not point to a valid git repository",
		}

	default:
		return fmt.Errorf("git clone failed: %s", strings.TrimSpace(stderrStr))
	}
}

func ExtractZip(part multipart.File, destDir string) error {
	// TODO: work on the large files in production.
	// Buffer the entire upload so we can pass its size to zip.NewReader.
	// For large files in production you'd stream to a temp file instead.
	data, err := io.ReadAll(io.LimitReader(part, maxUploadBytes+1))
	if err != nil {
		return fmt.Errorf("reading upload: %w", err)
	}
	if int64(len(data)) > maxUploadBytes {
		return fmt.Errorf("upload exceeds the %d MB limit", maxUploadBytes>>20)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("opening zip archive: %w", err)
	}

	for _, f := range zr.File {
		if err := extractZipEntry(f, destDir); err != nil {
			return err
		}
	}
	return nil
}

func extractZipEntry(f *zip.File, destDir string) error {
	name := stripTopLevelDir(f.Name)
	if name == "" || name == "." {
		return nil
	}

	target := filepath.Join(destDir, name)
	if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), filepath.Clean(destDir)+string(os.PathSeparator)) {
		return fmt.Errorf("zip entry %q would escape destination directory (zip-slip attack)", f.Name)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
	if err != nil {
		return fmt.Errorf("creating file %q: %w", target, err)
	}
	defer dst.Close()

	src, err := f.Open()
	if err != nil {
		return fmt.Errorf("opening zip entry %q: %w", f.Name, err)
	}
	defer src.Close()

	if _, err = io.Copy(dst, src); err != nil {
		return fmt.Errorf("writing file %q: %w", target, err)
	}
	return nil
}

func stripTopLevelDir(name string) string {
	name = filepath.ToSlash(name)
	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return name
}

func containsAny(s string, subs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}
