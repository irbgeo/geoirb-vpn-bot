package amnezia

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

const confFile = "/etc/amnezia/amneziawg/awg0.conf"

var errBoom = errors.New("boom")

// persistPath picks the target file out of the atomic-save command.
var persistPath = regexp.MustCompile(`f="([^"]+)"`)

// box fakes the whole host for VPN: the config file,
// the live interface (syncconf calls) and the ways a command can fail.
type box struct {
	files   map[string]string
	syncs   int
	syncErr error
	onSync  func()
	// confErr fails the next write of the config; with confWritten the
	// file is written first (a command timeout after the command ran).
	confErr     error
	confWritten bool
	dump        string
	// tableTouched is set if any command mentions the old clientsTable.
	tableTouched bool
	vpn          *vpn
}

func newBox(t *testing.T) *box {
	t.Helper()
	b := &box{
		files: map[string]string{
			confFile: serverConfText,
		},
	}
	srv, err := Open(
		context.Background(),
		&fakeRunner{
			handler: b.handle,
		},
		confFile,
	)
	require.NoError(t, err)
	b.vpn = NewVPN(srv)
	t.Cleanup(func() { require.False(t, b.tableTouched, "no clientsTable commands") })
	return b
}

func (s *box) handle(in execInput) (string, error) {
	c := strings.Join(in.Args, " ")
	if strings.Contains(c, "clientsTable") {
		s.tableTouched = true
	}
	switch {
	case in.Args[0] == "test":
		return "", nil
	case strings.HasPrefix(c, "sh -c command -v"):
		return "/usr/bin/awg\n", nil
	case in.Args[0] == "cat":
		return s.files[in.Args[1]], nil
	case strings.Contains(c, "syncconf"):
		s.syncs++
		if s.onSync != nil {
			s.onSync()
		}
		return "", s.syncErr
	case strings.Contains(c, `mv "$f.tmp"`):
		path := persistPath.FindStringSubmatch(c)[1]
		if path == confFile && s.confErr != nil {
			err := s.confErr
			s.confErr = nil
			if s.confWritten {
				s.files[path] = in.Stdin
			}
			return "", err
		}
		s.files[path] = in.Stdin
		return "", nil
	case c == "awg show awg0 public-key":
		return "SERVERPUB=\n", nil
	case c == "awg show awg0 dump":
		return s.dump, nil
	}
	return "", fmt.Errorf("unexpected command %q", c)
}

func (s *box) onServer(t *testing.T, key string) bool {
	t.Helper()
	c, err := ParseServerConf(s.files[confFile])
	require.NoError(t, err)
	return c.FindPeer(key) != nil
}

func newPeer() *service.VPNPeer {
	return &service.VPNPeer{
		PublicKey: "NEW=",
		PSK:       "NEWPSK=",
		Name:      "tg:alice",
		CreatedAt: time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC),
	}
}

func addNew(ctx context.Context, b *box) (string, error) {
	var saved string
	err := b.vpn.AddPeer(
		ctx,
		&service.AddPeerInput{
			Peer: newPeer(),
			Reserved: []netip.Addr{
				netip.MustParseAddr("10.8.1.3"),
			},
			Save: func(ip string) error {
				saved = ip
				return nil
			},
		},
	)
	return saved, err
}

func TestAddPeerTakesTheLowestFreeIP(t *testing.T) {
	b := newBox(t)

	ip, err := addNew(context.Background(), b)
	require.NoError(t, err)
	require.Equal(t, "10.8.1.4", ip, ".1 and .2 are peers, .3 is reserved")
	require.Contains(t, b.files[confFile], "PublicKey = NEW=\nPresharedKey = NEWPSK=\nAllowedIPs = 10.8.1.4/32")
	require.Equal(t, 1, b.syncs)
}

func TestAddPeerSaveFailureTouchesNothing(t *testing.T) {
	b := newBox(t)

	err := b.vpn.AddPeer(
		context.Background(),
		&service.AddPeerInput{
			Peer: newPeer(),
			Save: func(string) error {
				return errBoom
			},
		},
	)
	require.ErrorIs(t, err, errBoom)
	require.Equal(t, serverConfText, b.files[confFile])
	require.Zero(t, b.syncs)
}

func TestAddPeerRemovesAPeerLeftOnTheServer(t *testing.T) {
	b := newBox(t)
	b.confErr = errBoom // the file got the peer, then the call "failed"
	b.confWritten = true

	_, err := addNew(context.Background(), b)
	require.ErrorIs(t, err, errBoom)
	require.False(t, b.onServer(t, "NEW="), "no peer without an owner")
}

