package amnezia

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
)

// localRunner runs commands on this host, or through a wrapper (AWG_EXEC).
type localRunner struct {
	wrapper string
	timeout time.Duration
}

// NewLocalRunner runs commands on this host (or through cfg.AWGExec).
func NewLocalRunner(cfg *config.Config) *localRunner {
	return &localRunner{
		wrapper: cfg.AWGExec,
		timeout: cfg.AWGTimeout,
	}
}

// Exec runs one command and returns its stdout.
// Errors carry the command and stderr, never stdin (it may hold secrets).
func (s *localRunner) Exec(ctx context.Context, in execInput) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	name := s.wrapper
	args := in.Args
	if name == "" {
		name = in.Args[0]
		args = in.Args[1:]
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(in.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	// On timeout the process is killed, but a command it started may still
	// finish, so callers must treat the result as unknown.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("amnezia: %s: timed out after %s", strings.Join(in.Args, " "), s.timeout)
	}
	if err != nil {
		return "", fmt.Errorf("amnezia: %s: %w: %s", strings.Join(in.Args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
