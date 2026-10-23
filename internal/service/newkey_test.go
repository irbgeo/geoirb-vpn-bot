package service

import (
	"context"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPlainUserBuysASecondKeyWithItsOwnTerm(t *testing.T) {
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

		_, err = e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.ErrorIs(t, err, ErrHasKey, "the trial is for the first key only")

		res := e.payNewKey(t, "c1")

		require.True(t, res.NewKey)
		require.NotEqual(t, trial.PublicKey, res.Peer.PublicKey)
		require.NotEqual(t, trial.Name, res.Peer.Name)
		require.Equal(t, now.AddDate(0, 0, 30), res.Peer.ExpiresAt)
		require.Equal(t, now.AddDate(0, 0, 7), e.peers.m[trial.PublicKey].ExpiresAt, "the trial key keeps its term")
		require.Equal(t, res.Peer.PublicKey, e.payments.m["c1"].PeerKey)

		time.Sleep(8 * 24 * time.Hour)
		_, err = e.svc.Maintain(ctx)
		require.NoError(t, err)
		require.False(t, e.peers.m[trial.PublicKey].Enabled, "the trial key expired")
		require.True(t, e.peers.m[res.Peer.PublicKey].Enabled, "the paid key runs on")
	})
}

func TestPlainUserNewKeyLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		for i := range MaxUserKeys {
			e.payNewKey(t, "c"+strconv.Itoa(i))
		}

		_, err := e.svc.Invoice(
			context.Background(),
			PurchaseInput{
				UserID:    42,
				Days:      30,
				PublicKey: BuyNewKey,
			},
		)
		require.ErrorIs(t, err, ErrKeyLimit)
	})
}

func TestNewKeyIsNotForSaleToUnlimited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUnlimited)

		_, err := e.svc.Invoice(
			context.Background(),
			PurchaseInput{
				UserID:    42,
				Days:      30,
				PublicKey: BuyNewKey,
			},
		)
		require.ErrorIs(t, err, ErrNotForSale)
	})
}

func (s *env) payNewKey(t *testing.T, charge string) *PayResult {
	t.Helper()
	payload := s.invoice(
		t,
		PurchaseInput{
			UserID:    42,
			Days:      30,
			PublicKey: BuyNewKey,
		},
	)
	res, err := s.svc.Pay(
		context.Background(),
		PaymentInput{
			ChargeID: charge,
			PayerID:  42,
			Payload:  payload,
			Stars:    150,
		},
	)
	require.NoError(t, err)
	return res
}

func TestNewKeyIsRefusedWhileAKeyIsBlocked(t *testing.T) {
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
		require.NoError(t, e.svc.Disable(ctx, trial.PublicKey))

		_, err = e.svc.Invoice(
			ctx,
			PurchaseInput{
				UserID:    42,
				Days:      30,
				PublicKey: BuyNewKey,
			},
		)
		require.ErrorIs(t, err, ErrBlocked, "a new key must not get round the admin's block")
	})
}
