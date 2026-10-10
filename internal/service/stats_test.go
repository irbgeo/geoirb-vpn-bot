package service

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		online := e.seed(t, now.AddDate(0, 0, 3)) // expires within 7 days
		idle := e.seed(t, now.AddDate(0, 0, 30))  // active
		off := e.seed(t, now.AddDate(0, 0, 30))   // disabled below
		forever := e.seed(t, time.Time{})         // never expires
		require.NoError(t, e.svc.Disable(ctx, off.PublicKey))
		e.vpn.stats = []PeerStat{
			{
				PublicKey:     online.PublicKey,
				LastHandshake: now.Add(-time.Minute),
				Sent:          10,
				Received:      1000,
			},
			{
				PublicKey:     idle.PublicKey,
				LastHandshake: now.Add(-time.Hour),
				Sent:          5000,
				Received:      50000, // the most traffic, on the second IP
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
		require.Len(t, st.TopTraffic, 4)
		require.Equal(
			t,
			[]string{
				idle.PublicKey,
				online.PublicKey,
			},
			[]string{
				st.TopTraffic[0].Peer.PublicKey,
				st.TopTraffic[1].Peer.PublicKey,
			},
			"sorted by traffic, not by IP",
		)
		_ = forever
	})
}

func TestBroadcastRecipients(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		ctx := context.Background()
		a := e.seed(t, now.AddDate(0, 0, 30)) // user 42
		e.seed(t, now.AddDate(0, 0, 30))      // user 42 again
		off := e.seed(t, now.AddDate(0, 0, 30))
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
	})
}
