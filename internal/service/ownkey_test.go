package service

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

func TestReissueKeyGivesNewKeysSameEverythingElse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		old := ownKey(t, e)

		p, err := e.svc.ReissueKey(
			context.Background(),
			UserKey{
				UserID:    42,
				PublicKey: old.PublicKey,
			},
		)
		require.NoError(t, err)
		require.NotEqual(t, old.PublicKey, p.PublicKey)
		require.NotEmpty(t, e.peers.m[p.PublicKey].PrivateKey, "the new config can be rendered")
		require.Equal(t, old.IP, p.IP)
		require.Equal(t, old.Name, p.Name)
		require.True(t, old.ExpiresAt.Equal(p.ExpiresAt))
		require.True(t, p.Enabled)

		require.False(t, e.vpn.hasPeer(old.PublicKey), "the old key stops working at once")
		require.True(t, e.vpn.hasPeer(p.PublicKey))
		require.NotContains(t, e.peers.m, old.PublicKey)
		require.Contains(t, e.peers.m, p.PublicKey)
		require.Equal(t, old.Name, e.vpn.peers[p.PublicKey].Name)
	})
}

func TestReissueDisabledKeyStaysDisabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		old := ownKey(t, e)
		require.NoError(t, e.svc.Disable(context.Background(), old.PublicKey))

		p, err := e.svc.ReissueKey(
			context.Background(),
			UserKey{
				UserID:    42,
				PublicKey: old.PublicKey,
			},
		)
		require.NoError(t, err)
		require.False(t, p.Enabled)
		require.True(t, p.Blocked, "an admin block survives a reissue")
		require.False(t, e.vpn.hasPeer(p.PublicKey))
	})
}

func TestReissueFailureKeepsTheOldKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		old := ownKey(t, e)
		e.vpn.err = errBoom

		_, err := e.svc.ReissueKey(
			context.Background(),
			UserKey{
				UserID:    42,
				PublicKey: old.PublicKey,
			},
		)
		require.Error(t, err)
		e.vpn.err = nil
		require.Len(t, e.peers.m, 1)
		require.Contains(t, e.peers.m, old.PublicKey, "the old record is back")
		require.True(t, e.vpn.hasPeer(old.PublicKey), "and the old key still works")
	})
}

func TestReissueUnreadableKeyRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		old := ownKey(t, e)
		row := e.peers.m[old.PublicKey]
		row.PSK = ""
		e.peers.m[old.PublicKey] = row
		before := e.vpn.hasPeer(old.PublicKey)

		_, err := e.svc.ReissueKey(
			context.Background(),
			UserKey{
				UserID:    42,
				PublicKey: old.PublicKey,
			},
		)
		require.ErrorIs(t, err, ErrUnreadable)
		require.Len(t, e.peers.m, 1)
		require.Equal(t, row, e.peers.m[old.PublicKey], "the row is unchanged")
		require.Equal(t, before, e.vpn.hasPeer(old.PublicKey))
		require.Equal(t, old.IP, e.peers.m[old.PublicKey].IP)
	})
}

func TestOwnKeyActionsNeedTheOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		old := ownKey(t, e)
		other := UserKey{
			UserID:    7,
			PublicKey: old.PublicKey,
		}

		_, err := e.svc.ReissueKey(context.Background(), other)
		require.ErrorIs(t, err, ErrNotFound)
		require.ErrorIs(t, e.svc.DeleteOwnKey(context.Background(), other), ErrNotFound)
		require.Contains(t, e.peers.m, old.PublicKey)
		require.True(t, e.vpn.hasPeer(old.PublicKey))
	})
}

func TestDeleteOwnKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		old := ownKey(t, e)

		require.NoError(
			t,
			e.svc.DeleteOwnKey(
				context.Background(),
				UserKey{
					UserID:    42,
					PublicKey: old.PublicKey,
				},
			),
		)
		require.NotContains(t, e.peers.m, old.PublicKey)
		require.False(t, e.vpn.hasPeer(old.PublicKey))
	})
}

func TestDeleteOwnKeyKeepsTheKeyWhenTheServerFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		old := ownKey(t, e)
		e.vpn.err = errBoom

		err := e.svc.DeleteOwnKey(
			context.Background(),
			UserKey{
				UserID:    42,
				PublicKey: old.PublicKey,
			},
		)
		require.ErrorIs(t, err, errBoom)
		require.Contains(t, e.peers.m, old.PublicKey)
	})
}

func TestDeleteOwnKeyRefusesABlockedKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		old := ownKey(t, e)
		ctx := context.Background()
		require.NoError(t, e.svc.Disable(ctx, old.PublicKey))

		err := e.svc.DeleteOwnKey(
			ctx,
			UserKey{
				UserID:    42,
				PublicKey: old.PublicKey,
			},
		)
		require.ErrorIs(t, err, ErrBlocked)
		require.True(t, e.peers.m[old.PublicKey].Blocked, "the block can't be deleted away")
		require.ErrorIs(t, e.svc.CheckCreateKey(ctx, 42), ErrHasKey)

		require.NoError(t, e.svc.Delete(ctx, old.PublicKey), "an admin still deletes it")
	})
}

// ownKey registers user 42 (plain) with a paid-looking key that ends in 10 days.
func ownKey(t *testing.T, e *env) *Peer {
	t.Helper()
	e.register(t, RoleUser)
	return e.seed(t, now.AddDate(0, 0, 10))
}
