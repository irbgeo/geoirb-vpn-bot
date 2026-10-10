package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

func TestRegisterNewUserIsPlainUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()

		u, err := e.svc.Register(
			context.Background(),
			RegisterInput{
				ID:       42,
				Username: "alice",
			},
		)
		require.NoError(t, err)
		require.Equal(
			t,
			User{
				ID:        42,
				Username:  "alice",
				Role:      RoleUser,
				CreatedAt: now,
			},
			*u,
		)
		require.Equal(t, *u, e.users().m[42])
	})
}

func TestRegisterKeepsRoleAndUpdatesUsername(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		_, err := e.svc.Register(
			ctx,
			RegisterInput{
				ID:       42,
				Username: "alice",
			},
		)
		require.NoError(t, err)
		e.setRole(roleInput{ID: 42, Role: RoleAdmin})

		u, err := e.svc.Register(
			ctx,
			RegisterInput{
				ID:       42,
				Username: "alice_new",
			},
		)
		require.NoError(t, err)
		require.Equal(t, RoleAdmin, u.Role, "role is set by hand in the DB, never overwritten")
		require.Equal(t, "alice_new", u.Username)
		require.Equal(t, now, u.CreatedAt)
	})
}

func TestGetUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()

		_, err := e.svc.User(ctx, 42)
		require.ErrorIs(t, err, ErrNotFound)

		_, err = e.svc.Register(
			ctx,
			RegisterInput{
				ID: 42,
			},
		)
		require.NoError(t, err)
		u, err := e.svc.User(ctx, 42)
		require.NoError(t, err)
		require.Equal(t, RoleUser, u.Role)
	})
}

func TestAdmins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		for _, id := range []int64{
			1,
			2,
		} {
			_, err := e.svc.Register(
				ctx,
				RegisterInput{
					ID: id,
				},
			)
			require.NoError(t, err)
		}
		e.setRole(roleInput{ID: 2, Role: RoleAdmin})

		admins, err := e.svc.Admins(ctx)
		require.NoError(t, err)
		require.Len(t, admins, 1)
		require.Equal(t, int64(2), admins[0].ID)
	})
}

func TestUsersPage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		for id := int64(1); id <= 3; id++ {
			_, err := e.svc.Register(
				ctx,
				RegisterInput{
					ID: id,
				},
			)
			require.NoError(t, err)
		}

		page, total, err := e.svc.Users(
			ctx,
			Page{
				Skip:  2,
				Limit: 10,
			},
		)
		require.NoError(t, err)
		require.Equal(t, int64(3), total)
		require.Len(t, page, 1)
	})
}

func TestIssueNamesKeyAfterTelegramUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		for id, name := range map[int64]string{
			42: "bob",
			7:  "",
		} {
			_, err := e.svc.Register(
				ctx,
				RegisterInput{
					ID:       id,
					Username: name,
				},
			)
			require.NoError(t, err)
		}

		bob, err := e.svc.Issue(
			ctx,
			IssueInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		require.Equal(t, "tg:bob", bob.Name)

		noUsername, err := e.svc.Issue(
			ctx,
			IssueInput{
				UserID: 7,
			},
		)
		require.NoError(t, err)
		require.Equal(t, "tg:7", noUsername.Name, "same format as self-service keys")
	})
}

func TestKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		p := e.issue(t, 30)

		got, err := e.svc.Key(ctx, p.PublicKey)
		require.NoError(t, err)
		require.Equal(t, p.IP, got.IP)

		_, err = e.svc.Key(ctx, "MANUAL1=")
		require.ErrorIs(t, err, ErrNotFound)
	})
}

func TestCreateKeyUserStartsTrial(t *testing.T) {
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
		require.Equal(t, now.AddDate(0, 0, 7), p.ExpiresAt, "7-day trial")
		require.Equal(t, "tg:bob", p.Name)
		require.True(t, e.users().m[42].TrialUsed)

		_, err = e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.ErrorIs(t, err, ErrHasKey, "one key per user")
	})
}

func TestCreateKeyUserTrialOnlyOnce(t *testing.T) {
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
		require.NoError(t, e.svc.Delete(ctx, p.PublicKey))

		_, err = e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.ErrorIs(t, err, ErrTrialUsed, "no second trial after the key is gone")
	})
}

func TestCreateKeyUnlimitedUpToThreeForeverKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUnlimited)
		ctx := context.Background()

		for i := 1; i <= MaxUnlimitedKeys; i++ {
			p, err := e.svc.CreateKey(
				ctx,
				CreateKeyInput{
					UserID: 42,
				},
			)
			require.NoError(t, err)
			require.True(t, p.ExpiresAt.IsZero(), "never expires")
			require.Equal(t, fmt.Sprintf("tg:bob #%d", i), p.Name)
		}
		_, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.ErrorIs(t, err, ErrKeyLimit)
		require.False(t, e.users().m[42].TrialUsed, "no trial involved")
	})
}

func TestCreateKeyAdminHasNoLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleAdmin)
		ctx := context.Background()

		for i := 1; i <= MaxUnlimitedKeys+2; i++ {
			p, err := e.svc.CreateKey(
				ctx,
				CreateKeyInput{
					UserID: 42,
				},
			)
			require.NoError(t, err)
			require.True(t, p.ExpiresAt.IsZero(), "never expires")
			require.Equal(t, fmt.Sprintf("tg:bob #%d", i), p.Name)
		}
		require.NoError(t, e.svc.CheckCreateKey(ctx, 42))
	})
}

func TestCreateKeyLimitCountsDisabledKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUnlimited)
		ctx := context.Background()
		for i := 0; i < MaxUnlimitedKeys; i++ {
			p, err := e.svc.CreateKey(
				ctx,
				CreateKeyInput{
					UserID: 42,
				},
			)
			require.NoError(t, err)
			require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
		}

		_, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.ErrorIs(t, err, ErrKeyLimit, "a disabled key still holds its slot and IP")
	})
}

func TestCreateKeyUnknownUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		_, err := e.svc.CreateKey(
			context.Background(),
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.ErrorIs(t, err, ErrNotFound)
	})
}

func TestCheckCreateKeyGivesCreateKeysVerdictWithoutIssuing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()

		require.NoError(t, e.svc.CheckCreateKey(ctx, 42), "a new user may get the trial key")
		require.Empty(t, e.peers.m, "nothing issued by a check")

		_, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		require.ErrorIs(t, e.svc.CheckCreateKey(ctx, 42), ErrHasKey)
		require.ErrorIs(t, e.svc.CheckCreateKey(ctx, 7), ErrNotFound)
	})
}

func TestKeysCountFollowsIssueAndDelete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUnlimited)
		ctx := context.Background()

		a, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		_, err = e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		require.Equal(t, 2, e.users().m[42].KeysCount)

		require.NoError(t, e.svc.Delete(ctx, a.PublicKey))
		require.Equal(t, 1, e.users().m[42].KeysCount)
	})
}

func TestReconcileFixesKeysCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUnlimited)
		ctx := context.Background()
		_, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		u := e.users().m[42]
		u.KeysCount = 7 // drifted (e.g. a failed update)
		e.users().m[42] = u

		_, err = e.svc.Reconcile(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, e.users().m[42].KeysCount)
	})
}

func TestCreateKeyUsesTheGivenName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, r := range []Role{
			RoleUser,
			RoleUnlimited,
		} {
			e := newEnv()
			e.register(t, r)

			p, err := e.svc.CreateKey(
				context.Background(),
				CreateKeyInput{
					UserID: 42,
					Name:   "  iPhone   Маши  ",
				},
			)
			require.NoError(t, err, string(r))
			require.Equal(t, "iPhone Маши", p.Name, "trimmed, inner spaces squeezed")
			require.Equal(t, "iPhone Маши", e.vpn.peers[p.PublicKey].Name, "the same name on the server")
		}
	})
}

func TestCreateKeyWithoutNameKeepsTheOldScheme(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUnlimited)

		p, err := e.svc.CreateKey(
			context.Background(),
			CreateKeyInput{
				UserID: 42,
				Name:   "   ",
			},
		)
		require.NoError(t, err)
		require.Equal(t, "tg:bob #1", p.Name)
	})
}

func TestCreateKeyRejectsBadNames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, name := range []string{
			strings.Repeat("я", MaxKeyNameLen+1),
			"two\nlines",
			"tab\tinside",
		} {
			e := newEnv()
			e.register(t, RoleUnlimited)

			_, err := e.svc.CreateKey(
				context.Background(),
				CreateKeyInput{
					UserID: 42,
					Name:   name,
				},
			)
			require.ErrorIs(t, err, ErrBadKeyName, name)
			require.Empty(t, e.peers.m, "nothing issued")
		}
	})
}

func TestCreateKeyNameAtTheLimitIsFine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUnlimited)

		_, err := e.svc.CreateKey(
			context.Background(),
			CreateKeyInput{
				UserID: 42,
				Name:   strings.Repeat("я", MaxKeyNameLen),
			},
		)
		require.NoError(t, err)
	})
}

func (s *env) users() *fakeUsers {
	return s.svc.users.(*fakeUsers)
}

func (s *env) setRole(in roleInput) {
	u := s.users().m[in.ID]
	u.Role = in.Role
	s.users().m[in.ID] = u
}

func (s *env) register(t *testing.T, r Role) {
	t.Helper()
	_, err := s.svc.Register(
		context.Background(),
		RegisterInput{
			ID:       42,
			Username: "bob",
		},
	)
	require.NoError(t, err)
	s.setRole(roleInput{ID: 42, Role: r})
}

func TestNextKeyNumberTakesSmallestFree(t *testing.T) {
	require.Equal(t, 2, nextKeyNumber(keyNumberInput{Names: []string{"tg:u #1", "tg:u #3"}, Base: "tg:u"}))
	require.Equal(t, 3, nextKeyNumber(keyNumberInput{Names: []string{"tg:u #1", "tg:u #2"}, Base: "tg:u"}))
	require.Equal(t, 1, nextKeyNumber(keyNumberInput{Names: nil, Base: "tg:u"}))
	require.Equal(t, 1, nextKeyNumber(keyNumberInput{Names: []string{"tg:u #2", "other #1", "tg:u"}, Base: "tg:u"}))
}

func TestRegisterKeepsARoleSetByHandInBetween(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		e.register(t, RoleUnlimited)

		u, err := e.svc.Register(
			ctx,
			RegisterInput{
				ID:       42,
				Username: "bob2",
			},
		)
		require.NoError(t, err)
		require.Equal(t, RoleUnlimited, u.Role)
		require.Equal(t, "bob2", u.Username)
	})
}

// CreateKey must read the user under s.mu. Read before it, the user can be
// stale by the time the lock is taken: another CreateKey used the trial and
// the key was deleted meanwhile, and this call would start a second trial.
func TestCreateKeyReadsTheUserUnderTheLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		unlocked := 0
		e.users().onGet = func() {
			if e.svc.mu.TryLock() {
				e.svc.mu.Unlock()
				unlocked++
			}
		}

		_, err := e.svc.CreateKey(
			context.Background(),
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		require.Zero(t, unlocked, "the user was read while the lock was free")
	})
}
