package hostexec

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
)

// fakeBin writes an executable shell script called name into a temp dir and
// puts that dir first on PATH.
func fakeBin(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o700))
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return p
}

func TestRunExec(t *testing.T) {
	fakeBin(t, "awg", `echo "$@"; cat`)
	cfg := &config.Config{AWGTimeout: 5 * time.Second}

	out, err := New(cfg).Run(
		context.Background(),
		Input{
			Args:  []string{"awg", "pubkey"},
			Stdin: "SECRET",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "pubkey\nSECRET", out)
}

func TestRunErrorHasStderrNotStdin(t *testing.T) {
	fakeBin(t, "awg", `echo "bad key" >&2; exit 1`)
	cfg := &config.Config{AWGTimeout: 5 * time.Second}

	_, err := New(cfg).Run(
		context.Background(),
		Input{
			Args:  []string{"awg", "pubkey"},
			Stdin: "SECRET",
		},
	)
	require.ErrorContains(t, err, "awg pubkey")
	require.ErrorContains(t, err, "bad key")
	require.NotContains(t, err.Error(), "SECRET")
}

func TestRunTimeout(t *testing.T) {
	fakeBin(t, "slow", `exec sleep 5`)
	cfg := &config.Config{AWGTimeout: 50 * time.Millisecond}

	start := time.Now()
	_, err := New(cfg).Run(
		context.Background(),
		Input{Args: []string{"slow"}},
	)
	require.ErrorContains(t, err, "timed out")
	require.Less(t, time.Since(start), 3*time.Second)
}

func TestRunWrapperGetsArgsFirst(t *testing.T) {
	wrapper := fakeBin(t, "wrap", `echo "wrapped $@"; cat`)
	cfg := &config.Config{
		AWGTimeout: 5 * time.Second,
		AWGExec:    wrapper,
	}

	out, err := New(cfg).Run(
		context.Background(),
		Input{
			Args:  []string{"awg", "show"},
			Stdin: "IN",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "wrapped awg show\nIN", out)
}

func TestRunTimeoutKillsGrandchildren(t *testing.T) {
	cfg := &config.Config{AWGTimeout: 50 * time.Millisecond}

	start := time.Now()
	_, err := New(cfg).Run(
		context.Background(),
		Input{Args: []string{"sh", "-c", "sleep 5; true"}},
	)
	require.ErrorContains(t, err, "timed out")
	require.Less(t, time.Since(start), time.Second)
}
