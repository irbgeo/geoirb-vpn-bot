package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

func TestCompanyRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	c := &service.Company{
		ID:            "abcd2345",
		Name:          "Ромашка",
		LeaderID:      42,
		PaidUntil:     ts("2027-04-26T10:00:00Z"),
		MaxMembers:    20,
		KeysPerMember: 2,
		CreatedAt:     ts("2026-10-26T10:00:00Z"),
	}

	missing, err := s.Companies.Get(ctx, c.ID)
	require.NoError(t, err)
	require.Nil(t, missing)

	require.NoError(t, s.Companies.Add(ctx, c))
	got, err := s.Companies.Get(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, c, got)

	c.PaidUntil = ts("2027-10-26T10:00:00Z")
	c.Reminded3d = true
	require.NoError(t, s.Companies.Save(ctx, c))
	all, err := s.Companies.List(ctx)
	require.NoError(t, err)
	require.Equal(t, []*service.Company{c}, all)
}

func TestSetMembershipAndByCompany(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, id := range []int64{7, 8} {
		_, err := s.Users.Register(
			ctx,
			&service.User{
				ID:   id,
				Role: service.RoleUser,
			},
		)
		require.NoError(t, err)
	}

	require.NoError(t, s.Users.SetMembership(
		ctx,
		service.Membership{
			UserID:    7,
			Role:      service.RoleMember,
			CompanyID: "abcd2345",
		},
	))
	members, err := s.Users.ByCompany(ctx, "abcd2345")
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Equal(t, int64(7), members[0].ID)
	require.Equal(t, service.RoleMember, members[0].Role)
	require.Equal(t, "abcd2345", members[0].CompanyID)
	require.True(t, members[0].TrialUsed)

	require.NoError(t, s.Users.SetMembership(
		ctx,
		service.Membership{
			UserID: 7,
			Role:   service.RoleUser,
		},
	))
	members, err = s.Users.ByCompany(ctx, "abcd2345")
	require.NoError(t, err)
	require.Empty(t, members)
	var raw bson.M
	require.NoError(t, s.Users.coll.FindOne(ctx, byID(int64(7))).Decode(&raw))
	require.NotContains(t, raw, "company_id", "leaving unsets the field")
	require.Equal(t, "user", raw["role"])
}
