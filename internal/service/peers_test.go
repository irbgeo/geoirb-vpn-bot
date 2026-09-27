package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var now = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

type env struct {
	svc      *Service
	peers    *fakePeers
	payments *fakePayments
	vpn      *fakeVPN
}

func newEnv() *env {
	peers := &fakePeers{
		m: map[string]Peer{},
	}
	vpn := newFakeVPN()
	payments := &fakePayments{
		m: map[string]Payment{},
	}
	svc := New(
		&Deps{
			Users: &fakeUsers{
				m: map[int64]User{},
			},
			Peers:    peers,
			Payments: payments,
			VPN:      vpn,
			Settings: Settings{
				ServerID:     "srv",
				EndpointHost: "vpn.example.com",
				DNS:          "1.1.1.1, 1.0.0.1",
				TrialDays:    7,
				Tariffs: []Tariff{
					{
						Days:  30,
						Stars: 150,
					},
					{
						Days:  90,
						Stars: 400,
					},
				},
			},
			Now: func() time.Time { return now },
		},
	)
	return &env{
		svc:      svc,
		peers:    peers,
		payments: payments,
		vpn:      vpn,
	}
}

func (e *env) issue(t *testing.T, days int) *Peer {
	t.Helper()
	p, err := e.svc.Issue(
		context.Background(),
		IssueInput{
			UserID: 42,
			Name:   "tg:alice",
			Days:   days,
		},
	)
	require.NoError(t, err)
	return p
}

func TestIssue(t *testing.T) {
	e := newEnv()

	p := e.issue(t, 30)

	require.Equal(
		t,
		Peer{
			PublicKey:  "PUB1=",
			ServerID:   "srv",
			UserID:     42,
			Name:       "tg:alice",
			IP:         "10.8.1.2",
			PrivateKey: "PRIV1=",
			PSK:        "PSK1=",
			Enabled:    true,
			ExpiresAt:  now.AddDate(0, 0, 30),
			CreatedAt:  now,
		},
		*p,
	)
	require.Equal(t, *p, e.peers.m["PUB1="], "saved in the DB")
	require.True(t, e.vpn.hasPeer("PUB1="), "added on the server")
	require.Equal(t, "tg:alice", e.vpn.table["PUB1="], "visible in the Amnezia app")
}

func TestIssueForever(t *testing.T) {
	e := newEnv()
	p := e.issue(t, 0)
	require.True(t, p.ExpiresAt.IsZero())
}

func TestIssueSkipsIPsOfDisabledPeers(t *testing.T) {
	e := newEnv()
	first := e.issue(t, 30)
	require.NoError(t, e.svc.Disable(context.Background(), first.PublicKey))

	second := e.issue(t, 30)
	require.Equal(t, "10.8.1.3", second.IP, ".2 stays reserved for the disabled key")
}

func TestIssueDBFailureLeavesServerUntouched(t *testing.T) {
	e := newEnv()
	e.peers.saveErr = errBoom

	_, err := e.svc.Issue(
		context.Background(),
		IssueInput{
			UserID: 42,
			Name:   "tg:alice",
			Days:   30,
		},
	)
	require.ErrorIs(t, err, errBoom)
	require.Equal(t, fakeServerConf, e.vpn.conf)
}

func TestIssueSyncFailureRemovesDBRecord(t *testing.T) {
	e := newEnv()
	e.vpn.syncErr = errBoom

	_, err := e.svc.Issue(
		context.Background(),
		IssueInput{
			UserID: 42,
			Name:   "tg:alice",
			Days:   30,
		},
	)
	require.ErrorIs(t, err, errBoom)
	require.Empty(t, e.peers.m, "no DB record for a key the server doesn't have")
}

func TestIssueRollbackSurvivesCancelledContext(t *testing.T) {
	e := newEnv()
	ctx, cancel := context.WithCancel(context.Background())
	e.vpn.syncErr = errBoom
	e.vpn.onUpdate = cancel // e.g. SIGTERM while syncconf runs

	_, err := e.svc.Issue(
		ctx,
		IssueInput{
			UserID: 42,
			Name:   "tg:alice",
			Days:   30,
		},
	)
	require.ErrorIs(t, err, errBoom)
	require.Empty(t, e.peers.m, "rollback still ran")
}

func TestIssueClientsTableFailureIsNotFatal(t *testing.T) {
	e := newEnv()
	e.vpn.tableErr = errBoom

	p := e.issue(t, 30)
	require.True(t, e.vpn.hasPeer(p.PublicKey), "the key works; only the app list is missing it")
}

func TestExtendActiveAddsToEndDate(t *testing.T) {
	e := newEnv()
	p := e.issue(t, 30)
	p.Reminded3d = true
	p.Reminded1d = true
	require.NoError(t, e.peers.Save(context.Background(), p))

	got, err := e.svc.Extend(
		context.Background(),
		ExtendInput{
			PublicKey: p.PublicKey,
			Days:      90,
		},
	)
	require.NoError(t, err)
	require.Equal(t, now.AddDate(0, 0, 30+90), got.ExpiresAt)
	require.False(t, got.Reminded3d, "reminders reset for the new date")
	require.False(t, got.Reminded1d)
}

