package service

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadsDoNotReturnSecrets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		e.register(t, RoleUser)

		issued := e.issue(t, 30)
		noSecrets(t, issued)
		require.NotEmpty(t, e.peers.m[issued.PublicKey].PSK, "the DB keeps them")

		extended, err := e.svc.Extend(
			ctx,
			ExtendInput{
				PublicKey: issued.PublicKey,
				Days:      1,
			},
		)
		require.NoError(t, err)
		noSecrets(t, extended)

		key, err := e.svc.Key(ctx, issued.PublicKey)
		require.NoError(t, err)
		noSecrets(t, key)

		own := UserKey{
			UserID:    42,
			PublicKey: issued.PublicKey,
		}
		kc, err := e.svc.UserConfig(ctx, own)
		require.NoError(t, err)
		noSecrets(t, kc.Peer)

		infos, err := e.svc.Access(ctx, 42)
		require.NoError(t, err)
		for _, k := range infos {
			noSecrets(t, k.Peer)
		}

		st, err := e.svc.Stats(ctx)
		require.NoError(t, err)
		for _, k := range st.TopTraffic {
			noSecrets(t, k.Peer)
		}

		reissued, err := e.svc.ReissueKey(ctx, own)
		require.NoError(t, err)
		noSecrets(t, reissued)

		res := e.pay(t, "c1")
		noSecrets(t, res.Peer)
		repeat := e.pay(t, "c1")
		noSecrets(t, repeat.Peer)

		e.setRole(roleInput{ID: 42, Role: RoleUnlimited})
		created, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		noSecrets(t, created)
	})
}

func TestMaintainAndReconcileDoNotReturnSecrets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		e.seed(t, now.Add(-time.Minute))
		remind := e.seed(t, now.Add(12*time.Hour))

		m, err := e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Len(t, m.Expired, 1)
		require.Len(t, m.Remind1d, 1)
		noSecrets(t, m.Expired...)
		noSecrets(t, m.Remind1d...)

		stored := e.peers.m[remind.PublicKey]
		stored.Enabled = false // disabled in the DB, still on the server
		e.peers.m[remind.PublicKey] = stored

		r, err := e.svc.Reconcile(ctx)
		require.NoError(t, err)
		require.Len(t, r.DisabledButOnServer, 1)
		noSecrets(t, r.DisabledButOnServer...)
	})
}

// noSecrets fails if a key read from the service carries its private key
// or PSK: they stay in the service, only a rendered config leaves it.
func noSecrets(t *testing.T, ps ...*Peer) {
	t.Helper()
	for _, p := range ps {
		require.NotNil(t, p)
		require.Empty(t, p.PrivateKey, "private key of %s left the service", p.PublicKey)
		require.Empty(t, p.PSK, "PSK of %s left the service", p.PublicKey)
	}
}
