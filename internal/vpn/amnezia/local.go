package amnezia

import (
	"context"
	"fmt"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
	"github.com/irbgeo/geoirb-vpn-bot/internal/hostexec"
)

// localRunner runs commands on this host, or through a wrapper (AWG_EXEC).
type localRunner struct {
	run *hostexec.Runner
}

// NewLocalRunner runs commands on this host (or through cfg.AWGExec).
func NewLocalRunner(cfg *config.Config) *localRunner {
	run := hostexec.New(cfg)
	return &localRunner{
		run: run,
	}
}

// Exec runs one command and returns its stdout. On a timeout the result is
// unknown (callers use ErrNotPersisted / undo).
func (s *localRunner) Exec(ctx context.Context, in execInput) (string, error) {
	hostInput := hostexec.Input{
		Args:  in.Args,
		Stdin: in.Stdin,
	}
	out, err := s.run.Run(ctx, hostInput)
	if err != nil {
		return "", fmt.Errorf("amnezia: %w", err)
	}
	return out, nil
}
