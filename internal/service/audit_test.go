package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Tests for the fixes from the 2026-09-27 audit.

func TestPayRepeatIgnoresKeyLookupError(t *testing.T) {
	e := newEnv()
	register(t, e, RoleUser)
	pay(t, e, "c1")
	e.peers.getErr = errBoom // the DB hiccups on the repeat

	payload := invoice(
		t,
		e,
		PurchaseInput{
			UserID: 42,
			Days:   30,
		},
	)
	e.peers.getErr = errBoom
	res, err := e.svc.Pay(
		context.Background(),
		PaymentInput{
			ChargeID: "c1",
			PayerID:  42,
			Payload:  payload,
			Stars:    150,
		},
	)
	require.NoError(t, err, "a repeat must never become a refund")
	require.True(t, res.Repeat)
}

func TestExtendRollsBackServerWhenSaveFails(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	p := e.issue(t, 30)
	require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
	e.peers.saveErr = errBoom

	_, err := e.svc.Extend(
		ctx,
		ExtendInput{
			PublicKey: p.PublicKey,
			Days:      30,
		},
	)
	require.ErrorIs(t, err, errBoom)
	require.False(t, e.vpn.hasPeer(p.PublicKey), "not left live while the DB says disabled")
}

func TestEnableRollsBackServerWhenSaveFails(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	p := e.issue(t, 30)
	require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
	e.peers.saveErr = errBoom

	require.ErrorIs(t, e.svc.Enable(ctx, p.PublicKey), errBoom)
	require.False(t, e.vpn.hasPeer(p.PublicKey))
}

func TestEnableExpiredKeyNeedsExtend(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	p := seed(t, e, now.Add(-time.Hour))
	require.NoError(t, e.svc.Disable(ctx, p.PublicKey))

	require.ErrorIs(t, e.svc.Enable(ctx, p.PublicKey), ErrExpired)
	require.False(t, e.vpn.hasPeer(p.PublicKey))
}

func TestPayRetriesTheAppliedMark(t *testing.T) {
	e := newEnv()
	register(t, e, RoleUser)
	e.payments.saveFails = 1

	pay(t, e, "c1")
	require.True(t, e.payments.m["c1"].Applied, "the second try saved it")
}

func TestForeverKeyIsNotForSale(t *testing.T) {
	e := newEnv()
	register(t, e, RoleUser)
	ctx := context.Background()
	forever, err := e.svc.Issue(
		ctx,
		IssueInput{
			UserID: 42,
		},
	)
	require.NoError(t, err)

	_, err = e.svc.Invoice(
		ctx,
		PurchaseInput{
			UserID:    42,
			Days:      30,
			PublicKey: forever.PublicKey,
		},
	)
	require.ErrorIs(t, err, ErrNotForSale, "that key never ends")

	_, err = e.svc.Invoice(
		ctx,
		PurchaseInput{
			UserID: 42,
			Days:   30,
		},
	)
	require.ErrorIs(t, err, ErrNotForSale, "all their keys are forever")
}

func TestPayExtendsTheTimedKeyNotTheForeverOne(t *testing.T) {
	e := newEnv()
	register(t, e, RoleUser)
	ctx := context.Background()
	_, err := e.svc.Issue(
		ctx,
		IssueInput{
			UserID: 42,
		},
	) // .2, forever
	require.NoError(t, err)
	timed, err := e.svc.Issue(
		ctx,
		IssueInput{
			UserID: 42,
			Days:   5,
		},
	) // .3
	require.NoError(t, err)

	res := pay(t, e, "c1")
	require.Equal(t, timed.PublicKey, res.Peer.PublicKey)
	require.Equal(t, now.AddDate(0, 0, 5+30), res.Peer.ExpiresAt)
}

func TestMaintainStopsAtTheFirstServerError(t *testing.T) {
	e := newEnv()
	seed(t, e, now.Add(-time.Hour))
	seed(t, e, now.Add(-time.Hour))
	seed(t, e, now.Add(-time.Hour))
	e.vpn.syncErr = errBoom
	e.vpn.updates = 0

	m, err := e.svc.Maintain(context.Background())
	require.NoError(t, err)
	require.Empty(t, m.Expired)
	require.Equal(t, 1, e.vpn.updates, "no more docker calls after the first failure; the next run retries")
}

func TestIssueRemovesAPeerLeftOnTheServer(t *testing.T) {
	e := newEnv()
	e.vpn.appliedErr = errBoom // the peer got on the server, then the call "failed"

	_, err := e.svc.Issue(
		context.Background(),
		IssueInput{
			Name: "x",
			Days: 30,
		},
	)
	require.ErrorIs(t, err, errBoom)
	require.Empty(t, e.peers.m)
	require.False(t, e.vpn.hasPeer("PUB1="), "no peer without an owner")
}

func TestRegisterKeepsARoleSetByHandInBetween(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	register(t, e, RoleUnlimited)

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
}
