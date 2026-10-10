package tunnel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
)

// fakeBins puts shell scripts named after the map keys first on PATH.
func fakeBins(t *testing.T, scripts map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, script := range scripts {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o700))
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

func newTestNet() *hostNet {
	return NewHostNet(&config.Config{
		ExitIface:  "awg-exit",
		AWGTimeout: 5 * time.Second,
	})
}

func TestLastHandshake(t *testing.T) {
	fakeBins(t, map[string]string{
		"awg": `[ "$*" = "show awg-exit latest-handshakes" ] || exit 9; printf 'PUB=\t1791491368\n'`,
	})

	got, err := newTestNet().LastHandshake(context.Background())
	require.NoError(t, err)
	require.Equal(t, time.Unix(1791491368, 0), got)
}

func TestLastHandshakeNever(t *testing.T) {
	fakeBins(t, map[string]string{
		"awg": `printf 'PUB=\t0'`,
	})

	got, err := newTestNet().LastHandshake(context.Background())
	require.NoError(t, err)
	require.True(t, got.IsZero())
}

// A stopped or failed awg-exit unit leaves no interface: that is a dead
// tunnel, not a failed read, or the watcher would never go Down.
func TestLastHandshakeNoInterfaceIsNever(t *testing.T) {
	fakeBins(t, map[string]string{
		"awg": `echo "Unable to access interface: No such device" >&2; exit 1`,
	})

	got, err := newTestNet().LastHandshake(context.Background())
	require.NoError(t, err)
	require.True(t, got.IsZero())
}

func TestLastHandshakeErrors(t *testing.T) {
	fakeBins(t, map[string]string{
		"awg": `echo "Unable to access interface: Operation not permitted" >&2; exit 1`,
	})
	_, err := newTestNet().LastHandshake(context.Background())
	require.ErrorContains(t, err, "Operation not permitted")
	require.ErrorContains(t, err, "tunnel: awg show awg-exit")

	fakeBins(t, map[string]string{
		"awg": `printf 'PUB=\tsoon\n'`,
	})
	_, err = newTestNet().LastHandshake(context.Background())
	require.Error(t, err)
}

// fakeIP fakes ip: logs its args and answers `rule show prio N` from the
// present rules ("rule show prio 101", "-6 rule show prio 101", ...).
func fakeIP(t *testing.T, present ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, show := range present {
		f := filepath.Join(dir, strings.ReplaceAll(show, " ", "_"))
		line := "101:\tfrom all uidrange " + botUIDs() + " lookup 100\n"
		require.NoError(t, os.WriteFile(f, []byte(line), 0o600))
	}
	log := filepath.Join(dir, "ip.log")
	fakeBins(t, map[string]string{
		"ip": `echo "$*" >> ` + log + `
f=` + dir + `/$(echo "$*" | tr ' ' _)
if [ -f "$f" ]; then cat "$f"; fi`,
	})
	return log
}

func botUIDs() string {
	return fmt.Sprintf("%d-%d", os.Getuid(), os.Getuid())
}

