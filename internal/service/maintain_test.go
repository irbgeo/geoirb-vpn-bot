package service

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMaintainExpiresAndDisables(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		expired := e.seed(t, now.Add(-time.Minute))
		active := e.seed(t, now.AddDate(0, 0, 10))
		forever := e.seed(t, time.Time{})

		m, err := e.svc.Maintain(context.Background())
		require.NoError(t, err)

		require.Equal(t, []string{expired.PublicKey}, keysOf(m.Expired))
		require.False(t, e.peers.m[expired.PublicKey].Enabled)
		require.False(t, e.vpn.hasPeer(expired.PublicKey), "removed from the server")
		require.Equal(t, "10.8.1.2", e.peers.m[expired.PublicKey].IP, "IP kept for a later extension")
		require.True(t, e.vpn.hasPeer(active.PublicKey))
		require.True(t, e.vpn.hasPeer(forever.PublicKey))

		m, err = e.svc.Maintain(context.Background())
		require.NoError(t, err)
		require.Empty(t, m.Expired, "a disabled key is not expired again")
	})
}

func TestMaintainRemindersOncePerTerm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		in2days := e.seed(t, now.Add(48*time.Hour))
		in12h := e.seed(t, now.Add(12*time.Hour))
		e.seed(t, now.AddDate(0, 0, 5))
		ctx := context.Background()

		m, err := e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{in2days.PublicKey}, keysOf(m.Remind3d))
		require.Equal(t, []string{in12h.PublicKey}, keysOf(m.Remind1d), "under a day: only the 1-day reminder")
		require.True(t, e.peers.m[in12h.PublicKey].Reminded3d, "the 3-day one is skipped for good")

		m, err = e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Empty(t, m.Remind3d)
		require.Empty(t, m.Remind1d)

		_, err = e.svc.Extend(
			ctx,
			ExtendInput{
				PublicKey: in12h.PublicKey,
				Days:      4,
			},
		)
		require.NoError(t, err)
		time.Sleep(36 * time.Hour) // 3 days left on it now
		m, err = e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Contains(t, keysOf(m.Remind3d), in12h.PublicKey, "extension resets reminders")
	})
}

// A term that starts inside a reminder window must not get that reminder
// the minute the key is issued or extended.
func TestShortTermGetsNoReminderAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		e.svc.cfg.TrialDays = 3
		ctx := context.Background()
		p, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)

		m, err := e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Empty(t, m.Remind3d, "a 3-day trial just started")
		require.Empty(t, m.Remind1d)

		time.Sleep(49 * time.Hour)
		m, err = e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{p.PublicKey}, keysOf(m.Remind1d), "the 1-day reminder still comes")

		_, err = e.svc.Extend(
			ctx,
			ExtendInput{
				PublicKey: p.PublicKey,
				Days:      1,
			},
		)
		require.NoError(t, err)
		m, err = e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Empty(t, m.Remind3d, "a day bought: under 2 days left is no news")
		require.Empty(t, m.Remind1d)
	})
}

func TestMaintainSubnetUsage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		p := e.seed(t, now.AddDate(0, 0, 10))
		require.NoError(t, e.svc.Disable(context.Background(), p.PublicKey))

		m, err := e.svc.Maintain(context.Background())
		require.NoError(t, err)
		require.Equal(t, 2, m.SubnetUsed, "manual peer .1 + disabled key's reserved .2")
		require.Equal(t, 254, m.SubnetTotal)
	})
}

func TestMaintainSubnetFailureStillReportsKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		expired := e.seed(t, now.Add(-time.Minute))
		soon := e.seed(t, now.Add(12*time.Hour))
		e.vpn.readErr = errBoom

		m, err := e.svc.Maintain(context.Background())
		require.NoError(t, err, "the notices must still go out")
		require.Equal(t, []string{expired.PublicKey}, keysOf(m.Expired))
		require.Equal(t, []string{soon.PublicKey}, keysOf(m.Remind1d))
		require.Zero(t, m.SubnetTotal, "unknown")
	})
}

