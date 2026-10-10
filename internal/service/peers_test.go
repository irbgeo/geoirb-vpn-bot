package service

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// now is the start time of a synctest bubble, where every env-based test runs;
// time.Now() there is in the Local zone and stands still until time.Sleep.
var now = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Local()

type env struct {
	svc      *service
	peers    *fakePeers
	payments *fakePayments
	feedback *fakeFeedback
	vpn      *fakeVPN
}

func newEnv() *env {
	peers := &fakePeers{
		m: map[string]Peer{},
	}
	vpn := newFakeVPN()
	feedback := &fakeFeedback{}
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
			Feedback: feedback,
			VPN:      vpn,
			Settings: Settings{
				ServerID:     "srv",
				EndpointHost: "vpn.example.com",
				DNS:          "1.1.1.1, 1.0.0.1",
				MTU:          1380,
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
		},
	)
	return &env{
		svc:      svc,
		peers:    peers,
		payments: payments,
		feedback: feedback,
		vpn:      vpn,
	}
}

func TestIssue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()

		p := e.issue(t, 30)

		want := Peer{
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
		}
		require.Equal(t, want, e.peers.m["PUB1="], "saved in the DB")
		require.Equal(t, *want.public(), *p, "returned without secrets")
		require.True(t, e.vpn.hasPeer("PUB1="), "added on the server")
	})
}

func TestIssueForever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		p := e.issue(t, 0)
		require.True(t, p.ExpiresAt.IsZero())
	})
}

func TestIssueSkipsIPsOfDisabledPeers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		first := e.issue(t, 30)
		require.NoError(t, e.svc.Disable(context.Background(), first.PublicKey))

		second := e.issue(t, 30)
		require.Equal(t, "10.8.1.3", second.IP, ".2 stays reserved for the disabled key")
	})
}

func TestIssueDBFailureLeavesServerUntouched(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		require.False(t, e.vpn.hasPeer("PUB1="), "the server is not touched")
	})
}

func TestIssueSyncFailureRemovesDBRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.vpn.err = errBoom

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
	})
}

func TestIssueRollbackSurvivesCancelledContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx, cancel := context.WithCancel(context.Background())
		e.vpn.err = errBoom
		e.vpn.onChange = cancel // e.g. SIGTERM while syncconf runs

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
	})
}

func TestExtendActiveAddsToEndDate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestExtendExpiredStartsFromNowAndEnables(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestExtendForeverStaysForever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestUnknownKeyIsNotFound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestDisableEnable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestEnableFailsWhenIPTakenOnServer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		p := e.issue(t, 30)
		require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
		e.vpn.peers["MANUAL2="] = VPNPeer{
			PublicKey: "MANUAL2=",
			IP:        "10.8.1.2",
		}

		err := e.svc.Enable(ctx, p.PublicKey)
		require.ErrorIs(t, err, ErrIPTaken)
		require.ErrorContains(t, err, "10.8.1.2")
		require.False(t, e.peers.m[p.PublicKey].Enabled)
	})
}

func TestDelete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		p := e.issue(t, 30)

		require.NoError(t, e.svc.Delete(context.Background(), p.PublicKey))
		require.False(t, e.vpn.hasPeer(p.PublicKey))
		require.NotContains(t, e.peers.m, p.PublicKey)
	})
}

func TestClientConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		p := e.issue(t, 30)

		conf, err := e.svc.ClientConfig(context.Background(), p.PublicKey)
		require.NoError(t, err)
		require.Contains(t, conf, "Address = 10.8.1.2/32\n")
		require.Contains(t, conf, "DNS = 1.1.1.1, 1.0.0.1\n")
		require.Contains(t, conf, "MTU = 1380\n")
		require.Contains(t, conf, "PrivateKey = PRIV1=\n")
		require.Contains(t, conf, "PresharedKey = PSK1=\n")
		require.Contains(t, conf, "Endpoint = vpn.example.com\n")
	})
}

