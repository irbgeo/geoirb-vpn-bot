package worker

import (
	"context"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// Job is the periodic work (implemented by the value service.New returns).
type Job interface {
	Maintain(ctx context.Context) (*service.Maintenance, error)
}

// Delivery sends what a run found (implemented by the value bot.New returns).
type Delivery interface {
	DeliverMaintenance(ctx context.Context, m *service.Maintenance)
}
