package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/vpn/amnezia"
)

func TestStats(t *testing.T) {
	e := newEnv()
	register(t, e, RoleUser)
	ctx := context.Background()
	online := seed(t, e, now.AddDate(0, 0, 3)) // expires within 7 days
	idle := seed(t, e, now.AddDate(0, 0, 30))  // active
	off := seed(t, e, now.AddDate(0, 0, 30))   // disabled below
	forever := seed(t, e, time.Time{})         // never expires
	require.NoError(t, e.svc.Disable(ctx, off.PublicKey))
	e.vpn.stats = []amnezia.PeerStat{
		{
			PublicKey:       online.PublicKey,
			LatestHandshake: now.Add(-time.Minute),
			RX:              10,
			TX:              1000,
		},
		{
			PublicKey:       idle.PublicKey,
			LatestHandshake: now.Add(-time.Hour),
			RX:              5,
			TX:              50,
		},
	}
	e.payments.m = map[string]Payment{
		"new": {
			ChargeID:  "new",
			Stars:     150,
			Applied:   true,
			CreatedAt: now.AddDate(0, 0, -3),
		},
		"refunded": {
			ChargeID:   "refunded",
			Stars:      400,
			Applied:    true,
			CreatedAt:  now.AddDate(0, 0, -3),
			RefundedAt: now,
		},
		"old": {
			ChargeID:  "old",
			Stars:     1500,
			Applied:   true,
			CreatedAt: now.AddDate(0, 0, -40),
		},
	}

	st, err := e.svc.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), st.Users)
	require.Equal(t, 3, st.Active)
	require.Equal(t, 1, st.Disabled)
	require.Equal(t, 1, st.Expiring7d)
	require.Equal(t, 1, st.Online)
	require.Equal(t, 150, st.Revenue30d, "refunded and old payments don't count")
	require.Equal(t, 1, st.Payments30d)
	require.Equal(t, online.PublicKey, st.TopTraffic[0].Peer.PublicKey, "sorted by traffic")
	require.Len(t, st.TopTraffic, 4)
	_ = forever
}

func TestBroadcastRecipients(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	a := seed(t, e, now.AddDate(0, 0, 30)) // user 42
	seed(t, e, now.AddDate(0, 0, 30))      // user 42 again
	off := seed(t, e, now.AddDate(0, 0, 30))
	stored := e.peers.m[off.PublicKey]
	stored.UserID = 7
	e.peers.m[off.PublicKey] = stored
	require.NoError(t, e.svc.Disable(ctx, off.PublicKey))
	_, err := e.svc.Issue(
		ctx,
		IssueInput{
			Name: "Мама iPhone",
			Days: 30,
		},
	)
	require.NoError(t, err)

	ids, err := e.svc.BroadcastRecipients(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{a.UserID}, ids, "one per user, enabled keys only, no keys without Telegram")
}
