package tunnel

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
	"github.com/irbgeo/geoirb-vpn-bot/internal/hostexec"
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
	iface  string
	uids   string // "U-U" for ip rule uidrange
	run    *hostexec.Runner
	noIPv6 sync.Once // logs "no IPv6 on this host" once
}

// NewHostNet runs `awg show <iface> latest-handshakes` and `ip rule` for the
// bot's uid, through cfg.AWGExec if set.
func NewHostNet(cfg *config.Config) *hostNet {
	run := hostexec.New(cfg)
	return &hostNet{
		iface: cfg.ExitIface,
		uids:  fmt.Sprintf("%d-%d", os.Getuid(), os.Getuid()),
		run:   run,
	}
}

// LastHandshake returns the newest handshake over all peers of the exit
// interface (awg-exit has one); zero = never.
func (s *hostNet) LastHandshake(ctx context.Context) (time.Time, error) {
	out, err := s.exec(ctx, "awg", "show", s.iface, "latest-handshakes")
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

// RouteBot sends the bot's traffic through the tunnel or directly. Via the
// tunnel IPv6 is unreachable for the bot, so Go falls back to IPv4 at once
// instead of going around the tunnel.
func (s *hostNet) RouteBot(ctx context.Context, viaTunnel bool) error {
	err := s.delRule(ctx, "rule", "del", "prio", exitPrio, "uidrange", s.uids)
	if err != nil {
		return err
	}
	err = s.delRule(ctx, "-6", "rule", "del", "prio", exitPrio, "uidrange", s.uids)
	if err != nil || !viaTunnel {
		return err
	}
	// Re-adding prio 100 makes sure it exists, without parsing `ip rule show`.
	err = s.delRule(ctx, "rule", "del", "prio", keepLocalPrio, "uidrange", s.uids)
	if err != nil {
		return err
	}
	err = s.ip(ctx, "rule", "add", "uidrange", s.uids, "lookup", "main", "suppress_prefixlength", "0", "prio", keepLocalPrio)
	if err != nil {
		return err
	}
	err = s.ip(ctx, "rule", "add", "uidrange", s.uids, "lookup", exitTable, "prio", exitPrio)
	if err != nil {
		return err
	}
	return s.ip(ctx, "-6", "rule", "add", "uidrange", s.uids, "prio", exitPrio, "unreachable")
}

// delRule runs an `ip rule del`; a missing rule is fine.
func (s *hostNet) delRule(ctx context.Context, args ...string) error {
	err := s.ip(ctx, args...)
	if err != nil && strings.Contains(err.Error(), "No such file or directory") {
		return nil
	}
	return err
}

// ip runs `ip <args>`. A host without IPv6 is fine: its error is logged once.
func (s *hostNet) ip(ctx context.Context, args ...string) error {
	_, err := s.exec(ctx, append([]string{"ip"}, args...)...)
	if err != nil && strings.Contains(err.Error(), "Address family not supported") {
		s.noIPv6.Do(func() { log.Printf("tunnel: no IPv6 on this host, IPv6 rules skipped: %v", err) })
		return nil
	}
	return err
}

// exec runs one command on the host and returns its stdout.
func (s *hostNet) exec(ctx context.Context, args ...string) (string, error) {
	in := hostexec.Input{
		Args: args,
	}
	out, err := s.run.Run(ctx, in)
	if err != nil {
		return "", fmt.Errorf("tunnel: %w", err)
	}
	return out, nil
}