func TestReconcile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		ok := e.issue(t, 30)
		missing := e.issue(t, 30)
		disabled := e.issue(t, 30)
		require.NoError(t, e.svc.Disable(ctx, disabled.PublicKey))

		delete(e.vpn.peers, missing.PublicKey)
		e.vpn.peers[disabled.PublicKey] = VPNPeer{
			PublicKey: disabled.PublicKey,
			IP:        disabled.IP,
		}

		r, err := e.svc.Reconcile(ctx)
		require.NoError(t, err)
		require.False(t, r.OK())
		require.Equal(t, []string{missing.PublicKey}, keys(r.MissingOnServer))
		require.Equal(t, []string{disabled.PublicKey}, keys(r.DisabledButOnServer))
		require.Equal(t, 1, r.Manual)
		require.True(t, e.vpn.hasPeer(ok.PublicKey), "reconcile changes nothing")
		require.True(t, e.vpn.hasPeer(disabled.PublicKey))
	})
}

func TestReconcileReportOK(t *testing.T) {
	require.True(t, (&ReconcileReport{Manual: 3}).OK(), "manual peers are not a difference")
	missing := &ReconcileReport{
		MissingOnServer: []*Peer{
			{},
		},
	}
	require.False(t, missing.OK())
	disabled := &ReconcileReport{
		DisabledButOnServer: []*Peer{
			{},
		},
	}
	require.False(t, disabled.OK())
}

func (s *env) issue(t *testing.T, days int) *Peer {
	t.Helper()
	p, err := s.svc.Issue(
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

func keys(ps []*Peer) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.PublicKey)
	}
	return out
}

func TestIssueUsesUpPlainUsersTrial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()

		p, err := e.svc.Issue(
			ctx,
			IssueInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		require.True(t, e.users().m[42].TrialUsed)

		require.NoError(t, e.svc.Delete(ctx, p.PublicKey))
		require.ErrorIs(t, e.svc.CheckCreateKey(ctx, 42), ErrTrialUsed)
	})
}

func TestIssueKeepsTrialOfUnlimitedUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUnlimited)

		_, err := e.svc.Issue(
			context.Background(),
			IssueInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		require.False(t, e.users().m[42].TrialUsed)
	})
}

func TestExtendRollsBackServerWhenSaveFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		p := e.issue(t, 30)
		require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
		e.peers.saveErr = errBoom

		_, err := e.svc.Extend(
			ctx,
			ExtendInput{
				PublicKey: p.PublicKey,
				Days:      30,
			},
		)
		require.ErrorIs(t, err, errBoom)
		require.False(t, e.vpn.hasPeer(p.PublicKey), "not left live while the DB says disabled")
	})
}

func TestEnableRollsBackServerWhenSaveFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		p := e.issue(t, 30)
		require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
		e.peers.saveErr = errBoom

		require.ErrorIs(t, e.svc.Enable(ctx, p.PublicKey), errBoom)
		require.False(t, e.vpn.hasPeer(p.PublicKey))
	})
}

func TestEnableExpiredKeyNeedsExtend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		p := e.seed(t, now.Add(-time.Hour))
		require.NoError(t, e.svc.Disable(ctx, p.PublicKey))

		require.ErrorIs(t, e.svc.Enable(ctx, p.PublicKey), ErrExpired)
		require.False(t, e.vpn.hasPeer(p.PublicKey))
	})
}

func TestExtendFailureOnTheServerKeepsTheKeyDisabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		p := expiredKey(t, e)
		e.vpn.err = errBoom

		_, err := e.svc.Extend(
			context.Background(),
			ExtendInput{
				PublicKey: p.PublicKey,
				Days:      30,
			},
		)
		require.Error(t, err)
		require.False(t, e.vpn.hasPeer(p.PublicKey), "the DB says disabled, so the server must not run it")
		require.False(t, e.peers.m[p.PublicKey].Enabled)
	})
}

func TestExtendSaveFailureTakesTheKeyOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		p := expiredKey(t, e)
		e.peers.saveErr = errBoom

		_, err := e.svc.Extend(
			context.Background(),
			ExtendInput{
				PublicKey: p.PublicKey,
				Days:      30,
			},
		)
		require.Error(t, err)
		require.False(t, e.vpn.hasPeer(p.PublicKey))
	})
}

func TestAdminExtendUnblocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		p := e.issue(t, 30)
		ctx := context.Background()
		require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
		require.True(t, e.peers.m[p.PublicKey].Blocked)

		_, err := e.svc.Extend(
			ctx,
			ExtendInput{
				PublicKey: p.PublicKey,
				Days:      30,
			},
		)
		require.NoError(t, err)
		require.False(t, e.peers.m[p.PublicKey].Blocked, "the admin chose to give access again")
	})
}
