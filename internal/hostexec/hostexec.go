// Package hostexec runs commands on this host (or through the AWG_EXEC
// wrapper) with a time limit that kills the whole process group.
package hostexec

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

// Runner runs commands on this host, or through a wrapper (AWG_EXEC).
type Runner struct {
	wrapper string
	timeout time.Duration
}

// New creates a Runner from cfg.AWGExec and cfg.AWGTimeout.
func New(
	cfg *config.Config,
) *Runner {
	return &Runner{
		wrapper: cfg.AWGExec,
		timeout: cfg.AWGTimeout,
	}
}

// Run runs one command and returns its stdout. Errors carry the command
// and stderr, never stdin (it may hold secrets).
func (s *Runner) Run(ctx context.Context, in Input) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	args := in.Args
	if s.wrapper != "" {
		args = append([]string{s.wrapper}, args...)
	}
	//nolint:gosec // G204: running host commands is this package's job; callers pass fixed text, config values and keys, never user text
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
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

	start := time.Now()
	err := cmd.Run()
	name := strings.Join(in.Args, " ")
	// On timeout the group is killed, possibly half-way through a script, so
	// callers treat the result as unknown.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// The real time: the deadline may be the caller's, shorter than s.timeout.
		return "", fmt.Errorf("%s: timed out after %s", name, time.Since(start).Round(time.Millisecond))
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
