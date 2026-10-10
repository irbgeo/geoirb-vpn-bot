package service

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccessJoinsKeysWithLiveStats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUnlimited)
		ctx := context.Background()
		online, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		idle, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		disabled, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		require.NoError(t, e.svc.Disable(ctx, disabled.PublicKey))
		e.vpn.stats = []PeerStat{
			{
				PublicKey:     online.PublicKey,
				LastHandshake: now.Add(-time.Minute),
				Sent:          100,
				Received:      2000,
			},
			{
				PublicKey:     idle.PublicKey,
				LastHandshake: now.Add(-time.Hour),
			},
		}

		keys, err := e.svc.Access(ctx, 42)
		require.NoError(t, err)
		require.Len(t, keys, 3)
		byKey := map[string]KeyInfo{}
		for _, k := range keys {
			byKey[k.Peer.PublicKey] = k
		}

		require.True(t, byKey[online.PublicKey].Online, "handshake under 3 minutes")
		require.Equal(t, now.Add(-time.Minute), byKey[online.PublicKey].LastHandshake)
		require.Equal(t, int64(100), byKey[online.PublicKey].Sent)
		require.Equal(t, int64(2000), byKey[online.PublicKey].Received)
		require.False(t, byKey[idle.PublicKey].Online)
		require.True(t, byKey[disabled.PublicKey].LastHandshake.IsZero(), "not on the server")
	})
}

func TestAccessSortedByIP(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUnlimited)
		ctx := context.Background()
		for i := 0; i < 3; i++ {
			_, err := e.svc.CreateKey(
				ctx,
				CreateKeyInput{
					UserID: 42,
				},
			)
			require.NoError(t, err)
		}

		keys, err := e.svc.Access(ctx, 42)
		require.NoError(t, err)
		require.Equal(t, "10.8.1.2", keys[0].Peer.IP)
		require.Equal(t, "10.8.1.3", keys[1].Peer.IP)
		require.Equal(t, "10.8.1.4", keys[2].Peer.IP)
	})
}

func TestAccessWhenStatsFailReturnsKeysFlagged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		_, err := e.svc.CreateKey(ctx, CreateKeyInput{UserID: 42})
		require.NoError(t, err)
		e.vpn.statsErr = errors.New("awg down")

		keys, err := e.svc.Access(ctx, 42)

		require.NoError(t, err)
		require.Len(t, keys, 1)
		require.True(t, keys[0].StatsUnavailable)
		require.False(t, keys[0].Online)
	})
}

func TestUserConfigOnlyForOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		p, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)

		kc, err := e.svc.UserConfig(
			ctx,
			UserKey{
				UserID:    42,
				PublicKey: p.PublicKey,
			},
		)
		require.NoError(t, err)
		require.Equal(t, p.PublicKey, kc.Peer.PublicKey)
		require.Contains(t, kc.Conf, "PrivateKey = "+p.PrivateKey)

		_, err = e.svc.UserConfig(
			ctx,
			UserKey{
				UserID:    7,
				PublicKey: p.PublicKey,
			},
		)
		require.ErrorIs(t, err, ErrNotFound, "someone else's key looks like no key")
	})
}

func TestConfigOfImportedKeyWithoutPrivateKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		p, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		stored := e.peers.m[p.PublicKey]
		stored.PrivateKey = "" // imported from the Amnezia app: the device keeps it
		e.peers.m[p.PublicKey] = stored

		_, err = e.svc.UserConfig(
			ctx,
			UserKey{
				UserID:    42,
				PublicKey: p.PublicKey,
			},
		)
		require.ErrorIs(t, err, ErrNoPrivateKey)
		_, err = e.svc.ClientConfig(ctx, p.PublicKey)
		require.ErrorIs(t, err, ErrNoPrivateKey)
	})
}
