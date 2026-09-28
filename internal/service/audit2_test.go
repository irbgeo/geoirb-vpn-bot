package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// expiredKey issues a key and lets Maintain disable it: a disabled key
// whose term ended, ready to be extended.
func expiredKey(t *testing.T, e *env) *Peer {
	t.Helper()
	p := seed(t, e, now.Add(-time.Minute))
	_, err := e.svc.Maintain(context.Background())
	require.NoError(t, err)
	require.False(t, e.vpn.hasPeer(p.PublicKey))
	return p
}

func TestExtendFailureOnTheServerKeepsTheKeyDisabled(t *testing.T) {
	e := newEnv()
	p := expiredKey(t, e)
	e.vpn.err = errBoom

	_, err := e.svc.Extend(
		context.Background(),
		ExtendInput{
			PublicKey: p.PublicKey,
			Days:      30,
		},
	)
	require.Error(t, err)
	require.False(t, e.vpn.hasPeer(p.PublicKey), "the DB says disabled, so the server must not run it")
	require.NotContains(t, e.vpn.table, p.PublicKey, "not listed in the Amnezia app either")
	require.False(t, e.peers.m[p.PublicKey].Enabled)
}

func TestExtendSaveFailureTakesTheKeyOffTheAppToo(t *testing.T) {
	e := newEnv()
	p := expiredKey(t, e)
	e.peers.saveErr = errBoom

	_, err := e.svc.Extend(
		context.Background(),
		ExtendInput{
			PublicKey: p.PublicKey,
			Days:      30,
		},
	)
	require.Error(t, err)
	require.False(t, e.vpn.hasPeer(p.PublicKey))
	require.NotContains(t, e.vpn.table, p.PublicKey)
}

func TestMaintainSkipsAKeyWhoseIPIsTakenAndGoesOn(t *testing.T) {
	e := newEnv()
	e.users().m[7] = User{
		ID:   7,
		Role: RoleUnlimited,
	}
	// the manual peer MANUAL1 holds 10.8.1.1 on the server
	e.peers.m["STUCK="] = Peer{
		PublicKey: "STUCK=",
		ServerID:  "srv",
		UserID:    7,
		IP:        "10.8.1.1",
		PSK:       "PSK=",
		ExpiresAt: now.AddDate(0, 0, 3),
	}
	expired := seed(t, e, now.Add(-time.Minute))

	m, err := e.svc.Maintain(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{expired.PublicKey}, keysOf(m.Expired), "keys after the stuck one still expire")
	require.Empty(t, m.MadeForever)
}

func TestAdminDisabledKeyCannotBeBoughtBack(t *testing.T) {
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
	buy := PurchaseInput{
		UserID:    42,
		Days:      30,
		PublicKey: p.PublicKey,
	}

	require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
	_, err = e.svc.Invoice(ctx, buy)
	require.ErrorIs(t, err, ErrBlocked)

	require.NoError(t, e.svc.Enable(ctx, p.PublicKey))
	_, err = e.svc.Invoice(ctx, buy)
	require.NoError(t, err, "enabled by the admin again: can be bought")
}

func TestAdminExtendUnblocks(t *testing.T) {
	e := newEnv()
	p := e.issue(t, 30)
	ctx := context.Background()
	require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
	require.True(t, e.peers.m[p.PublicKey].Blocked)

	_, err := e.svc.Extend(
		ctx,
		ExtendInput{
			PublicKey: p.PublicKey,
			Days:      30,
		},
	)
	require.NoError(t, err)
	require.False(t, e.peers.m[p.PublicKey].Blocked, "the admin chose to give access again")
}

func TestExpiredKeyIsNotBlocked(t *testing.T) {
	e := newEnv()
	p := expiredKey(t, e)
	require.False(t, e.peers.m[p.PublicKey].Blocked, "expiry is not an admin block: the user may pay")
}

func TestRefundDuringPayIsNotLost(t *testing.T) {
	e := newEnv()
	register(t, e, RoleUser)
	// the admin refunds while Pay is applying the days
	e.vpn.onChange = func() {
		require.NoError(t, e.svc.MarkRefunded(context.Background(), "c1"))
	}

	pay(t, e, "c1")
	got := e.payments.m["c1"]
	require.True(t, got.Applied)
	require.False(t, got.RefundedAt.IsZero(), "Pay's own mark does not wipe the refund")
}

func TestPayRecordsTheKeyItExtendsBeforeApplying(t *testing.T) {
	e := newEnv()
	register(t, e, RoleUser)
	ctx := context.Background()
	trial, err := e.svc.CreateKey(
		ctx,
		CreateKeyInput{
			UserID: 42,
		},
	)
	require.NoError(t, err)
	stored := e.peers.m[trial.PublicKey]
	stored.ExpiresAt = now.Add(-time.Minute)
	e.peers.m[trial.PublicKey] = stored
	_, err = e.svc.Maintain(ctx)
	require.NoError(t, err)
	payload := invoice(
		t,
		e,
		PurchaseInput{
			UserID: 42,
			Days:   30,
		},
	)
	e.vpn.err = errBoom

	_, err = e.svc.Pay(
		ctx,
		PaymentInput{
			ChargeID: "c1",
			PayerID:  42,
			Payload:  payload,
			Stars:    150,
		},
	)
	require.Error(t, err)
	require.Equal(t, trial.PublicKey, e.payments.m["c1"].PeerKey, "admins see which key to check")
	require.False(t, e.payments.m["c1"].Applied)
}

func TestMaintainExpiresAKeyWhoseSecretsAreUnreadable(t *testing.T) {
	e := newEnv()
	p := seed(t, e, now.Add(-time.Minute))
	stored := e.peers.m[p.PublicKey]
	stored.PrivateKey, stored.PSK = "", "" // secrets that could not be read
	e.peers.m[p.PublicKey] = stored

	m, err := e.svc.Maintain(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{p.PublicKey}, keysOf(m.Expired))
	require.False(t, e.vpn.hasPeer(p.PublicKey))
}

func TestUnreadableKeyIsNotPutBackOnTheServer(t *testing.T) {
	e := newEnv()
	e.users().m[7] = User{
		ID:   7,
		Role: RoleUnlimited,
	}
	e.peers.m["SEALED="] = Peer{
		PublicKey: "SEALED=",
		ServerID:  "srv",
		UserID:    7,
		IP:        "10.8.1.2",
		ExpiresAt: now.AddDate(0, 0, 3), // no PSK: secrets could not be read
	}
	expired := seed(t, e, now.Add(-time.Minute))

	m, err := e.svc.Maintain(context.Background())
	require.NoError(t, err)
	require.False(t, e.vpn.hasPeer("SEALED="), "no PSK to put it back with")
	require.Equal(t, []string{expired.PublicKey}, keysOf(m.Expired), "the rest go on")
}

func TestForeverKeepsAnAdminBlock(t *testing.T) {
	e := newEnv()
	e.users().m[42] = User{
		ID:   42,
		Role: RoleUnlimited,
	}
	p := seed(t, e, now.AddDate(0, 0, 3))
	ctx := context.Background()
	require.NoError(t, e.svc.Disable(ctx, p.PublicKey))

	m, err := e.svc.Maintain(ctx)
	require.NoError(t, err)
	got := e.peers.m[p.PublicKey]
	require.True(t, got.ExpiresAt.IsZero(), "the end date goes")
	require.False(t, got.Enabled, "but the block stays")
	require.False(t, e.vpn.hasPeer(p.PublicKey))
	require.Empty(t, m.MadeForever, "a blocked user is not told their access is forever")
}
