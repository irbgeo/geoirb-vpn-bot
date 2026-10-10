package service

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNoKeyPurchaseSkipsBlockedKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		blocked := e.issue(t, 30)
		other := e.issue(t, 30)
		require.NoError(t, e.svc.Disable(context.Background(), blocked.PublicKey))
		before := e.peers.m[other.PublicKey].ExpiresAt

		e.pay(t, "c1")

		require.True(t, e.peers.m[other.PublicKey].ExpiresAt.After(before), "the open key is extended")
		require.True(t, e.peers.m[blocked.PublicKey].Blocked)
		require.False(t, e.peers.m[blocked.PublicKey].Enabled)
	})
}

func TestNoKeyPurchaseOnlyBlockedKeyRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		p := e.issue(t, 30)
		require.NoError(t, e.svc.Disable(ctx, p.PublicKey))
		buy := PurchaseInput{
			UserID: 42,
			Days:   30,
		}
		pay := PaymentInput{
			ChargeID: "c1",
			PayerID:  42,
			Payload:  payloadVersion + "|42|30|150|",
			Stars:    150,
		}

		_, err := e.svc.Invoice(ctx, buy)
		require.ErrorIs(t, err, ErrBlocked)
		require.ErrorIs(t, e.svc.CheckPurchase(ctx, pay), ErrBlocked)
		_, err = e.svc.Pay(ctx, pay)
		require.ErrorIs(t, err, ErrBlocked)
		require.True(t, e.peers.m[p.PublicKey].Blocked)
		require.Equal(t, p.ExpiresAt, e.peers.m[p.PublicKey].ExpiresAt)
	})
}

func TestPayExistingUnappliedRecordNotReapplied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		p := e.issue(t, 5)
		e.payments.m["c1"] = Payment{
			ChargeID:  "c1",
			UserID:    42,
			PeerKey:   p.PublicKey,
			Stars:     150,
			Days:      30,
			CreatedAt: time.Now(),
		}
		payload := e.invoice(
			t,
			PurchaseInput{
				UserID: 42,
				Days:   30,
			},
		)

		res, err := e.svc.Pay(
			context.Background(),
			PaymentInput{
				ChargeID: "c1",
				PayerID:  42,
				Payload:  payload,
				Stars:    150,
			},
		)

		require.NoError(t, err)
		require.True(t, res.NeedsReview)
		require.Equal(t, p.ExpiresAt, e.peers.m[p.PublicKey].ExpiresAt)
		require.False(t, e.payments.m["c1"].Applied)
	})
}

func TestAdminDisabledKeyCannotBeBoughtBack(t *testing.T) {
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
	})
}
