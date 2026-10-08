package amnezia

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeRunner answers container commands from a handler and records calls.
type fakeRunner struct {
	mu      sync.Mutex
	calls   []execInput
	handler func(in execInput) (string, error)
}

func (s *fakeRunner) Exec(ctx context.Context, in execInput) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, in)
	s.mu.Unlock()
	err := ctx.Err()
	if err != nil {
		return "", err // like docker: a cancelled call does not run
	}
	return s.handler(in)
}

func (s *fakeRunner) cmds() []string {
	out := make([]string, 0, len(s.calls))
	for _, c := range s.calls {
		out = append(out, strings.Join(c.Args, " "))
	}
	return out
}

// awgContainer fakes a container with awg0.conf and the awg tool.
func awgContainer(conf *string) *fakeRunner {
	return &fakeRunner{handler: func(in execInput) (string, error) {
		cmd := strings.Join(in.Args, " ")
		switch {
		case cmd == "ls /opt/amnezia/awg":
			return "awg0.conf\nclientsTable\nwireguard_psk.key\n", nil
		case strings.HasPrefix(cmd, "sh -c command -v"):
			return "/usr/bin/awg\n", nil
		case cmd == "cat /opt/amnezia/awg/awg0.conf":
			return *conf, nil
		case strings.HasPrefix(cmd, "sh -c") && strings.Contains(cmd, "mv"):
			*conf = in.Stdin
			return "", nil
		}
		return "", nil
	}}
}

func TestOpenDetectsLayout(t *testing.T) {
	conf := serverConfText
	s, err := Open(context.Background(), awgContainer(&conf))
	require.NoError(t, err)
	require.Equal(t, "awg0", s.iface)
	require.Equal(t, "/opt/amnezia/awg/awg0.conf", s.confPath)
	require.Equal(t, "awg", s.tool)
}

func TestOpenOldWireGuardLayout(t *testing.T) {
	r := &fakeRunner{handler: func(in execInput) (string, error) {
		if in.Args[0] == "ls" {
			return "wg0.conf\nclientsTable\n", nil
		}
		return "/usr/bin/wg\n", nil
	}}
	s, err := Open(context.Background(), r)
	require.NoError(t, err)
	require.Equal(t, "wg0", s.iface)
	require.Equal(t, "wg", s.tool)
}

func TestOpenFailsWithoutConf(t *testing.T) {
	r := &fakeRunner{handler: func(execInput) (string, error) { return "clientsTable\n", nil }}
	_, err := Open(context.Background(), r)
	require.ErrorContains(t, err, "no awg0.conf or wg0.conf")
}

func TestGenKeys(t *testing.T) {
	r := &fakeRunner{handler: func(in execInput) (string, error) {
		switch in.Args[1] {
		case "genkey":
			return "PRIV=\n", nil
		case "pubkey":
			require.Equal(t, "PRIV=", in.Stdin, "private key goes via stdin, not argv")
			return "PUB=\n", nil
		case "genpsk":
			return "PSK=\n", nil
		}
		return "", errors.New("unexpected")
	}}
	s := &Server{
		run:  r,
		tool: "awg",
	}

	k, err := s.GenKeys(context.Background())
	require.NoError(t, err)
	require.Equal(
		t,
		keys{
			Private: "PRIV=",
			Public:  "PUB=",
			PSK:     "PSK=",
		},
		k,
	)
}

func TestUpdateSyncsThenPersists(t *testing.T) {
	conf := serverConfText
	r := awgContainer(&conf)
	s, err := Open(context.Background(), r)
	require.NoError(t, err)
	r.calls = nil

	err = s.Update(context.Background(), func(c *serverConf) error {
		c.AddPeer(
			peer{
				PublicKey:    "PUB3=",
				PresharedKey: "PSK3=",
				AllowedIPs:   "10.8.1.3/32",
			},
		)
		return nil
	})
	require.NoError(t, err)

	require.Len(t, r.calls, 3, "read, syncconf, persist")
	sync := r.calls[1]
	require.Contains(t, sync.Args[2], "awg syncconf awg0")
	require.NotContains(t, sync.Stdin, "Address", "syncconf gets the stripped config")
	require.Contains(t, sync.Stdin, "PUB3=")

	persist := r.calls[2]
	require.Contains(t, persist.Args[2], `cp -p "$f" "$f.bak"`, "backup before write")
	require.Contains(t, persist.Args[2], `mv "$f.tmp" "$f"`, "atomic replace")
	require.Contains(t, conf, "Address = 10.8.1.0/24", "file keeps awg-quick keys")
	require.Contains(t, conf, "PUB3=")
	for _, c := range r.calls {
		require.NotContains(t, strings.Join(c.Args, " "), "PSK3=", "secrets never in argv")
	}
}

