package deployment

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/MogboPython/komo-sahvah/backend/internal/build"
	"github.com/MogboPython/komo-sahvah/backend/internal/logs"
	"github.com/MogboPython/komo-sahvah/backend/internal/repository"
)

const dockerNetwork = "ks-network"

type Pipeline struct {
	deployRepo repository.DeploymentRepository
	registry   *logs.Registry
	logger     *slog.Logger
}

func NewPipeline(deployRepo repository.DeploymentRepository, r *logs.Registry, l *slog.Logger) *Pipeline {
	return &Pipeline{deployRepo: deployRepo, registry: r, logger: l}
}

// Run executes the full pipeline for deployID.
// It is safe to call from a goroutine; it never panics.
//
// Phases:
//
//  1. (optional) git clone  — already done by handleGitHub before this is called
//  2. railpack build        → Docker image
//  3. docker run            → running container + IP
//  4. (future) Caddy route  → public URL
func (p *Pipeline) Run(deployID string) {
	log := p.logger.With("deployment_id", deployID)

	bc, ok := p.registry.Get(deployID)
	if !ok {
		log.Error("broadcaster not found for deployment")
		return
	}

	d, err := p.deployRepo.GetByID(deployID)
	if err != nil {
		log.Error("failed to get deployment", "error", err)
		bc.Close(logs.DoneEvent{Status: "failed", Error: "internal: failed to get deployment"})
		return
	}
	if d == nil {
		log.Error("deployment not found in database")
		bc.Close(logs.DoneEvent{Status: "failed", Error: "internal: deployment record missing"})
		return
	}

	imageName := build.ImageName(deployID)

	p.deployRepo.Update(deployID, map[string]any{
		"status":     repository.StatusBuilding,
		"image_name": imageName,
	})

	log.Info("starting railpack build", "source", d.SourceDir, "image", imageName)

	if err := build.RunRailpack(context.Background(), d.SourceDir, imageName, bc); err != nil {
		log.Error("railpack build failed", "error", err)
		p.fail(deployID, bc, fmt.Sprintf("build failed: %s", err))
		return
	}

	log.Info("starting container", "image", imageName)

	info, err := build.RunContainer(
		context.Background(),
		deployID,
		imageName,
		dockerNetwork,
		bc,
	)
	if err != nil {
		log.Error("container start failed", "error", err)
		p.fail(deployID, bc, fmt.Sprintf("container failed to start: %s", err))
		return
	}

	appURL := fmt.Sprintf("/app/%s/", deployID)
	p.deployRepo.Update(deployID, map[string]any{
		"status":       repository.StatusRunning,
		"container_id": info.ID,
		"container_ip": info.IP,
		"app_url":      appURL,
	})

	log.Info("deployment running",
		"container_id", info.ID,
		"container_ip", info.IP,
		"app_url", appURL,
	)

	bc.Close(logs.DoneEvent{
		Status: string(repository.StatusRunning),
	})
}

func (p *Pipeline) RunAfterClone(deployID string) {
	bc, ok := p.registry.Get(deployID)
	if !ok {
		return
	}

	for {
		d, err := p.deployRepo.GetByID(deployID)
		if err != nil {
			bc.Close(logs.DoneEvent{Status: "failed", Error: "internal: failed to get deployment"})
			return
		}
		if d == nil {
			bc.Close(logs.DoneEvent{Status: "failed", Error: "internal: deployment record missing"})
			return
		}

		switch d.Status {
		case repository.StatusFailed:
			bc.Systemf("clone failed: %s", d.Error)
			bc.Close(logs.DoneEvent{Status: "failed", Error: d.Error})
			return

		case repository.StatusPending:
			p.Run(deployID)
			return

		case repository.StatusCloning:
			yieldMS(200)

		default:
			p.Run(deployID)
			return
		}
	}
}

// mark deployment as failed, log error and close stream.
func (p *Pipeline) fail(deployID string, bc *logs.Broadcaster, msg string) {
	p.deployRepo.Update(deployID, map[string]any{
		"status": repository.StatusFailed,
		"error":  msg,
	})
	bc.Systemf("FAILED: %s", msg)
	bc.Close(logs.DoneEvent{Status: "failed", Error: msg})
}

func yieldMS(ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
}
