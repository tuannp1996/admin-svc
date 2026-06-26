package scheduler

import (
	"admin-svc/internal/config"
	"admin-svc/internal/docker"
	"admin-svc/internal/service"
	"admin-svc/internal/usecase/port"
)

type Scheduler = service.Statistics

func New(cfg *config.Config, notifier port.Notifier, dockerChecker *docker.Checker) *Scheduler {
	return service.New(cfg, notifier, dockerChecker)
}
