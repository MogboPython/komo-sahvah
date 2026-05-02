package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/MogboPython/komo-sahvah/backend/internal/repository"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Handler struct {
	deployRepo repository.DeploymentRepository
	logger     *slog.Logger
}

func NewHandler(deployRepo repository.DeploymentRepository, logger *slog.Logger) *Handler {
	return &Handler{deployRepo: deployRepo, logger: logger}
}

type githubRequest struct {
	GithubURL string `json:"github_url"`
}

type deployResponse struct {
	DeploymentID string `json:"deployment_id"`
	Status       string `json:"status"`
	Message      string `json:"message"`
}

type errorResponse struct {
	Error string `json:"error"`
	Hint  string `json:"hint,omitempty"`
}

const maxMultipartMemory = 32 << 20 // 32 MB buffered in RAM; rest spills to disk

// Create handles both submission paths:
//
//   - application/json        { "github_url": "https://github.com/..." }
//   - multipart/form-data     form field "file" containing a .zip archive
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}

	ct := r.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing or malformed Content-Type header", "")
		return
	}

	switch {
	case mediaType == "application/json":
		h.handleGitHub(w, r)
	case strings.HasPrefix(mediaType, "multipart/form-data"):
		h.handleUpload(w, r)
	default:
		writeError(w, http.StatusUnsupportedMediaType,
			fmt.Sprintf("unsupported Content-Type %q", mediaType),
			"send application/json with a github_url field, or multipart/form-data with a file field",
		)
	}
}

func (h *Handler) handleGitHub(w http.ResponseWriter, r *http.Request) {
	var req githubRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse JSON body", "")
		return
	}

	req.GithubURL = strings.TrimSpace(req.GithubURL)
	if req.GithubURL == "" {
		writeError(w, http.StatusBadRequest, "github_url is required", "")
		return
	}
	if !isValidGitURL(req.GithubURL) {
		writeError(w, http.StatusBadRequest,
			"invalid github_url — must be a GitHub, GitLab, or Bitbucket URL",
			"",
		)
		return
	}

	deployID, workDir, err := h.initDeployment(req.GithubURL)
	if err != nil {
		h.logger.Error("failed to initialise deployment", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create deployment", "")
		return
	}

	// Clone asynchronously so the HTTP response returns the deployment ID immediately.
	// The client will poll status or stream logs via /api/deploy/:id and /api/deploy/:id/logs.
	go h.cloneAsync(deployID, req.GithubURL, workDir)

	writeJSON(w, http.StatusAccepted, deployResponse{
		DeploymentID: deployID,
		Status:       string(repository.StatusCloning),
		Message:      "deployment created — cloning repository",
	})
}

func isValidGitURL(u string) bool {
	validPrefixes := []string{
		"https://github.com/",
		"https://gitlab.com/",
		"https://bitbucket.org/",
		"git@github.com:",
		"git@gitlab.com:",
		"git@bitbucket.org:",
	}
	for _, p := range validPrefixes {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

// cloneAsync runs git clone in a background goroutine and updates the database
// when the operation completes or fails.
func (h *Handler) cloneAsync(deployID, repoURL, workDir string) {
	log := h.logger.With("deployment_id", deployID, "repo", repoURL)
	log.Info("starting git clone")

	// TODO: catch error
	h.deployRepo.Update(deployID, map[string]any{
		"status": repository.StatusCloning,
	})

	err := CloneRepo(context.Background(), repoURL, workDir)
	if err != nil {
		var inaccessible *ErrRepoInaccessible
		if errors.As(err, &inaccessible) {
			log.Warn("repository inaccessible", "detail", inaccessible.Detail)
			h.deployRepo.Update(deployID, map[string]any{
				"status": repository.StatusFailed,
				"error": inaccessible.Detail +
					"\n\nTo fix this: make the repository public, or add our deploy key " +
					"in GitHub → Settings → Deploy keys → Add deploy key.",
			})
			return
		}

		log.Error("git clone failed", "error", err)
		h.deployRepo.Update(deployID, map[string]any{
			"status": repository.StatusFailed,
			"error":  err.Error(),
		})
		return
	}

	log.Info("repository cloned successfully")
	h.deployRepo.Update(deployID, map[string]any{
		"status": repository.StatusPending,
	})
}

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse multipart form", "")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest,
			`form field "file" is required`,
			"attach a .zip archive of your project source",
		)
		return
	}
	defer file.Close()

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		writeError(w, http.StatusBadRequest,
			"only .zip archives are accepted",
			"compress your project directory into a .zip file and try again",
		)
		return
	}

	deployID, workDir, err := h.initDeployment("")
	if err != nil {
		h.logger.Error("failed to initialise deployment", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create deployment", "")
		return
	}

	if err := ExtractZip(file, workDir); err != nil {
		h.logger.Error("zip extraction failed", "deployment_id", deployID, "error", err)
		h.deployRepo.Update(deployID, map[string]any{
			"status": repository.StatusFailed,
			"error":  err.Error(),
		})
		writeError(w, http.StatusBadRequest, "failed to extract zip archive: "+err.Error(), "")
		return
	}

	h.logger.Info("zip extracted", "deployment_id", deployID, "filename", header.Filename)
	h.deployRepo.Update(deployID, map[string]any{
		"status": repository.StatusPending,
	})

	writeJSON(w, http.StatusAccepted, deployResponse{
		DeploymentID: deployID,
		Status:       string(repository.StatusPending),
		Message:      "deployment created — source extracted and ready to build",
	})
}

func (h *Handler) initDeployment(sourceURL string) (string, string, error) {
	objID := primitive.NewObjectID()

	workDir, err := PrepareWorkDir(objID.Hex())
	if err != nil {
		return "", "", err
	}

	err = h.deployRepo.Create(&repository.Deployment{
		ID:        objID,
		Status:    repository.StatusPending,
		SourceDir: workDir,
		SourceURL: sourceURL,
		Error:     "",
	})

	if err != nil {
		return "", "", err
	}

	return objID.Hex(), workDir, nil
}

// GetStatus returns the current state of a deployment.
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}

	id := deployIDFromPath(r.URL.Path)
	if id == "" {
		writeError(w, http.StatusBadRequest, "deployment id is required", "")
		return
	}

	d, err := h.deployRepo.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get deployment", err.Error())
		return
	}
	if d == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("deployment %q not found", id), "")
		return
	}

	writeJSON(w, http.StatusOK, *d)
}

func deployIDFromPath(path string) string {
	const prefix = "/api/deploy/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	if before, _, ok := strings.Cut(rest, "/"); ok {
		return before
	}
	return rest
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg, hint string) {
	writeJSON(w, status, errorResponse{Error: msg, Hint: hint})
}
