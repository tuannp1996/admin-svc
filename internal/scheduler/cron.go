package scheduler

import (
	"admin-svc/internal/client"
	"admin-svc/internal/config"
	"admin-svc/internal/docker"
	"admin-svc/internal/service"
	"admin-svc/internal/usecase/port"
)

type Scheduler = service.Statistics

func New(cfg *config.Config, notifier port.Notifier, dockerChecker *docker.Checker, services *[]client.Service) *Scheduler {
	return service.New(cfg, notifier, dockerChecker, services)
}
