package service

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// Audit 2026-10-26, finding 2: a company must not get round an admin block.
func TestBlockedUserCannotJoinACompany(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		c := e.createCompany(t)
		ctx := context.Background()
		e.addUser(User{ID: 7, Role: RoleUser})
		k, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 7,
			},
		)
		require.NoError(t, err)
		require.NoError(t, e.svc.Disable(ctx, k.PublicKey))

		_, err = e.svc.JoinCompany(
			ctx,
			JoinCompanyInput{
				UserID: 7,
				Invite: c.Invite,
			},
		)
		require.ErrorIs(t, err, ErrBlocked)
		require.Equal(t, RoleUser, e.users().m[7].Role)
	})
}

// Finding 2, the other way in: a member whose key got blocked gets no new one.
func TestBlockedMemberGetsNoNewKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		c := e.createCompany(t)
		k := e.joinWithKey(t, c.Invite)
		ctx := context.Background()
		require.NoError(t, e.svc.Disable(ctx, k.PublicKey))

		_, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: k.UserID,
			},
		)
		require.ErrorIs(t, err, ErrBlocked)
		require.ErrorIs(t, e.svc.CheckCreateKey(ctx, k.UserID), ErrBlocked)
	})
}

// Finding 4: removing a member keeps their blocked keys, so the block stays.
func TestRemoveMemberKeepsBlockedKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		c := e.createCompany(t)
		blocked := e.joinWithKey(t, c.Invite)
		ctx := context.Background()
		free, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: blocked.UserID,
			},
		)
		require.NoError(t, err)
		require.NoError(t, e.svc.Disable(ctx, blocked.PublicKey))

		removeMemberInput := RemoveMemberInput{
			ActorID:   42,
			CompanyID: c.ID,
			UserID:    blocked.UserID,
		}
		require.NoError(t, e.svc.RemoveMember(ctx, removeMemberInput))

		require.Contains(t, e.peers.m, blocked.PublicKey, "the admin block stays")
		require.NotContains(t, e.peers.m, free.PublicKey, "the working company key goes")
		_, err = e.svc.Invoice(
			ctx,
			PurchaseInput{
				UserID:    blocked.UserID,
				Days:      30,
				PublicKey: BuyNewKey,
			},
		)
		require.ErrorIs(t, err, ErrBlocked, "no new paid key either")
	})
}

// Finding 5: a seat holds at most KeysPerMember keys.
func TestJoinRefusedWithMoreKeysThanASeatHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		c := e.createCompany(t)
		e.addUser(User{ID: 7, Role: RoleUnlimited})
		ctx := context.Background()
		for range CompanyKeysPerMember + 1 {
			_, err := e.svc.CreateKey(
				ctx,
				CreateKeyInput{
					UserID: 7,
				},
			)
			require.NoError(t, err)
		}

		_, err := e.svc.JoinCompany(
			ctx,
			JoinCompanyInput{
				UserID: 7,
				Invite: c.Invite,
			},
		)
		require.ErrorIs(t, err, ErrCompanyKeys)
		require.Equal(t, RoleUnlimited, e.users().m[7].Role)
	})
}

// Finding 6: a leaked link can be replaced; the old one stops working.
func TestRotateInvite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		c := e.createCompany(t)
		k := e.joinWithKey(t, c.Invite)
		e.addUser(User{ID: 1, Role: RoleAdmin})
		e.addUser(User{ID: 7, Role: RoleUser})
		e.addUser(User{ID: 8, Role: RoleUser})
		ctx := context.Background()
		rotate := func(actor int64) (*Company, error) {
			rotateInviteInput := RotateInviteInput{
				ActorID:   actor,
				CompanyID: c.ID,
			}
			return e.svc.RotateInvite(ctx, rotateInviteInput)
		}

		_, err := rotate(k.UserID)
		require.ErrorIs(t, err, ErrNotLeader)

		got, err := rotate(42)
		require.NoError(t, err)
		require.NotEqual(t, c.Invite, got.Invite)
		require.Len(t, got.Invite, inviteCodeLen)

		_, err = e.svc.JoinCompany(
			ctx,
			JoinCompanyInput{
				UserID: 7,
				Invite: c.Invite,
			},
		)
		require.ErrorIs(t, err, ErrNoCompany, "the old link is dead")
		_, err = e.svc.JoinCompany(
			ctx,
			JoinCompanyInput{
				UserID: 7,
				Invite: got.Invite,
			},
		)
		require.NoError(t, err)

		_, err = rotate(1)
		require.NoError(t, err, "an admin may too")
	})
}

// Finding 3: keys that ended long ago give their IP back.
func TestLongEndedKeysAreDeleted(t *testing.T) {
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
		e.addUser(User{ID: 7, Role: RoleUser})
		blocked, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 7,
			},
		)
		require.NoError(t, err)
		require.NoError(t, e.svc.Disable(ctx, blocked.PublicKey))

		time.Sleep(8 * 24 * time.Hour) // the trial ended a day ago
		m, err := e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Len(t, m.Expired, 1)
		require.Contains(t, e.peers.m, trial.PublicKey, "only disabled at first")

		time.Sleep(staleKeyAge)
		_, err = e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.NotContains(t, e.peers.m, trial.PublicKey, "its IP is free again")
		require.Contains(t, e.peers.m, blocked.PublicKey, "an admin block is kept")
		require.Equal(t, 0, e.users().m[42].KeysCount)
	})
}

// Finding 3, the other half: new trials are limited per hour for all users.
func TestTrialsPerHourLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		create := func(id int64) error {
			e.addUser(User{ID: id, Role: RoleUser})
			_, err := e.svc.CreateKey(
				ctx,
				CreateKeyInput{
					UserID: id,
				},
			)
			return err
		}
		for i := range MaxTrialsPerHour {
			require.NoError(t, create(int64(1000+i)))
		}

		require.ErrorIs(t, create(5000), ErrTrialBusy)
		require.False(t, e.users().m[5000].TrialUsed, "a refused trial stays available")

		time.Sleep(time.Hour)
		require.NoError(t, create(5000))
	})
}
