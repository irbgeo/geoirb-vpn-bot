package amnezia

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
func fakeBin(t *testing.T, in fakeBinInput) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, in.Name)
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+in.Script), 0o700))
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return p
}

func TestLocalRunnerExec(t *testing.T) {
	fakeBin(t, fakeBinInput{Name: "awg", Script: `echo "$@"; cat`})
	cfg := &config.Config{AWGTimeout: 5 * time.Second}

	out, err := NewLocalRunner(cfg).Exec(
		context.Background(),
		execInput{
			Args:  []string{"awg", "pubkey"},
			Stdin: "SECRET",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "pubkey\nSECRET", out)
}

func TestLocalRunnerErrorHasStderrNotStdin(t *testing.T) {
	fakeBin(t, fakeBinInput{Name: "awg", Script: `echo "bad key" >&2; exit 1`})
	cfg := &config.Config{AWGTimeout: 5 * time.Second}

	_, err := NewLocalRunner(cfg).Exec(
		context.Background(),
		execInput{
			Args:  []string{"awg", "pubkey"},
			Stdin: "SECRET",
		},
	)
	require.ErrorContains(t, err, "amnezia: awg pubkey")
	require.ErrorContains(t, err, "bad key")
	require.NotContains(t, err.Error(), "SECRET")
}
