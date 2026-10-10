package service

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInvoiceForPlainUserOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()

		inv, err := e.svc.Invoice(
			ctx,
			PurchaseInput{
				UserID: 42,
				Days:   30,
			},
		)
		require.NoError(t, err)
		require.Equal(t, 150, inv.Stars)
		require.Equal(t, 30, inv.Days)
		require.NotEmpty(t, inv.Payload)

		_, err = e.svc.Invoice(
			ctx,
			PurchaseInput{
				UserID: 42,
				Days:   45,
			},
		)
		require.ErrorIs(t, err, ErrNoTariff)

		e.setRole(roleInput{ID: 42, Role: RoleUnlimited})
		_, err = e.svc.Invoice(
			ctx,
			PurchaseInput{
				UserID: 42,
				Days:   30,
			},
		)
		require.ErrorIs(t, err, ErrNotForSale, "unlimited users don't pay")
	})
}

func TestCheckPurchase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		payload := e.invoice(
			t,
			PurchaseInput{
				UserID: 42,
				Days:   30,
			},
		)

		require.NoError(t, e.svc.CheckPurchase(
			ctx,
			PaymentInput{
				PayerID: 42,
				Payload: payload,
				Stars:   150,
			},
		))
		require.ErrorIs(t, e.svc.CheckPurchase(
			ctx,
			PaymentInput{
				PayerID: 7,
				Payload: payload,
				Stars:   150,
			},
		), ErrWrongPayer, "someone else's invoice")
		require.ErrorIs(t, e.svc.CheckPurchase(
			ctx,
			PaymentInput{
				PayerID: 42,
				Payload: payload,
				Stars:   1,
			},
		), ErrPriceChanged)
		require.ErrorIs(t, e.svc.CheckPurchase(
			ctx,
			PaymentInput{
				PayerID: 42,
				Payload: "garbage",
				Stars:   150,
			},
		), ErrBadPayload)
	})
}

func TestPayWithoutKeyIssuesOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)

		res := e.pay(t, "c1")

		require.True(t, res.NewKey)
		require.Equal(t, now.AddDate(0, 0, 30), res.Peer.ExpiresAt)
		require.True(t, e.users().m[42].TrialUsed, "no trial after buying")
		require.Equal(
			t,
			Payment{
				ChargeID:  "c1",
				UserID:    42,
				PeerKey:   res.Peer.PublicKey,
				Stars:     150,
				Days:      30,
				Applied:   true,
				CreatedAt: now,
			},
			e.payments.m["c1"],
		)
	})
}

func TestPayExtendsExistingKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		trial, err := e.svc.CreateKey(
			context.Background(),
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)

		res := e.pay(t, "c1")

		require.False(t, res.NewKey)
		require.Equal(t, trial.PublicKey, res.Peer.PublicKey)
		require.Equal(t, now.AddDate(0, 0, 7+30), res.Peer.ExpiresAt, "added to the trial end")
	})
}

func TestPayChosenKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		first, err := e.svc.CreateKey(
			ctx,
			CreateKeyInput{
				UserID: 42,
			},
		)
		require.NoError(t, err)
		second, err := e.svc.Issue(
			ctx,
			IssueInput{
				UserID: 42,
				Days:   10,
			},
		)
		require.NoError(t, err)

		payload := e.invoice(
			t,
			PurchaseInput{
				UserID:    42,
				Days:      30,
				PublicKey: second.PublicKey,
			},
		)
		res, err := e.svc.Pay(
			ctx,
			PaymentInput{
				ChargeID: "c1",
				PayerID:  42,
				Payload:  payload,
				Stars:    150,
			},
		)
		require.NoError(t, err)
		require.Equal(t, second.PublicKey, res.Peer.PublicKey)
		require.Equal(t, now.AddDate(0, 0, 7), e.peers.m[first.PublicKey].ExpiresAt, "other key untouched")
	})
}

func TestPaySameChargeTwiceExtendsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		first := e.pay(t, "c1")

		again := e.pay(t, "c1")

		require.True(t, again.Repeat)
		require.Equal(t, first.Peer.ExpiresAt, e.peers.m[first.Peer.PublicKey].ExpiresAt, "not extended twice")
	})
}

func TestPayRejectsBadPurchase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)

		_, err := e.svc.Pay(
			context.Background(),
			PaymentInput{
				ChargeID: "c1",
				PayerID:  7,
				Payload: e.invoice(
					t,
					PurchaseInput{
						UserID: 42,
						Days:   30,
					},
				),
				Stars: 150,
			},
		)
		require.ErrorIs(t, err, ErrWrongPayer)
		require.Empty(t, e.payments.m, "nothing recorded: the bot refunds it")
	})
}

