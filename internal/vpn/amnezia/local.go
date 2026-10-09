package amnezia

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
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
	// Own process group: on timeout the whole group dies, not only the direct
	// child (sh -c scripts, ssh under a wrapper keep the pipes open).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = strings.NewReader(in.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	// On timeout the group is killed, possibly half-way through a script, so
	// callers treat the result as unknown (ErrNotPersisted / undo).
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("amnezia: %s: timed out after %s", strings.Join(in.Args, " "), s.timeout)
	}
	if err != nil {
		return "", fmt.Errorf("amnezia: %s: %w: %s", strings.Join(in.Args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