func TestMaintainMakesKeysOfUnlimitedUsersForever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		trial, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		) // a 7-day trial key
		require.NoError(t, err)
		stored := e.peers.m[trial.PublicKey]
		stored.ExpiresAt = now.Add(-time.Hour) // the trial ran out...
		stored.Reminded3d = true
		stored.Reminded1d = true
		e.peers.m[trial.PublicKey] = stored
		_, err = e.svc.Maintain(ctx) // ...and expiry disabled the key
		require.NoError(t, err)
		require.False(t, e.peers.m[trial.PublicKey].Enabled)
		e.setRole(roleInput{ID: 42, Role: RoleUnlimited}) // then the admin made them unlimited

		m, err := e.svc.Maintain(ctx)
		require.NoError(t, err)

		require.Equal(t, []string{trial.PublicKey}, keysOf(m.MadeForever))
		require.Empty(t, m.Expired, "not expired: the role wins")
		got := e.peers.m[trial.PublicKey]
		require.True(t, got.ExpiresAt.IsZero(), "never expires")
		require.True(t, got.Enabled, "back on")
		require.False(t, got.Reminded3d)
		require.False(t, got.Reminded1d)
		require.True(t, e.vpn.hasPeer(trial.PublicKey), "back on the server, same key")

		m, err = e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Empty(t, m.MadeForever, "done once")
	})
}

func TestMaintainLeavesPlainUsersKeysAlone(t *testing.T) {
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

		m, err := e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.Empty(t, m.MadeForever)
		require.Equal(t, now.AddDate(0, 0, 7), e.peers.m[p.PublicKey].ExpiresAt)
	})
}

func TestMaintainCountsOnlinePeers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.vpn.stats = []PeerStat{
			{
				PublicKey:     "MANUAL1=",
				LastHandshake: now.Add(-time.Minute),
			},
			{
				PublicKey:     "KEY2=",
				LastHandshake: now.Add(-10 * time.Minute),
			},
			{
				PublicKey: "KEY3=",
			},
		}

		m, err := e.svc.Maintain(context.Background())
		require.NoError(t, err)
		require.Equal(t, 1, m.Online, "only the recent handshake counts, manual peers too")
	})
}

func TestMaintainOnlineUnknownWhenStatsFail(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		expired := e.seed(t, now.Add(-time.Minute))
		e.vpn.statsErr = errBoom

		m, err := e.svc.Maintain(context.Background())
		require.NoError(t, err, "the notices must still go out")
		require.Equal(t, []string{expired.PublicKey}, keysOf(m.Expired))
		require.Equal(t, -1, m.Online)
	})
}

// seed issues a key for user 42 that ends at `ends` (zero = never).
func (s *env) seed(t *testing.T, ends time.Time) *Peer {
	t.Helper()
	p := s.issue(t, 30)
	stored := s.peers.m[p.PublicKey]
	stored.ExpiresAt = ends
	s.peers.m[p.PublicKey] = stored
	return &stored
}

func keysOf(ps []*Peer) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.PublicKey)
	}
	return out
}

// The once-only log is keyed by the error kind, not its text: a wrapped
// ErrIPTaken with a new message each run still logs once.
func TestMaintainLogsSkippedKindOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		p, err := e.svc.CreateKey(ctx, CreateKeyInput{UserID: 42})
		require.NoError(t, err)
		row := e.peers.m[p.PublicKey]
		row.Enabled = false
		row.ExpiresAt = now.Add(-time.Hour)
		e.peers.m[p.PublicKey] = row
		delete(e.vpn.peers, p.PublicKey)
		e.setRole(roleInput{ID: 42, Role: RoleUnlimited})
		run := 0
		e.vpn.onChange = func() {
			run++
			e.vpn.err = fmt.Errorf("%w: run %d", ErrIPTaken, run)
		}

		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)
		for range 3 {
			_, err = e.svc.Maintain(ctx)
			require.NoError(t, err)
		}

		require.Equal(t, 3, run)
		require.Equal(t, 1, strings.Count(buf.String(), "forever"), buf.String())
	})
}

func TestMaintainLogsSkippedKeyOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		p, err := e.svc.CreateKey(ctx, CreateKeyInput{UserID: 42})
		require.NoError(t, err)
		row := e.peers.m[p.PublicKey]
		row.PSK = "" // unreadable secrets: it can't go back on the server
		row.Enabled = false
		row.ExpiresAt = now.Add(-time.Hour)
		e.peers.m[p.PublicKey] = row
		e.setRole(roleInput{ID: 42, Role: RoleUnlimited})

		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)
		for range 3 {
			_, err = e.svc.Maintain(ctx)
			require.NoError(t, err)
		}

		require.Equal(t, 1, strings.Count(buf.String(), "forever"), buf.String())
	})
}