func TestMarkRefunded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		e.pay(t, "c1")

		require.NoError(t, e.svc.MarkRefunded(ctx, "c1"))
		require.Equal(t, now, e.payments.m["c1"].RefundedAt)
		require.NoError(t, e.svc.MarkRefunded(ctx, "unknown"), "a refund before the record was saved is fine")
	})
}

func TestPaymentsOfUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		e.pay(t, "c1")

		ps, err := e.svc.Payments(context.Background(), 42)
		require.NoError(t, err)
		require.Len(t, ps, 1)
		require.Equal(t, "c1", ps[0].ChargeID)
	})
}

func TestPayRepeatSkipsPurchaseChecks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		first := e.pay(t, "c1")
		payload := e.invoice(
			t,
			PurchaseInput{
				UserID: 42,
				Days:   30,
			},
		)
		e.svc.cfg.Tariffs = nil // prices changed after the charge was applied

		res, err := e.svc.Pay(
			context.Background(),
			PaymentInput{
				ChargeID: "c1",
				PayerID:  42,
				Payload:  payload,
				Stars:    150,
			},
		)
		require.NoError(t, err, "a repeat must not turn into a refund")
		require.True(t, res.Repeat)
		require.Equal(t, first.Peer.PublicKey, res.Peer.PublicKey)
	})
}

func TestPayRefundedChargeIsNotApplied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		e.payments.m["c1"] = Payment{
			ChargeID:   "c1",
			UserID:     42,
			Stars:      150,
			Days:       30,
			RefundedAt: now,
		}

		_, err := e.svc.Pay(
			context.Background(),
			PaymentInput{
				ChargeID: "c1",
				PayerID:  42,
				Payload: e.invoice(
					t,
					PurchaseInput{
						UserID: 42,
						Days:   30,
					},
				),
				Stars: 150,
			},
		)
		require.ErrorIs(t, err, ErrAlreadyRefunded)
		require.Empty(t, e.peers.m, "no key for a refunded charge")
	})
}

func TestPaySaveFailureAfterApplyIsNotAnError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		e.payments.saveErr = errBoom

		res := e.pay(t, "c1")
		require.True(t, res.NewKey, "days were given: no refund")
	})
}

// A write whose reply was lost (shutdown, network cut) is still stored: the
// days are given, so Pay must not report an error that ends in a refund.
func TestPayIsNotAnErrorWhenTheExtendWasStoredButItsReplyLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		key := e.seed(t, now.AddDate(0, 0, 10))
		e.peers.saveLost = errBoom

		res := e.pay(t, "c1")

		require.Equal(t, key.PublicKey, res.Peer.PublicKey)
		require.Equal(t, now.AddDate(0, 0, 40), e.peers.m[key.PublicKey].ExpiresAt)
		require.True(t, e.payments.m["c1"].Applied)
	})
}

func TestExtendOfADisabledKeyStaysOnWhenTheSaveReplyWasLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		p := expiredKey(t, e)
		e.peers.saveLost = errBoom

		_, err := e.svc.Extend(
			context.Background(),
			ExtendInput{
				PublicKey: p.PublicKey,
				Days:      30,
			},
		)
		require.NoError(t, err)
		require.True(t, e.peers.m[p.PublicKey].Enabled)
		require.True(t, e.vpn.hasPeer(p.PublicKey), "the DB says enabled, so the server runs it")
	})
}

func TestUnfinishedPayments(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.payments.m = map[string]Payment{
			"done": {
				ChargeID:  "done",
				Applied:   true,
				CreatedAt: now,
			},
			"refunded": {
				ChargeID:   "refunded",
				CreatedAt:  now,
				RefundedAt: now,
			},
			"stuck": {
				ChargeID:  "stuck",
				CreatedAt: now.Add(-time.Hour),
			},
			"ancient": {
				ChargeID:  "ancient",
				CreatedAt: now.AddDate(0, 0, -40),
			},
		}

		ps, err := e.svc.UnfinishedPayments(context.Background())
		require.NoError(t, err)
		require.Len(t, ps, 1)
		require.Equal(t, "stuck", ps[0].ChargeID)
	})
}

func (s *env) invoice(t *testing.T, in PurchaseInput) string {
	t.Helper()
	payload, err := s.svc.Invoice(context.Background(), in)
	require.NoError(t, err)
	return payload.Payload
}

func (s *env) pay(t *testing.T, charge string) *PayResult {
	t.Helper()
	payload := s.invoice(
		t,
		PurchaseInput{
			UserID: 42,
			Days:   30,
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
