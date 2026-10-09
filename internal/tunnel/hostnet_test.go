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

func TestLastHandshakeErrors(t *testing.T) {
	fakeBins(t, map[string]string{
		"awg": `echo "Unable to access interface" >&2; exit 1`,
	})
	_, err := newTestNet().LastHandshake(context.Background())
	require.ErrorContains(t, err, "Unable to access interface")
	require.ErrorContains(t, err, "tunnel: awg show awg-exit")

	fakeBins(t, map[string]string{
		"awg": `printf 'PUB=\tsoon\n'`,
	})
	_, err = newTestNet().LastHandshake(context.Background())
	require.Error(t, err)
}

// ipLog fakes ip: logs its args; "rule del" of a missing rule fails like
// the real one.
func ipLog(t *testing.T) string {
	log := filepath.Join(t.TempDir(), "ip.log")
	fakeBins(t, map[string]string{
		"ip": `echo "$*" >> ` + log + `
case "$*" in "rule del"*) echo "RTNETLINK answers: No such file or directory" >&2; exit 2;; esac`,
	})
	return log
}

func readLog(t *testing.T, path string) []string {
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestRouteBotViaTunnel(t *testing.T) {
	log := ipLog(t)
	u := fmt.Sprintf("%d-%d", os.Getuid(), os.Getuid())

	require.NoError(t, newTestNet().RouteBot(context.Background(), true))
	require.Equal(t, []string{
		"rule del prio 101 uidrange " + u,
		"-6 rule del prio 101 uidrange " + u,
		"rule del prio 100 uidrange " + u,
		"rule add uidrange " + u + " lookup main suppress_prefixlength 0 prio 100",
		"rule add uidrange " + u + " lookup 100 prio 101",
		"-6 rule add uidrange " + u + " prio 101 unreachable",
	}, readLog(t, log))
}

func TestRouteBotDirect(t *testing.T) {
	log := ipLog(t)
	u := fmt.Sprintf("%d-%d", os.Getuid(), os.Getuid())

	require.NoError(t, newTestNet().RouteBot(context.Background(), false))
	require.Equal(t, []string{
		"rule del prio 101 uidrange " + u,
		"-6 rule del prio 101 uidrange " + u,
	}, readLog(t, log))
}

func TestRouteBotHostWithoutIPv6(t *testing.T) {
	fakeBins(t, map[string]string{
		"ip": `case "$1" in -6) echo "RTNETLINK answers: Address family not supported by protocol" >&2; exit 2;; esac`,
	})
	n := newTestNet()

	require.NoError(t, n.RouteBot(context.Background(), true))
	require.NoError(t, n.RouteBot(context.Background(), false))
}

func TestRouteBotFails(t *testing.T) {
	fakeBins(t, map[string]string{
		"ip": `case "$*" in "rule del"*) exit 0;; esac; echo "Operation not permitted" >&2; exit 2`,
	})

	err := newTestNet().RouteBot(context.Background(), true)
	require.ErrorContains(t, err, "Operation not permitted")
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
