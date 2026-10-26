package service

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVIPCreatesForeverKeysUpToTheLimitSetInTheDB(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		e.setMaxKeys(5)
		ctx := context.Background()

		for range 5 {
			p, err := e.svc.CreateKey(
				ctx,
				CreateKeyInput{
					UserID: 42,
				},
			)
			require.NoError(t, err)
			require.True(t, p.ExpiresAt.IsZero(), "vip keys never expire")
		}
		_, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.ErrorIs(t, err, ErrKeyLimit)
		require.ErrorIs(t, e.svc.CheckCreateKey(ctx, 42), ErrKeyLimit)
	})
}

func TestVIPWithoutALimitGetsTheUnlimitedOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		ctx := context.Background()

		for range MaxUnlimitedKeys {
			_, err := e.svc.CreateKey(
				ctx,
				CreateKeyInput{
					UserID: 42,
				},
			)
			require.NoError(t, err)
		}
		_, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.ErrorIs(t, err, ErrKeyLimit)
	})
}

func TestUserKeyLimit(t *testing.T) {
	cases := map[string]struct {
		user User
		want int
	}{
		"vip with a limit": {
			user: User{Role: RoleVIP, MaxKeys: 10},
			want: 10,
		},
		"vip without one": {
			user: User{Role: RoleVIP},
			want: MaxUnlimitedKeys,
		},
		"unlimited ignores max_keys": {
			user: User{Role: RoleUnlimited, MaxKeys: 10},
			want: MaxUnlimitedKeys,
		},
		"plain user": {
			user: User{Role: RoleUser, MaxKeys: 10},
			want: MaxUserKeys,
		},
	}
	for name, c := range cases {
		require.Equal(t, c.want, c.user.KeyLimit(), name)
	}
}

func TestVIPDoesNotBuy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)

		_, err := e.svc.Invoice(
			context.Background(),
			PurchaseInput{
				UserID: 42,
				Days:   30,
			},
		)
		require.ErrorIs(t, err, ErrNotForSale)
	})
}

func TestBecomingVIPMakesTheTrialKeyForever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		trial, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)

		e.setRole(roleInput{ID: 42, Role: RoleVIP})
		time.Sleep(time.Minute)
		_, err = e.svc.Maintain(ctx)
		require.NoError(t, err)

		require.True(t, e.peers.m[trial.PublicKey].ExpiresAt.IsZero())
	})
}

// setMaxKeys sets user 42's max_keys, as an admin does by hand in the DB.
func (s *env) setMaxKeys(n int) {
	u := s.users().m[42]
	u.MaxKeys = n
	s.users().m[42] = u
}
