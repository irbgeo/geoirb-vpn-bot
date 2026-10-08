package amnezia

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeDocker writes a shell script that stands in for the docker binary.
func fakeDocker(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "docker")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o700))
	return p
}

func TestDockerRunnerExec(t *testing.T) {
	r := &DockerRunner{
		Bin:       fakeDocker(t, `echo "$@"; cat`),
		Container: "amnezia-awg2",
		Timeout:   5 * time.Second,
	}

	out, err := r.Exec(
		context.Background(),
		execInput{
			Args:  []string{"awg", "pubkey"},
			Stdin: "SECRET",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "exec -i amnezia-awg2 awg pubkey\nSECRET", out)
}

func TestDockerRunnerErrorHasStderrNotStdin(t *testing.T) {
	r := &DockerRunner{
		Bin:       fakeDocker(t, `echo "no such container" >&2; exit 1`),
		Container: "x",
		Timeout:   5 * time.Second,
	}

	_, err := r.Exec(
		context.Background(),
		execInput{
			Args:  []string{"awg", "pubkey"},
			Stdin: "SECRET",
		},
	)
	require.ErrorContains(t, err, "awg pubkey")
	require.ErrorContains(t, err, "no such container")
	require.NotContains(t, err.Error(), "SECRET")
}

func TestDockerRunnerTimeout(t *testing.T) {
	r := &DockerRunner{
		Bin:       fakeDocker(t, `exec sleep 5`),
		Container: "x",
		Timeout:   100 * time.Millisecond,
	}

	start := time.Now()
	_, err := r.Exec(context.Background(), execInput{Args: []string{"true"}})
	require.ErrorContains(t, err, "timed out")
	require.Less(t, time.Since(start), 3*time.Second)
}

func TestDetectContainer(t *testing.T) {
	bin := fakeDocker(t, `printf 'mongo\namnezia-awg\namnezia-awg2\n'`)

	name, err := DetectContainer(context.Background(), bin)
	require.NoError(t, err)
	require.Equal(t, "amnezia-awg2", name, "newer container wins")

	bin = fakeDocker(t, `printf 'mongo\n'`)
	_, err = DetectContainer(context.Background(), bin)
	require.ErrorContains(t, err, "no amnezia-awg2 or amnezia-awg")
}
