package service

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// sixMonths is the paid date of a company created at `now` for 6 months.
var sixMonths = now.AddDate(0, 6, 0)

func TestCreateCompanyMakesTheLeaderAMemberAndDatesTheirKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		ctx := context.Background()
		forever, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)

		c := e.createCompany(t)

		require.Equal(t, "Ромашка", c.Name)
		require.Equal(t, int64(42), c.LeaderID)
		require.Equal(t, sixMonths, c.PaidUntil)
		require.Equal(t, CompanySeats, c.MaxMembers)
		require.Equal(t, CompanyKeysPerMember, c.KeysPerMember)
		require.Len(t, c.ID, inviteCodeLen)
		require.Len(t, c.Invite, inviteCodeLen)
		require.NotEqual(t, c.ID, c.Invite, "the link is not the company's ID")
		leader := e.users().m[42]
		require.Equal(t, RoleMember, leader.Role)
		require.Equal(t, c.ID, leader.CompanyID)
		require.Equal(t, sixMonths, e.peers.m[forever.PublicKey].ExpiresAt, "the vip key now ends with the company")
	})
}

func TestCreateCompanyRefusesAdminsAndMembers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleAdmin)
		ctx := context.Background()
		in := CreateCompanyInput{
			LeaderID: 42,
			Name:     "Ромашка",
			Months:   6,
		}

		_, err := e.svc.CreateCompany(ctx, in)
		require.ErrorIs(t, err, ErrCompanyRole)

		e.setRole(roleInput{ID: 42, Role: RoleUser})
		_, err = e.svc.CreateCompany(ctx, in)
		require.NoError(t, err)
		_, err = e.svc.CreateCompany(ctx, in)
		require.ErrorIs(t, err, ErrInCompany, "a member can't lead a second company")

		in.Name = "  "
		e.addUser(User{ID: 9, Role: RoleUser})
		in.LeaderID = 9
		_, err = e.svc.CreateCompany(ctx, in)
		require.ErrorIs(t, err, ErrBadCompany, "no name")
	})
}

func TestEmployeeJoinsAndGetsTwoKeysUntilTheCompanyDate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		c := e.createCompany(t)
		e.addUser(User{ID: 7, Role: RoleUser})
		ctx := context.Background()

		got, err := e.svc.JoinCompany(
			ctx,
			JoinCompanyInput{
				UserID: 7,
				Invite: c.Invite,
			},
		)
		require.NoError(t, err)
		require.Equal(t, c.ID, got.ID)
		u := e.users().m[7]
		require.Equal(t, RoleMember, u.Role)
		require.Equal(t, c.ID, u.CompanyID)
		require.True(t, u.TrialUsed, "no free trial for company staff")

		require.NoError(t, e.svc.CheckCreateKey(ctx, 7))
		for range CompanyKeysPerMember {
			p, err := e.svc.CreateKey(
				ctx,
				CreateKeyInput{
					UserID: 7,
				},
			)
			require.NoError(t, err)
			require.Equal(t, sixMonths, p.ExpiresAt)
		}
		_, err = e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 7,
			},
		)
		require.ErrorIs(t, err, ErrKeyLimit)

		_, err = e.svc.Invoice(
			ctx,
			PurchaseInput{
				UserID: 7,
				Days:   30,
			},
		)
		require.ErrorIs(t, err, ErrCompanyPaid, "the company pays for its staff")
	})
}

func TestJoinCompanyRefusals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		c := e.createCompany(t)
		ctx := context.Background()
		join := func(in JoinCompanyInput) error {
			_, err := e.svc.JoinCompany(ctx, in)
			return err
		}
		e.addUser(User{ID: 1, Role: RoleAdmin})
		e.addUser(User{ID: 7, Role: RoleUser})
		e.addUser(User{ID: 8, Role: RoleUser})

		require.ErrorIs(t, join(JoinCompanyInput{UserID: 7, Invite: "nope"}), ErrNoCompany)
		require.ErrorIs(t, join(JoinCompanyInput{UserID: 1, Invite: c.Invite}), ErrCompanyRole, "admins stay admins")

		full := e.companies.m[c.ID]
		full.MaxMembers = 1
		e.companies.m[c.ID] = full
		require.NoError(t, join(JoinCompanyInput{UserID: 7, Invite: c.Invite}))
		require.NoError(t, join(JoinCompanyInput{UserID: 7, Invite: c.Invite}), "the same link again is fine")
		require.ErrorIs(t, join(JoinCompanyInput{UserID: 8, Invite: c.Invite}), ErrCompanyFull, "the leader takes no seat, the employee does")

		other := e.createCompanyFor(8)
		require.ErrorIs(t, join(JoinCompanyInput{UserID: 7, Invite: other.Invite}), ErrInCompany)

		time.Sleep(time.Until(sixMonths) + time.Minute)
		e.addUser(User{ID: 9, Role: RoleUser})
		full.MaxMembers = 20
		e.companies.m[c.ID] = full
		require.ErrorIs(t, join(JoinCompanyInput{UserID: 9, Invite: c.Invite}), ErrCompanyEnded)
	})
}

