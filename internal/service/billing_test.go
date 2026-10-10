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

// The owner check is the only thing between a forged button or payload and
// another user's key (extending also lifts an admin block).
func TestPurchaseOfAnotherUsersKeyIsNotFound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		theirs := e.seed(t, now.AddDate(0, 0, 10)) // user 42's key
		_, err := e.svc.Register(
			ctx,
			RegisterInput{
				ID:       7,
				Username: "eve",
			},
		)
		require.NoError(t, err)

		_, err = e.svc.Invoice(
			ctx,
			PurchaseInput{
				UserID:    7,
				Days:      30,
				PublicKey: theirs.PublicKey,
			},
		)
		require.ErrorIs(t, err, ErrNotFound)

		forged := PaymentInput{
			ChargeID: "c1",
			PayerID:  7,
			Payload:  "v1|7|30|150|" + theirs.PublicKey,
			Stars:    150,
		}
		require.ErrorIs(t, e.svc.CheckPurchase(ctx, forged), ErrNotFound)
		_, err = e.svc.Pay(ctx, forged)
		require.ErrorIs(t, err, ErrNotFound)
		require.False(t, e.payments.m["c1"].Applied, "recorded for the refund, never applied")
		require.Equal(t, now.AddDate(0, 0, 10), e.peers.m[theirs.PublicKey].ExpiresAt)
	})
}

func TestPaySameChargeTwiceExtendsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		first := e.pay(t, "c1")

		again := e.pay(t, "c1")

		require.Equal(
			t,
			&PayResult{
				Repeat: true,
			},
			again,
			"a repeat reads nothing that could fail",
		)
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
		require.Equal(
			t,
			Payment{
				ChargeID:  "c1",
				UserID:    7,
				Stars:     150,
				CreatedAt: now,
			},
			e.payments.m["c1"],
			"the charge is real: recorded under the payer, so a failed refund can be found",
		)
		left, err := e.svc.UnfinishedPayments(context.Background())
		require.NoError(t, err)
		require.Len(t, left, 1, "admins see it until the Stars go back")
	})
}

func TestMarkRefunded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		e.pay(t, "c1")

		require.NoError(
			t,
			e.svc.MarkRefunded(
				ctx,
				RefundInput{
					ChargeID: "c1",
				},
			),
		)
		got := e.payments.m["c1"]
		require.Equal(t, now, got.RefundedAt)
		require.Equal(t, 150, got.Stars, "the record itself is not rewritten")
		require.True(t, got.Applied)
	})
}

// A charge that could not be recorded and was refunded still gets a record:
// if Telegram delivers the same payment again after a restart, it must not
// be applied as a new one.
func TestRefundOfAnUnrecordedChargeIsRemembered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		paymentInput := PaymentInput{
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
		}
		e.payments.addErr = errBoom
		_, err := e.svc.Pay(ctx, paymentInput)
		require.ErrorIs(t, err, errBoom)
		require.Empty(t, e.payments.m, "the charge has no record")

		// the bot returns the Stars and records it
		require.NoError(
			t,
			e.svc.MarkRefunded(
				ctx,
				RefundInput{
					ChargeID: "c1",
					UserID:   42,
					Stars:    150,
				},
			),
		)
		require.Equal(
			t,
			Payment{
				ChargeID:   "c1",
				UserID:     42,
				Stars:      150,
				CreatedAt:  now,
				RefundedAt: now,
			},
			e.payments.m["c1"],
			"a record of a refused charge: no key, no days",
		)

		e.payments.addErr = nil
		_, err = e.svc.Pay(ctx, paymentInput)
		require.ErrorIs(t, err, ErrAlreadyRefunded, "the redelivered payment is not applied")
		require.Empty(t, e.peers.m)
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
		require.Equal(t, first.Peer.ExpiresAt, e.peers.m[first.Peer.PublicKey].ExpiresAt)
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

// A disabled key without readable secrets can't go back on the server, so
// it must be refused before Telegram charges, not refunded after.
func TestPurchaseForADeadUnreadableKeyIsRefusedBeforePaying(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		ctx := context.Background()
		p := expiredKey(t, e)
		row := e.peers.m[p.PublicKey]
		row.PrivateKey, row.PSK = "", ""
		e.peers.m[p.PublicKey] = row

		for _, key := range []string{
			"",
			p.PublicKey,
		} {
			_, err := e.svc.Invoice(
				ctx,
				PurchaseInput{
					UserID:    42,
					Days:      30,
					PublicKey: key,
				},
			)
			require.ErrorIs(t, err, ErrUnreadable, "key %q", key)
			err = e.svc.CheckPurchase(
				ctx,
				PaymentInput{
					PayerID: 42,
					Payload: "v1|42|30|150|" + key,
					Stars:   150,
				},
			)
			require.ErrorIs(t, err, ErrUnreadable, "key %q", key)
		}
	})
}

// An enabled key is already on the server: extending it needs no secrets.
func TestPurchaseForAnEnabledUnreadableKeyWorks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		p := e.seed(t, now.AddDate(0, 0, 10))
		row := e.peers.m[p.PublicKey]
		row.PrivateKey, row.PSK = "", ""
		e.peers.m[p.PublicKey] = row

		res := e.pay(t, "c1")
		require.Equal(t, p.PublicKey, res.Peer.PublicKey)
		require.Equal(t, now.AddDate(0, 0, 40), e.peers.m[p.PublicKey].ExpiresAt)
	})
}

// A charge recorded by someone else between Pay's lookup and its own
// record must not be applied a second time.
func TestPayDoesNotApplyAChargeSomeoneElseRecorded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		key := e.seed(t, now.AddDate(0, 0, 10))
		e.payments.addTaken = true

		res := e.pay(t, "c1")

		require.True(t, res.NeedsReview)
		require.Equal(t, now.AddDate(0, 0, 10), e.peers.m[key.PublicKey].ExpiresAt, "no days added")
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

func TestPayRepeatIgnoresKeyLookupError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		e.pay(t, "c1")
		e.peers.getErr = errBoom // the DB hiccups on the repeat

		payload := e.invoice(
			t,
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
	})
}

func TestPayRetriesTheAppliedMark(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		e.payments.saveFails = 1

		e.pay(t, "c1")
		require.True(t, e.payments.m["c1"].Applied, "the second try saved it")
	})
}

func TestForeverKeyIsNotForSale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
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
	})
}

func TestPayExtendsTheTimedKeyNotTheForeverOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
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

		res := e.pay(t, "c1")
		require.Equal(t, timed.PublicKey, res.Peer.PublicKey)
		require.Equal(t, now.AddDate(0, 0, 5+30), res.Peer.ExpiresAt)
	})
}

func TestRefundDuringPayIsNotLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.register(t, RoleUser)
		// the admin refunds while Pay is applying the days
		e.vpn.onChange = func() {
			require.NoError(t, e.svc.MarkRefunded(context.Background(), RefundInput{ChargeID: "c1"}))
		}

		e.pay(t, "c1")
		got := e.payments.m["c1"]
		require.True(t, got.Applied)
		require.False(t, got.RefundedAt.IsZero(), "Pay's own mark does not wipe the refund")
	})
}

func TestPayRecordsTheKeyItExtendsBeforeApplying(t *testing.T) {
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
		stored := e.peers.m[trial.PublicKey]
		stored.ExpiresAt = now.Add(-time.Minute)
		e.peers.m[trial.PublicKey] = stored
		_, err = e.svc.Maintain(ctx)
		require.NoError(t, err)
		payload := e.invoice(
			t,
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
	})
}