func TestAddPeerRollbackResyncsWhenOnlyTheLiveInterfaceChanged(t *testing.T) {
	b := newBox(t)
	b.confErr = errBoom // syncconf ran, the file was not saved

	_, err := addNew(context.Background(), b)
	require.ErrorIs(t, err, ErrNotPersisted)
	require.Equal(t, 2, b.syncs, "rollback re-syncs from the file even though the file lacks the peer")
}

// failFirstSync makes the first syncconf fail the way a timeout does: the
// command may have run, the caller only sees an error.
func failFirstSync(b *box) {
	b.syncErr = errBoom
	b.onSync = func() {
		if b.syncs > 1 {
			b.syncErr = nil
		}
	}
}

func TestAddPeerRollbackResyncsWhenSyncItselfFailed(t *testing.T) {
	b := newBox(t)
	failFirstSync(b)

	_, err := addNew(context.Background(), b)
	require.ErrorIs(t, err, errBoom)
	require.Equal(t, 2, b.syncs, "a timed-out syncconf may have put the peer on the interface")
	require.False(t, b.onServer(t, "NEW="))
}

func TestPutPeerRollbackResyncsWhenSyncItselfFailed(t *testing.T) {
	b := newBox(t)
	failFirstSync(b)
	p := newPeer()
	p.IP = "10.8.1.5"

	require.ErrorIs(t, b.vpn.PutPeer(context.Background(), p), errBoom)
	require.Equal(t, 2, b.syncs)
}

func TestReplacePeerRollbackResyncsWhenSyncItselfFailed(t *testing.T) {
	b := newBox(t)
	failFirstSync(b)

	require.ErrorIs(t, b.vpn.ReplacePeer(context.Background(), replaceInput()), errBoom)
	require.Greater(t, b.syncs, 1, "the interface is put back on the file")
	require.True(t, b.onServer(t, "PUB1="))
	require.False(t, b.onServer(t, "NEW="))
}

func TestAddPeerRollbackSurvivesCancelledContext(t *testing.T) {
	b := newBox(t)
	ctx, cancel := context.WithCancel(context.Background())
	b.onSync = cancel // SIGTERM while syncconf runs: the file is not saved

	_, err := addNew(ctx, b)
	require.ErrorIs(t, err, ErrNotPersisted)
	require.Equal(t, 2, b.syncs, "rollback still re-synced the live interface")
}

func TestPutPeerRefusesATakenIP(t *testing.T) {
	b := newBox(t)
	p := newPeer()
	p.IP = "10.8.1.2" // PUB2= holds it

	err := b.vpn.PutPeer(context.Background(), p)
	require.ErrorIs(t, err, service.ErrIPTaken)
	require.ErrorContains(t, err, "10.8.1.2")
	require.Equal(t, serverConfText, b.files[confFile])
}

func TestPutPeerRefusesAnIPHeldInAnyForm(t *testing.T) {
	for _, allowed := range []string{"10.8.1.5", "10.8.1.0/29", "fd00::5/128, 10.8.1.5/32"} {
		b := newBox(t)
		b.files[confFile] += "\n[Peer]\nPublicKey = OTHER=\nAllowedIPs = " + allowed + "\n"
		before := b.files[confFile]
		p := newPeer()
		p.IP = "10.8.1.5"

		require.ErrorIs(t, b.vpn.PutPeer(context.Background(), p), service.ErrIPTaken, allowed)
		require.Equal(t, before, b.files[confFile])
	}
}

func TestPutPeerRefusesABadIP(t *testing.T) {
	b := newBox(t)
	p := newPeer()
	p.IP = "not-an-ip"

	require.ErrorContains(t, b.vpn.PutPeer(context.Background(), p), "not-an-ip")
	require.Equal(t, serverConfText, b.files[confFile])
}

func TestPutPeerIgnoresSimilarLookingIP(t *testing.T) {
	b := newBox(t)
	b.files[confFile] += "\n[Peer]\nPublicKey = OTHER=\nAllowedIPs = 110.8.1.5/32\n"
	p := newPeer()
	p.IP = "10.8.1.5"

	require.NoError(t, b.vpn.PutPeer(context.Background(), p), "110.8.1.5 is not 10.8.1.5")
	require.True(t, b.onServer(t, "NEW="))
}

func TestPutPeerTakesThePeerOffWhenItTimesOut(t *testing.T) {
	b := newBox(t)
	b.confErr = errBoom
	b.confWritten = true
	p := newPeer()
	p.IP = "10.8.1.5"

	require.ErrorIs(t, b.vpn.PutPeer(context.Background(), p), errBoom)
	require.False(t, b.onServer(t, "NEW="))
}

