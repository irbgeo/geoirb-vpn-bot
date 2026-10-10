package tunnel

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
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
// interface (awg-exit has one); zero = never. No interface at all (its unit
// stopped or failed) is "never" too, not an error: the watcher keeps its
// state on an error and would leave the bot on a dead route for good.
func (s *hostNet) LastHandshake(ctx context.Context) (time.Time, error) {
	out, err := s.exec(ctx, "awg", "show", s.iface, "latest-handshakes")
	if err != nil && strings.Contains(err.Error(), "No such device") {
		return time.Time{}, nil
	}
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
// instead of going around the tunnel. It is called on every check, so it
// changes only what is wrong: a rule in place is never deleted and re-added
// (that would open a gap each minute).
func (s *hostNet) RouteBot(ctx context.Context, viaTunnel bool) error {
	exit := ipRule{
		prio: exitPrio,
		add: []string{
			"lookup",
			exitTable,
			"prio",
			exitPrio,
		},
	}
	exit6 := ipRule{
		v6:   true,
		prio: exitPrio,
		add: []string{
			"prio",
			exitPrio,
			"unreachable",
		},
	}
	if !viaTunnel {
		err := s.delRule(ctx, exit)
		if err != nil {
			return err
		}
		return s.delRule(ctx, exit6)
	}
	keepLocal := ipRule{
		prio: keepLocalPrio,
		add: []string{
			"lookup",
			"main",
			"suppress_prefixlength",
			"0",
			"prio",
			keepLocalPrio,
		},
	}
	rules := []ipRule{
		keepLocal,
		exit,
		exit6,
	}
	for _, r := range rules {
		err := s.addRule(ctx, r)
		if err != nil {
			return err
		}
	}
	return nil
}

// addRule adds the rule unless the bot already has it.
func (s *hostNet) addRule(ctx context.Context, r ipRule) error {
	has, err := s.hasRule(ctx, r)
	if err != nil || has {
		return err
	}
	base := []string{
		"rule",
		"add",
		"uidrange",
		s.uids,
	}
	args := slices.Concat(base, r.add)
	_, err = s.ip(ctx, r.family(args))
	return err
}

// delRule deletes the rule if the bot has it; gone in between is fine.
func (s *hostNet) delRule(ctx context.Context, r ipRule) error {
	has, err := s.hasRule(ctx, r)
	if err != nil || !has {
		return err
	}
	args := []string{
		"rule",
		"del",
		"prio",
		r.prio,
		"uidrange",
		s.uids,
	}
	_, err = s.ip(ctx, r.family(args))
	if err != nil && strings.Contains(err.Error(), "No such file or directory") {
		return nil
	}
	return err
}

// hasRule reports whether a rule of the bot's uid sits at the rule's prio.
func (s *hostNet) hasRule(ctx context.Context, r ipRule) (bool, error) {
	args := []string{
		"rule",
		"show",
		"prio",
		r.prio,
	}
	out, err := s.ip(ctx, r.family(args))
	if err != nil {
		return false, err
	}
	return slices.Contains(strings.Fields(out), s.uids), nil
}

// ip runs `ip <args>`. A host without IPv6 is fine: its error is logged once
// and the answer is empty.
func (s *hostNet) ip(ctx context.Context, args []string) (string, error) {
	out, err := s.exec(ctx, append([]string{"ip"}, args...)...)
	if err != nil && strings.Contains(err.Error(), "Address family not supported") {
		s.noIPv6.Do(func() { log.Printf("tunnel: no IPv6 on this host, IPv6 rules skipped: %v", err) })
		return "", nil
	}
	return out, err
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