func TestExtendCompanyRedatesEveryKeyAndBringsEndedOnesBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		c := e.createCompany(t)
		staff := e.joinWithKey(t, c.Invite)
		ctx := context.Background()
		blocked := e.joinWithKey(t, c.Invite)
		require.NoError(t, e.svc.Disable(ctx, blocked.PublicKey))

		time.Sleep(time.Until(sixMonths) + time.Minute)
		m, err := e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.False(t, e.peers.m[staff.PublicKey].Enabled, "the company date passed")
		require.Len(t, m.CompanyExpired, 1)
		require.Equal(t, "Ромашка", m.CompanyExpired[0].Company)
		require.Empty(t, m.Expired, "company keys get the company notice, not the usual one")

		got, err := e.svc.ExtendCompany(
			ctx,
			ExtendCompanyInput{
				CompanyID: c.ID,
				Months:    6,
			},
		)
		require.NoError(t, err)
		until := time.Now().AddDate(0, 6, 0)
		require.Equal(t, until, got.PaidUntil, "an ended company counts from now")
		require.True(t, e.peers.m[staff.PublicKey].Enabled)
		require.Equal(t, until, e.peers.m[staff.PublicKey].ExpiresAt)
		require.False(t, e.peers.m[blocked.PublicKey].Enabled, "an admin block stays")
		require.Equal(t, until, e.peers.m[blocked.PublicKey].ExpiresAt)

		got, err = e.svc.ExtendCompany(
			ctx,
			ExtendCompanyInput{
				CompanyID: c.ID,
				Months:    1,
			},
		)
		require.NoError(t, err)
		require.Equal(t, until.AddDate(0, 1, 0), got.PaidUntil, "a running company counts from its date")
	})
}

func TestRemoveMember(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		c := e.createCompany(t)
		k := e.joinWithKey(t, c.Invite)
		ctx := context.Background()
		e.addUser(User{ID: 1, Role: RoleAdmin})
		remove := func(in RemoveMemberInput) error {
			in.CompanyID = c.ID
			return e.svc.RemoveMember(ctx, in)
		}

		require.ErrorIs(t, remove(RemoveMemberInput{ActorID: k.UserID, UserID: 42}), ErrNotLeader)
		require.ErrorIs(t, remove(RemoveMemberInput{ActorID: 42, UserID: 42}), ErrIsLeader)
		require.NoError(t, remove(RemoveMemberInput{ActorID: 42, UserID: k.UserID}))

		_, onServer := e.vpn.peers[k.PublicKey]
		require.False(t, onServer)
		require.NotContains(t, e.peers.m, k.PublicKey, "company keys go with the employee")
		u := e.users().m[k.UserID]
		require.Equal(t, RoleUser, u.Role)
		require.Empty(t, u.CompanyID)
		require.True(t, u.TrialUsed)

		again := e.joinWithKey(t, c.Invite)
		require.NoError(t, remove(RemoveMemberInput{ActorID: 1, UserID: again.UserID}), "an admin may remove too")
	})
}

func TestCompanyRemindersGoOncePerCompanyNotPerKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		c := e.createCompany(t)
		e.joinWithKey(t, c.Invite)
		ctx := context.Background()

		time.Sleep(time.Until(sixMonths) - 70*time.Hour)
		m, err := e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Empty(t, m.Remind3d, "employees get no reminders")
		require.Len(t, m.CompanyRemind, 1)
		require.Equal(t, 3, m.CompanyRemind[0].Days)
		require.Equal(t, int64(42), m.CompanyRemind[0].Company.LeaderID)

		m, err = e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Empty(t, m.CompanyRemind, "once")

		time.Sleep(48 * time.Hour)
		m, err = e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Len(t, m.CompanyRemind, 1)
		require.Equal(t, 1, m.CompanyRemind[0].Days)
	})
}

func TestMyCompanyIsForTheLeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleVIP)
		c := e.createCompany(t)
		k := e.joinWithKey(t, c.Invite)
		ctx := context.Background()

		info, err := e.svc.MyCompany(ctx, 42)
		require.NoError(t, err)
		require.Equal(t, c.ID, info.Company.ID)
		require.Len(t, info.Members, 2, "the leader and the employee")
		require.Equal(t, 1, info.Employees())

		_, err = e.svc.MyCompany(ctx, k.UserID)
		require.ErrorIs(t, err, ErrNotLeader)

		all, err := e.svc.Companies(ctx)
		require.NoError(t, err)
		require.Len(t, all, 1)
	})
}

// --- helpers ---

func (s *env) addUser(u User) {
	s.users().m[u.ID] = u
}

// createCompany makes user 42 the leader of "Ромашка", paid for 6 months.
func (s *env) createCompany(t *testing.T) *Company {
	t.Helper()
	return s.createCompanyFor(42)
}

func (s *env) createCompanyFor(leaderID int64) *Company {
	c, err := s.svc.CreateCompany(
		context.Background(),
		CreateCompanyInput{
			LeaderID: leaderID,
			Name:     "Ромашка",
			Months:   6,
		},
	)
	if err != nil {
		panic(err)
	}
	return c
}

// joinWithKey adds a new employee by the invite and gives them one key.
func (s *env) joinWithKey(t *testing.T, invite string) *Peer {
	t.Helper()
	id := int64(100 + len(s.users().m))
	s.addUser(User{ID: id, Role: RoleUser})
	ctx := context.Background()
	_, err := s.svc.JoinCompany(
		ctx,
		JoinCompanyInput{
			UserID: id,
			Invite: invite,
		},
	)
	require.NoError(t, err)
	p, err := s.svc.CreateKey(
		ctx,
		CreateKeyInput{
			UserID: id,
		},
	)
	require.NoError(t, err)
	return p
}