func TestPutPeerFailureKeepsAPeerThatWasAlreadyThere(t *testing.T) {
	b := newBox(t)
	b.confErr = errBoom // syncconf ran, the file was not saved
	p := &service.VPNPeer{
		PublicKey: "PUB1=",
		PSK:       "PSK1=",
		IP:        "10.8.1.1",
	}

	require.ErrorIs(t, b.vpn.PutPeer(context.Background(), p), ErrNotPersisted)
	require.True(t, b.onServer(t, "PUB1="), "the call added nothing, so it takes nothing off")
}

func TestRemovePeer(t *testing.T) {
	b := newBox(t)

	require.NoError(
		t,
		b.vpn.RemovePeer(
			context.Background(),
			&service.VPNPeer{
				PublicKey: "PUB1=",
				IP:        "10.8.1.1",
			},
		),
	)
	require.False(t, b.onServer(t, "PUB1="))
	require.True(t, b.onServer(t, "PUB2="))
}

func replaceInput() *service.ReplacePeerInput {
	p := newPeer()
	p.IP = "10.8.1.1"
	return &service.ReplacePeerInput{
		Old: &service.VPNPeer{
			PublicKey: "PUB1=",
			PSK:       "PSK1=",
			IP:        "10.8.1.1",
			Name:      "Admin <iOS>",
		},
		New: p,
	}
}

func TestReplacePeer(t *testing.T) {
	b := newBox(t)

	require.NoError(t, b.vpn.ReplacePeer(context.Background(), replaceInput()))
	require.False(t, b.onServer(t, "PUB1="))
	require.Contains(t, b.files[confFile], "PublicKey = NEW=\nPresharedKey = NEWPSK=\nAllowedIPs = 10.8.1.1/32")
	require.Equal(t, 1, b.syncs, "one step")
}

func TestReplacePeerFailureKeepsTheOldPeer(t *testing.T) {
	b := newBox(t)
	b.confErr = errBoom
	b.confWritten = true

	require.ErrorIs(t, b.vpn.ReplacePeer(context.Background(), replaceInput()), errBoom)
	require.True(t, b.onServer(t, "PUB1="), "the old key still works")
	require.False(t, b.onServer(t, "NEW="))
}

func TestPeerKeysAndSubnetUsage(t *testing.T) {
	b := newBox(t)
	ctx := context.Background()

	keys, err := b.vpn.PeerKeys(ctx)
	require.NoError(t, err)
	require.Equal(
		t,
		[]string{
			"PUB1=",
			"PUB2=",
		},
		keys,
	)

	used, total, err := b.vpn.SubnetUsage(
		ctx,
		[]netip.Addr{
			netip.MustParseAddr("10.8.1.9"),
		},
	)
	require.NoError(t, err)
	require.Equal(t, 3, used)
	require.Equal(t, 254, total)
}

func TestStatsAreFromTheClientSide(t *testing.T) {
	b := newBox(t)
	b.dump = "SERVERPRIV=\tSERVERPUB=\t443\toff\n" +
		"PUB1=\tPSK1=\t1.2.3.4:5000\t10.8.1.1/32\t1790505566\t100\t200\toff\n"

	st, err := b.vpn.Stats(context.Background())
	require.NoError(t, err)
	require.Equal(
		t,
		[]service.PeerStat{
			{
				PublicKey:     "PUB1=",
				LastHandshake: time.Unix(1790505566, 0),
				Sent:          100, // the server received it
				Received:      200,
			},
		},
		st,
	)
}

func TestClientConfig(t *testing.T) {
	b := newBox(t)

	conf, err := b.vpn.ClientConfig(
		context.Background(),
		&service.ClientSpec{
			IP:           "10.8.1.2",
			PrivateKey:   "PRIV2=",
			PSK:          "PSK2=",
			DNS:          "1.1.1.1, 1.0.0.1",
			MTU:          1380,
			EndpointHost: "vpn.example.com",
		},
	)
	require.NoError(t, err)
	require.Contains(t, conf, "Address = 10.8.1.2/32\n")
	require.Contains(t, conf, "MTU = 1380\n")
	require.Contains(t, conf, "DNS = 1.1.1.1, 1.0.0.1\n")
	require.Contains(t, conf, "PrivateKey = PRIV2=\n")
	require.Contains(t, conf, "I1 = <r 2><b 0x8580>\n", "commented server I1 is active for the client")
	require.Contains(t, conf, "PublicKey = SERVERPUB=\n")
	require.Contains(t, conf, "PresharedKey = PSK2=\n")
	require.Contains(t, conf, "Endpoint = vpn.example.com:443\n")
}