func TestExtendExpiredStartsFromNowAndEnables(t *testing.T) {
	e := newEnv()
	p := e.issue(t, 30)
	require.NoError(t, e.svc.Disable(context.Background(), p.PublicKey))
	stored := e.peers.m[p.PublicKey]
	stored.ExpiresAt = now.AddDate(0, 0, -5)
	e.peers.m[p.PublicKey] = stored

	got, err := e.svc.Extend(
		context.Background(),
		ExtendInput{
			PublicKey: p.PublicKey,
			Days:      30,
		},
	)
	require.NoError(t, err)
	require.Equal(t, now.AddDate(0, 0, 30), got.ExpiresAt)
	require.True(t, got.Enabled)
	require.Equal(t, "10.8.1.2", got.IP, "same key, same IP")
	require.True(t, e.vpn.hasPeer(p.PublicKey))
}

func TestExtendForeverStaysForever(t *testing.T) {
	e := newEnv()
	p := e.issue(t, 0)

	got, err := e.svc.Extend(
		context.Background(),
		ExtendInput{
			PublicKey: p.PublicKey,
			Days:      30,
		},
	)
	require.NoError(t, err)
	require.True(t, got.ExpiresAt.IsZero())
}

func TestUnknownKeyIsNotFound(t *testing.T) {
	e := newEnv()
	ctx := context.Background()

	_, err := e.svc.Extend(
		ctx,
		ExtendInput{
			PublicKey: "NOPE=",
			Days:      30,
		},
	)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, e.svc.Disable(ctx, "MANUAL1="), ErrNotFound, "peers made in the app are not ours")
	require.ErrorIs(t, e.svc.Delete(ctx, "MANUAL1="), ErrNotFound)
	require.True(t, e.vpn.hasPeer("MANUAL1="), "manual peer untouched")
}

func TestDisableEnable(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	p := e.issue(t, 30)

	require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
	require.False(t, e.vpn.hasPeer(p.PublicKey))
	require.False(t, e.peers.m[p.PublicKey].Enabled)
	require.NoError(t, e.svc.Disable(ctx, p.PublicKey), "disabling twice is fine")

	require.NoError(t, e.svc.Enable(ctx, p.PublicKey))
	require.True(t, e.vpn.hasPeer(p.PublicKey))
	require.True(t, e.peers.m[p.PublicKey].Enabled)
	require.NoError(t, e.svc.Enable(ctx, p.PublicKey), "enabling twice is fine")
}

func TestEnableFailsWhenIPTakenOnServer(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	p := e.issue(t, 30)
	require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
	e.vpn.conf += "\n[Peer]\nPublicKey = MANUAL2=\nAllowedIPs = 10.8.1.2/32\n"

	require.ErrorContains(t, e.svc.Enable(ctx, p.PublicKey), "10.8.1.2 is taken")
	require.False(t, e.peers.m[p.PublicKey].Enabled)
}

func TestEnableIgnoresSimilarLookingIP(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	p := e.issue(t, 30)
	require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
	e.vpn.conf += "\n[Peer]\nPublicKey = OTHER=\nAllowedIPs = 110.8.1.2/32\n"

	require.NoError(t, e.svc.Enable(ctx, p.PublicKey), "110.8.1.2 is not 10.8.1.2")
}

func TestDelete(t *testing.T) {
	e := newEnv()
	p := e.issue(t, 30)

	require.NoError(t, e.svc.Delete(context.Background(), p.PublicKey))
	require.False(t, e.vpn.hasPeer(p.PublicKey))
	require.NotContains(t, e.vpn.table, p.PublicKey)
	require.NotContains(t, e.peers.m, p.PublicKey)
}

func TestClientConfig(t *testing.T) {
	e := newEnv()
	p := e.issue(t, 30)

	conf, err := e.svc.ClientConfig(context.Background(), p.PublicKey)
	require.NoError(t, err)
	require.Contains(t, conf, "Address = 10.8.1.2/32\n")
	require.Contains(t, conf, "DNS = 1.1.1.1, 1.0.0.1\n")
	require.Contains(t, conf, "PrivateKey = PRIV1=\n")
	require.Contains(t, conf, "I1 = <r 2>\n", "commented server I1 is active for the client")
	require.Contains(t, conf, "PublicKey = SERVERPUB=\n")
	require.Contains(t, conf, "PresharedKey = PSK1=\n")
	require.Contains(t, conf, "Endpoint = vpn.example.com:443\n")
}

func TestReconcile(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	ok := e.issue(t, 30)
	missing := e.issue(t, 30)
	disabled := e.issue(t, 30)
	require.NoError(t, e.svc.Disable(ctx, disabled.PublicKey))

	c, err := e.vpn.ReadConf(ctx)
	require.NoError(t, err)
	c.RemovePeer(missing.PublicKey)
	c.AddPeer(c.Peers[0]) // stand-in for the disabled key still on the server
	c.Peers[len(c.Peers)-1].PublicKey = disabled.PublicKey
	e.vpn.conf = c.String()

	r, err := e.svc.Reconcile(ctx)
	require.NoError(t, err)
	require.False(t, r.OK())
	require.Equal(t, []string{missing.PublicKey}, keys(r.MissingOnServer))
	require.Equal(t, []string{disabled.PublicKey}, keys(r.DisabledButOnServer))
	require.Equal(t, 1, r.Manual)
	require.True(t, e.vpn.hasPeer(ok.PublicKey), "reconcile changes nothing")
	require.True(t, e.vpn.hasPeer(disabled.PublicKey))
}

func keys(ps []*Peer) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.PublicKey)
	}
	return out
}
