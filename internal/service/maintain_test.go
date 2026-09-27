package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// seed issues a key for user 42 that ends at `ends` (zero = never).
func seed(t *testing.T, e *env, ends time.Time) *Peer {
	t.Helper()
	p := e.issue(t, 30)
	stored := e.peers.m[p.PublicKey]
	stored.ExpiresAt = ends
	e.peers.m[p.PublicKey] = stored
	return &stored
}

func keysOf(ps []*Peer) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.PublicKey)
	}
	return out
}

func TestMaintainExpiresAndDisables(t *testing.T) {
	e := newEnv()
	expired := seed(t, e, now.Add(-time.Minute))
	active := seed(t, e, now.AddDate(0, 0, 10))
	forever := seed(t, e, time.Time{})

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
}

func TestMaintainRemindersOncePerTerm(t *testing.T) {
	e := newEnv()
	in2days := seed(t, e, now.Add(48*time.Hour))
	in12h := seed(t, e, now.Add(12*time.Hour))
	seed(t, e, now.AddDate(0, 0, 5))
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
			Days:      1,
		},
	)
	require.NoError(t, err)
	m, err = e.svc.Maintain(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{in12h.PublicKey}, keysOf(m.Remind3d), "extension resets reminders")
}

func TestMaintainSubnetUsage(t *testing.T) {
	e := newEnv()
	p := seed(t, e, now.AddDate(0, 0, 10))
	require.NoError(t, e.svc.Disable(context.Background(), p.PublicKey))

	m, err := e.svc.Maintain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, m.SubnetUsed, "manual peer .1 + disabled key's reserved .2")
	require.Equal(t, 254, m.SubnetTotal)
}

func TestMaintainSubnetFailureStillReportsKeys(t *testing.T) {
	e := newEnv()
	expired := seed(t, e, now.Add(-time.Minute))
	soon := seed(t, e, now.Add(12*time.Hour))
	e.vpn.readErr = errBoom

	m, err := e.svc.Maintain(context.Background())
	require.NoError(t, err, "the notices must still go out")
	require.Equal(t, []string{expired.PublicKey}, keysOf(m.Expired))
	require.Equal(t, []string{soon.PublicKey}, keysOf(m.Remind1d))
	require.Zero(t, m.SubnetTotal, "unknown")
}

func TestMaintainMakesKeysOfUnlimitedUsersForever(t *testing.T) {
	e := newEnv()
	register(t, e, RoleUser)
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
	e.setRole(t, 42, RoleUnlimited) // then the admin made them unlimited

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
}

func TestMaintainLeavesPlainUsersKeysAlone(t *testing.T) {
	e := newEnv()
	register(t, e, RoleUser)
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
}
