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

	"github.com/MogboPython/komo-sahvah/backend/internal/logs"
	"github.com/MogboPython/komo-sahvah/backend/internal/repository"
	"github.com/MogboPython/komo-sahvah/backend/pkg/common"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Handler struct {
	deployRepo repository.DeploymentRepository
	registry   *logs.Registry
	pipeline   *Pipeline
	logger     *slog.Logger
}

func NewHandler(deployRepo repository.DeploymentRepository, registry *logs.Registry, pipeline *Pipeline, logger *slog.Logger) *Handler {
	return &Handler{
		deployRepo: deployRepo,
		registry:   registry,
		pipeline:   pipeline,
		logger:     logger,
	}
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

// - application/json        { "github_url": "https://github.com/..." }
// - multipart/form-data     form field "file" containing a .zip archive
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
	if !common.IsValidGitURL(req.GithubURL) {
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

	go func() {
		h.cloneAndRun(deployID, req.GithubURL, workDir)
	}()

	writeJSON(w, http.StatusAccepted, deployResponse{
		DeploymentID: deployID,
		Status:       string(repository.StatusCloning),
		Message:      "deployment created — cloning repository",
	})
}

func (h *Handler) cloneAndRun(deployID, repoURL, workDir string) {
	log := h.logger.With("deployment_id", deployID, "repo", repoURL)

	bc, ok := h.registry.Get(deployID)
	if !ok {
		log.Error("broadcaster missing at clone start")
		return
	}

	bc.Systemf("=== cloning repository ===")
	bc.Systemf("url: %s", repoURL)

	h.deployRepo.Update(deployID, map[string]any{
		"status": repository.StatusCloning,
	})

	err := CloneRepo(context.Background(), repoURL, workDir)
	if err != nil {
		var inaccessible *ErrRepoInaccessible
		if errors.As(err, &inaccessible) {
			msg := inaccessible.Detail +
				"\n\nTo fix this: make the repository public, or add our deploy key " +
				"in GitHub → Settings → Deploy keys → Add deploy key."

			log.Warn("repository inaccessible", "detail", inaccessible.Detail)

			h.deployRepo.Update(deployID, map[string]any{
				"status": repository.StatusFailed,
				"error":  msg,
			})

			bc.Systemf("ERROR: %s", msg)
			bc.Close(logs.DoneEvent{Status: "failed", Error: msg})

			return
		}

		log.Error("git clone failed", "error", err)
		msg := err.Error()
		h.deployRepo.Update(deployID, map[string]any{
			"status": repository.StatusFailed,
			"error":  msg,
		})

		bc.Systemf("ERROR: git clone failed: %s", msg)
		bc.Close(logs.DoneEvent{Status: "failed", Error: msg})

		return
	}

	bc.Systemf("=== repository cloned successfully ===")
	log.Info("clone complete, starting pipeline")

	h.deployRepo.Update(deployID, map[string]any{
		"status": repository.StatusPending,
	})

	h.pipeline.Run(deployID)
}

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse multipart form", "")
		return
	}

	// TODO: instead of loading the entire file into memory, stream it to a temporary file and then extract it.

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

	bc, _ := h.registry.Get(deployID)
	bc.Systemf("=== extracting uploaded archive ===")
	bc.Systemf("filename: %s", header.Filename)

	if err := ExtractZip(file, workDir); err != nil {
		h.logger.Error("zip extraction failed", "deployment_id", deployID, "error", err)
		h.deployRepo.Update(deployID, map[string]any{
			"status": repository.StatusFailed,
			"error":  err.Error(),
		})

		bc.Systemf("ERROR: %s", err)
		bc.Close(logs.DoneEvent{Status: "failed", Error: err.Error()})

		writeError(w, http.StatusBadRequest, "failed to extract zip archive: "+err.Error(), "")
		return
	}

	bc.Systemf("=== zip extracted successfully ===")
	h.logger.Info("zip extracted", "deployment_id", deployID, "filename", header.Filename)

	h.deployRepo.Update(deployID, map[string]any{
		"status": repository.StatusPending,
	})

	writeJSON(w, http.StatusAccepted, deployResponse{
		DeploymentID: deployID,
		Status:       string(repository.StatusPending),
		Message:      "deployment created — source extracted, build starting",
	})

	go h.pipeline.Run(deployID)
}

func (h *Handler) initDeployment(sourceURL string) (id, workDir string, err error) {
	objID := primitive.NewObjectID()
	id = objID.Hex()

	workDir, err = PrepareWorkDir(id)
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

	h.registry.Create(id)

	return id, workDir, nil
}

func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}

	id := r.PathValue("id")
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

// SSE event format:
//
//	event: log
//	data: {"text":"...","stream":"stdout","time":"..."}
//
//	event: done
//	data: {"status":"running"}  (or {"status":"failed","error":"..."})
func (h *Handler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "deployment id is required", "")
		return
	}

	bc, ok := h.registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("deployment %q not found", id), "")
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported by this server", "")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disables Nginx/Caddy buffering

	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	lineCh, doneCh := bc.Subscribe(r.Context(), 0)

	enc := json.NewEncoder(w)

	for {
		select {
		case line, open := <-lineCh:
			if !open {
				continue
			}
			fmt.Fprintf(w, "event: log\ndata: ")
			_ = enc.Encode(line)
			fmt.Fprintf(w, "\n")
			flusher.Flush()

		case doneEvt := <-doneCh:
			fmt.Fprintf(w, "event: done\ndata: ")
			_ = enc.Encode(doneEvt)
			fmt.Fprintf(w, "\n")
			flusher.Flush()
			return

		case <-r.Context().Done():
			return
		}
	}
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
