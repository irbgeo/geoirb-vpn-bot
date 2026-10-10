package amnezia

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
)

// realHost is a server on a real config file in a temp dir: the sync and
// persist shell scripts really run, only awg is a fake that copies what
// syncconf gets into the file live.
type realHost struct {
	srv  *server
	conf string
	live string
	// editOnSync: while this file exists, the fake awg appends a line to the
	// config during syncconf (a manual edit between the two steps).
	editOnSync string
}

func newRealHost(t *testing.T) *realHost {
	t.Helper()
	return newRealHostVia(t, "")
}

// socketWrapper is an AWG_EXEC wrapper that runs the command with a socket
// as stdin, as sshd does for scripts/dev-remote.sh: /dev/stdin can't be
// opened there, only read.
func socketWrapper(t *testing.T) string {
	t.Helper()
	_, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3 on this host")
	}
	p := filepath.Join(t.TempDir(), "via-socket")
	script := `#!/usr/bin/env python3
import socket, subprocess, sys
ours, theirs = socket.socketpair()
data = sys.stdin.buffer.read()
child = subprocess.Popen(sys.argv[1:], stdin=theirs.fileno())
theirs.close()
ours.sendall(data)
ours.shutdown(socket.SHUT_WR)
sys.exit(child.wait())
`
	require.NoError(t, os.WriteFile(p, []byte(script), 0o700))
	return p
}

// newRealHostVia is newRealHost with every command run through wrapper
// (AWG_EXEC); "" = directly.
func newRealHostVia(t *testing.T, wrapper string) *realHost {
	t.Helper()
	_, err := exec.LookPath("sha256sum")
	if err != nil {
		t.Skip("no sha256sum on this host")
	}
	dir := t.TempDir()
	h := &realHost{
		conf:       filepath.Join(dir, "awg0.conf"),
		live:       filepath.Join(dir, "live"),
		editOnSync: filepath.Join(dir, "edit-on-sync"),
	}
	require.NoError(t, os.WriteFile(h.conf, []byte(serverConfText), 0o600))
	fakeBin(t, fakeBinInput{
		Name: "awg",
		// Linux can't open /dev/stdin when it is a socket (ENXIO); macOS can,
		// so the fake refuses a socket itself, as the real awg does on the server.
		Script: `[ "$1" = syncconf ] || exit 0
if [ -S "$3" ]; then echo "awg: $3: No such device or address" >&2; exit 1; fi
cat "$3" > ` + h.live + `
if [ -f ` + h.editOnSync + ` ]; then echo "# manual edit" >> ` + h.conf + `; fi`,
	})
	if exec.Command("stat", "-c", "%a", dir).Run() != nil {
		// BSD stat (macOS) has no -c: answer the two questions persist asks.
		fakeBin(t, fakeBinInput{
			Name:   "stat",
			Script: `case "$2" in %u:%g) echo "$(id -u):$(id -g)" ;; %a) echo 600 ;; esac`,
		})
	}
	cfg := &config.Config{
		AWGTimeout: 10 * time.Second,
		AWGExec:    wrapper,
	}
	h.srv, err = Open(context.Background(), NewLocalRunner(cfg), h.conf)
	require.NoError(t, err)
	return h
}

func (s *realHost) read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func addPUB3(c *serverConf) error {
	c.AddPeer(peer{
		PublicKey:    "PUB3=",
		PresharedKey: "PSK3=",
		AllowedIPs:   "10.8.1.3/32",
	})
	return nil
}

func TestUpdateOnARealFile(t *testing.T) {
	h := newRealHost(t)

	require.NoError(t, h.srv.Update(context.Background(), addPUB3))

	require.Contains(t, h.read(t, h.conf), "PublicKey = PUB3=")
	require.Contains(t, h.read(t, h.conf), "Address = 10.8.1.0/24", "the file keeps awg-quick keys")
	require.Equal(t, serverConfText, h.read(t, h.conf+".bak"), "the old file is the backup")
	require.NoFileExists(t, h.conf+".tmp")
	require.Contains(t, h.read(t, h.live), "PublicKey = PUB3=")
	require.NotContains(t, h.read(t, h.live), "Address", "syncconf gets the stripped config")
	info, err := os.Stat(h.conf)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// Through ssh (the dev wrapper) the script's stdin is a socket, which awg
// can't open as /dev/stdin: the script must hand it over through a pipe.
func TestUpdateWorksWhenStdinIsASocket(t *testing.T) {
	h := newRealHostVia(t, socketWrapper(t))

	require.NoError(t, h.srv.Update(context.Background(), addPUB3))

	require.Contains(t, h.read(t, h.live), "PublicKey = PUB3=", "awg got the config")
	require.Contains(t, h.read(t, h.conf), "PublicKey = PUB3=")
	require.NoFileExists(t, h.conf+".tmp")
}

// A failed awg must fail the update although it now runs behind a pipe.
func TestUpdateFailsWhenSyncconfFails(t *testing.T) {
	h := newRealHost(t)
	fakeBin(t, fakeBinInput{
		Name:   "awg",
		Script: `cat > /dev/null; echo "awg: bad config" >&2; exit 1`,
	})

	err := h.srv.Update(context.Background(), addPUB3)
	require.ErrorContains(t, err, "bad config")
	require.NotErrorIs(t, err, ErrNotPersisted)
	require.Equal(t, serverConfText, h.read(t, h.conf), "the file is not touched")
}

func TestUpdateRefusesAFileChangedBeforeSync(t *testing.T) {
	h := newRealHost(t)
	edited := serverConfText + "# manual edit\n"

	err := h.srv.Update(context.Background(), func(c *serverConf) error {
		require.NoError(t, os.WriteFile(h.conf, []byte(edited), 0o600))
		return addPUB3(c)
	})
	require.ErrorContains(t, err, "changed by another writer")
	require.NotErrorIs(t, err, ErrNotPersisted, "nothing went live")
	require.NoFileExists(t, h.live)
	require.Equal(t, edited, h.read(t, h.conf), "the manual edit is not overwritten")
}

func TestUpdateRefusesAFileChangedBeforeSave(t *testing.T) {
	h := newRealHost(t)
	require.NoError(t, os.WriteFile(h.editOnSync, nil, 0o600))

	err := h.srv.Update(context.Background(), addPUB3)
	require.ErrorIs(t, err, ErrNotPersisted)
	require.ErrorContains(t, err, "changed by another writer")
	require.Equal(t, serverConfText+"# manual edit\n", h.read(t, h.conf), "the manual edit is not overwritten")
	require.NoFileExists(t, h.conf+".bak")
	require.NoFileExists(t, h.conf+".tmp")
}
