package tunnel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
)

// Bot ip rules (owned only by the watcher): prio 100 keeps local and
// Docker traffic (Mongo) on main, prio 101 sends the rest to table 100 "exit".
const (
	keepLocalPrio = "100"
	exitPrio      = "101"
	exitTable     = "100"
)

// hostNet reads the exit tunnel and sets the bot's ip rules on this host.
type hostNet struct {
	iface   string
	uids    string // "U-U" for ip rule uidrange
	wrapper string
	timeout time.Duration
}

// NewHostNet runs `awg show <iface> latest-handshakes` and `ip rule` for the
// bot's uid, through cfg.AWGExec if set.
func NewHostNet(cfg *config.Config) *hostNet {
	return &hostNet{
		iface:   cfg.ExitIface,
		uids:    fmt.Sprintf("%d-%d", os.Getuid(), os.Getuid()),
		wrapper: cfg.AWGExec,
		timeout: cfg.AWGTimeout,
	}
}

// LastHandshake returns the newest handshake on the exit interface; zero = never.
func (s *hostNet) LastHandshake(ctx context.Context) (time.Time, error) {
	out, err := s.run(ctx, "awg", "show", s.iface, "latest-handshakes")
	if err != nil {
		return time.Time{}, err
	}
	var last int64
	for line := range strings.Lines(out) {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sec, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("tunnel: handshake %q: %w", line, err)
		}
		last = max(last, sec)
	}
	if last == 0 {
		return time.Time{}, nil
	}
	return time.Unix(last, 0), nil
}

// RouteBot sends the bot's traffic through the tunnel or directly.
func (s *hostNet) RouteBot(ctx context.Context, viaTunnel bool) error {
	err := s.delRule(ctx, exitPrio)
	if err != nil || !viaTunnel {
		return err
	}
	// Re-adding prio 100 makes sure it exists, without parsing `ip rule show`.
	err = s.delRule(ctx, keepLocalPrio)
	if err != nil {
		return err
	}
	_, err = s.run(ctx, "ip", "rule", "add", "uidrange", s.uids, "lookup", "main", "suppress_prefixlength", "0", "prio", keepLocalPrio)
	if err != nil {
		return err
	}
	_, err = s.run(ctx, "ip", "rule", "add", "uidrange", s.uids, "lookup", exitTable, "prio", exitPrio)
	return err
}

// delRule deletes the rule with this priority; a missing rule is fine.
func (s *hostNet) delRule(ctx context.Context, prio string) error {
	_, err := s.run(ctx, "ip", "rule", "del", "prio", prio)
	if err != nil && !strings.Contains(err.Error(), "No such file or directory") {
		return err
	}
	return nil
}

// run runs one command (through the wrapper if set) and returns its stdout.
// Same timeout handling as amnezia's local runner: the whole process group
// is killed, so a wrapper's ssh does not keep the pipes open.
func (s *hostNet) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if s.wrapper != "" {
		args = append([]string{s.wrapper}, args...)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	name := strings.Join(args, " ")
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("tunnel: %s: timed out after %s", name, s.timeout)
	}
	if err != nil {
		return "", fmt.Errorf("tunnel: %s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