func TestUpdateCallbackErrorWritesNothing(t *testing.T) {
	conf := serverConfText
	r := awgContainer(&conf)
	s, err := Open(context.Background(), r)
	require.NoError(t, err)
	r.calls = nil

	err = s.Update(context.Background(), func(*serverConf) error { return errors.New("boom") })
	require.ErrorContains(t, err, "boom")
	require.Equal(t, []string{"cat /opt/amnezia/awg/awg0.conf"}, r.cmds())
}

func TestUpdateSyncFailureKeepsFile(t *testing.T) {
	conf := serverConfText
	r := awgContainer(&conf)
	inner := r.handler
	r.handler = func(in execInput) (string, error) {
		if strings.Contains(strings.Join(in.Args, " "), "syncconf") {
			return "", errors.New("syncconf failed")
		}
		return inner(in)
	}
	s, err := Open(context.Background(), r)
	require.NoError(t, err)

	err = s.Update(context.Background(), func(c *serverConf) error {
		c.RemovePeer("PUB1=")
		return nil
	})
	require.ErrorContains(t, err, "syncconf failed")
	require.Equal(t, serverConfText, conf, "file untouched when live apply fails")
}

func TestStats(t *testing.T) {
	dump := "SERVERPRIV=\tSERVERPUB=\t443\toff\n" +
		"PUB1=\tPSK1=\t1.2.3.4:5000\t10.8.1.1/32\t1790505566\t100\t200\toff\n" +
		"PUB2=\tPSK2=\t(none)\t10.8.1.2/32\t0\t0\t0\toff\n"
	r := &fakeRunner{handler: func(in execInput) (string, error) {
		require.Equal(t, []string{"awg", "show", "awg0", "dump"}, in.Args)
		return dump, nil
	}}
	s := &Server{
		run:   r,
		tool:  "awg",
		iface: "awg0",
	}

	st, err := s.Stats(context.Background())
	require.NoError(t, err)
	require.Equal(
		t,
		[]peerStat{
			{
				PublicKey:       "PUB1=",
				Endpoint:        "1.2.3.4:5000",
				AllowedIPs:      "10.8.1.1/32",
				LatestHandshake: time.Unix(1790505566, 0),
				RX:              100,
				TX:              200,
			},
			{
				PublicKey:  "PUB2=",
				AllowedIPs: "10.8.1.2/32",
			},
		},
		st,
	)
}

func TestServerPublicKey(t *testing.T) {
	r := &fakeRunner{handler: func(in execInput) (string, error) {
		require.Equal(t, []string{"awg", "show", "awg0", "public-key"}, in.Args)
		return "SERVERPUB=\n", nil
	}}
	s := &Server{
		run:   r,
		tool:  "awg",
		iface: "awg0",
	}

	k, err := s.ServerPublicKey(context.Background())
	require.NoError(t, err)
	require.Equal(t, "SERVERPUB=", k)
}

func TestUpdateRefusesAConfigChangedByAnotherWriter(t *testing.T) {
	conf := serverConfText
	r := awgContainer(&conf)
	s, err := Open(context.Background(), r)
	require.NoError(t, err)
	r.calls = nil

	require.NoError(t, s.Update(context.Background(), func(c *serverConf) error {
		c.RemovePeer("PUB1=")
		return nil
	}))

	want := sha256Hex(serverConfText)
	sync, persist := r.calls[1].Args[2], r.calls[2].Args[2]
	require.Contains(t, sync, want, "syncconf runs only on the file we read")
	require.Contains(t, persist, want, "mv runs only on the file we read")
	require.Contains(t, sync, "sha256sum")
}