// changes returns the logged ip calls that are not `rule show`.
func changes(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	out := []string{}
	for line := range strings.Lines(string(b)) {
		if !strings.Contains(line, "rule show") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

var allRules = []string{
	"rule show prio 100",
	"rule show prio 101",
	"-6 rule show prio 101",
}

func TestRouteBotViaTunnelAddsMissingRules(t *testing.T) {
	log := fakeIP(t)
	u := botUIDs()

	require.NoError(t, newTestNet().RouteBot(context.Background(), true))
	require.Equal(t, []string{
		"rule add uidrange " + u + " lookup main suppress_prefixlength 0 prio 100",
		"rule add uidrange " + u + " lookup 100 prio 101",
		"-6 rule add uidrange " + u + " prio 101 unreachable",
	}, changes(t, log))
}

func TestRouteBotViaTunnelAddsOnlyTheMissingRule(t *testing.T) {
	log := fakeIP(
		t,
		"rule show prio 100",
		"-6 rule show prio 101",
	)

	require.NoError(t, newTestNet().RouteBot(context.Background(), true))
	require.Equal(t, []string{
		"rule add uidrange " + botUIDs() + " lookup 100 prio 101",
	}, changes(t, log))
}

func TestRouteBotViaTunnelLeavesPresentRulesAlone(t *testing.T) {
	log := fakeIP(t, allRules...)

	require.NoError(t, newTestNet().RouteBot(context.Background(), true))
	require.Empty(t, changes(t, log), "no gap: nothing deleted or re-added")
}

// A rule of another uid at the same prio is not the bot's rule.
func TestRouteBotIgnoresRulesOfOtherUIDs(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "ip.log")
	fakeBins(t, map[string]string{
		"ip": `echo "$*" >> ` + log + `
case "$*" in *"rule show"*) printf '101:\tfrom all uidrange 1` + botUIDs() + `9 lookup 100\n';; esac`,
	})

	require.NoError(t, newTestNet().RouteBot(context.Background(), true))
	require.Len(t, changes(t, log), 3)
}

func TestRouteBotDirectDeletesPresentExitRules(t *testing.T) {
	log := fakeIP(t, allRules...)
	u := botUIDs()

	require.NoError(t, newTestNet().RouteBot(context.Background(), false))
	require.Equal(t, []string{
		"rule del prio 101 uidrange " + u,
		"-6 rule del prio 101 uidrange " + u,
	}, changes(t, log), "prio 100 keep-local stays")
}

func TestRouteBotDirectWithNoRulesDeletesNothing(t *testing.T) {
	log := fakeIP(t, "rule show prio 100")

	require.NoError(t, newTestNet().RouteBot(context.Background(), false))
	require.Empty(t, changes(t, log))
}

func TestRouteBotHostWithoutIPv6(t *testing.T) {
	log := filepath.Join(t.TempDir(), "ip.log")
	fakeBins(t, map[string]string{
		"ip": `echo "$*" >> ` + log + `
case "$1" in -6) echo "RTNETLINK answers: Address family not supported by protocol" >&2; exit 2;; esac`,
	})
	n := newTestNet()
	u := botUIDs()

	require.NoError(t, n.RouteBot(context.Background(), true))
	got := changes(t, log)
	require.Contains(t, got, "rule add uidrange "+u+" lookup main suppress_prefixlength 0 prio 100")
	require.Contains(t, got, "rule add uidrange "+u+" lookup 100 prio 101")
	require.NoError(t, n.RouteBot(context.Background(), false))
}

// The rule vanished between show and del: nothing left to do.
func TestRouteBotDirectRuleGoneBeforeDel(t *testing.T) {
	log := filepath.Join(t.TempDir(), "ip.log")
	fakeBins(t, map[string]string{
		"ip": `echo "$*" >> ` + log + `
case "$*" in
*"rule show"*) printf '101:\tfrom all uidrange ` + botUIDs() + ` lookup 100\n';;
*"rule del"*) echo "RTNETLINK answers: No such file or directory" >&2; exit 2;;
esac`,
	})

	require.NoError(t, newTestNet().RouteBot(context.Background(), false))
	require.Len(t, changes(t, log), 2, "both deletes were tried")
}

func TestRouteBotShowFails(t *testing.T) {
	log := filepath.Join(t.TempDir(), "ip.log")
	fakeBins(t, map[string]string{
		"ip": `echo "$*" >> ` + log + `
case "$*" in *"rule show"*) echo "Operation not permitted" >&2; exit 2;; esac`,
	})

	for _, viaTunnel := range []bool{
		true,
		false,
	} {
		err := newTestNet().RouteBot(context.Background(), viaTunnel)
		require.ErrorContains(t, err, "tunnel: ip rule show prio")
		require.ErrorContains(t, err, "Operation not permitted")
	}
	require.Empty(t, changes(t, log), "no add or del after a failed show")
}

func TestRouteBotFails(t *testing.T) {
	fakeBins(t, map[string]string{
		"ip": `case "$*" in *"rule show"*) exit 0;; esac; echo "Operation not permitted" >&2; exit 2`,
	})

	err := newTestNet().RouteBot(context.Background(), true)
	require.ErrorContains(t, err, "Operation not permitted")
	require.ErrorContains(t, err, "tunnel: ip rule add")
}

func TestHostNetUsesTheWrapper(t *testing.T) {
	wrapper := filepath.Join(t.TempDir(), "wrap")
	require.NoError(t, os.WriteFile(wrapper, []byte("#!/bin/sh\n[ \"$1\" = awg ] && printf 'PUB=\\t7\\n'"), 0o700))
	n := NewHostNet(&config.Config{
		ExitIface:  "awg-exit",
		AWGTimeout: 5 * time.Second,
		AWGExec:    wrapper,
	})
	got, err := n.LastHandshake(context.Background())
	require.NoError(t, err)
	require.Equal(t, time.Unix(7, 0), got)
}
