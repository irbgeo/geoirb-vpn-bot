package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"
	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

func TestPlainUserMenuHasBuy(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			role: service.RoleUser,
		},
	)

	require.NoError(t, r.Handle(context.Background(), startUpdate("/start")))
	var data []string
	for _, row := range s.sent[0].Keyboard.InlineKeyboard {
		data = append(data, row[0].CallbackData)
	}
	require.Contains(t, data, "buy")
}

func TestUnlimitedMenuHasNoBuy(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			role: service.RoleUnlimited,
		},
	)

	require.NoError(t, r.Handle(context.Background(), startUpdate("/start")))
	for _, row := range s.sent[0].Keyboard.InlineKeyboard {
		require.NotEqual(t, "buy", row[0].CallbackData)
	}
}

func TestBuyShowsTariffs(t *testing.T) {
	r, s := newRouter(&fakeService{})

	require.NoError(t, r.Handle(context.Background(), press("buy")))
	kb := s.sent[0].Keyboard.InlineKeyboard
	require.Equal(t, "buy:30", kb[0][0].CallbackData)
	require.Contains(t, kb[0][0].Text, "1 месяц")
	require.Contains(t, kb[0][0].Text, "150 ⭐")
	require.Equal(t, "buy:365", kb[1][0].CallbackData)
	require.Contains(t, kb[1][0].Text, "12 месяцев")
}

func TestExtendKeyShowsTariffsForThatKey(t *testing.T) {
	r, s := newRouter(&fakeService{})

	require.NoError(t, r.Handle(context.Background(), press("buyk:PUB1=")))
	require.Equal(t, "buy:30:PUB1=", s.sent[0].Keyboard.InlineKeyboard[0][0].CallbackData)
}

func TestTariffSendsInvoice(t *testing.T) {
	svc := &fakeService{}
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press("buy:30:PUB1=")))

	require.Equal(
		t,
		service.PurchaseInput{
			UserID:    42,
			Days:      30,
			PublicKey: "PUB1=",
		},
		svc.invoiceIn,
	)
	require.Len(t, s.invoices, 1)
	inv := s.invoices[0]
	require.Equal(t, int64(42), inv.ChatID)
	require.Equal(t, "PAYLOAD", inv.Payload)
	require.Equal(t, 150, inv.Stars)
	require.LessOrEqual(t, len([]rune(inv.Title)), 32, "Telegram title limit")
	require.LessOrEqual(t, len([]rune(inv.Description)), 255, "Telegram description limit")
	require.Contains(t, inv.Description, "/terms")
}

func TestTariffForUnlimitedExplains(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			invoiceErr: service.ErrNotForSale,
		},
	)

	require.NoError(t, r.Handle(context.Background(), press("buy:30")))
	require.Empty(t, s.invoices)
	require.Contains(t, s.sent[0].Text, "платить не нужно")
}

func TestBlockedKeyPurchaseExplains(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			invoiceErr: service.ErrBlocked,
		},
	)

	require.NoError(t, r.Handle(context.Background(), press("buy:30")))
	require.Empty(t, s.invoices)
	require.Contains(t, s.sent[0].Text, "отключил администратор")
	require.Contains(t, preCheckoutErrorText(service.ErrBlocked), "отключил администратор")
}

func TestBuyForAnUnreadableKeyExplains(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			invoiceErr: service.ErrUnreadable,
		},
	)

	require.NoError(t, r.Handle(context.Background(), press("buy:30")))
	require.Empty(t, s.invoices)
	require.Equal(t, unreadableKeyBuyText, s.sent[0].Text)
	require.Equal(t, unreadableKeyBuyText, preCheckoutErrorText(service.ErrUnreadable))
}

func TestPreCheckout(t *testing.T) {
	r, s := newRouter(&fakeService{})
	require.NoError(t, r.Handle(context.Background(), preCheckout()))
	require.Equal(
		t,
		[]preCheckoutAnswer{
			{
				ID: "pc1",
				OK: true,
			},
		},
		s.answers,
	)

	r, s = newRouter(
		&fakeService{
			checkErr: service.ErrPriceChanged,
		},
	)
	require.NoError(t, r.Handle(context.Background(), preCheckout()))
	require.False(t, s.answers[0].OK, "Telegram won't charge")
	require.Contains(t, s.answers[0].Error, "устарел")
}

func TestPaymentExtendsKey(t *testing.T) {
	svc := adminService()
	svc.admins = []*service.User{
		{
			ID: 1,
		},
	}
	svc.payRes = &service.PayResult{
		Peer: &service.Peer{
			PublicKey: "PUB1=",
			Name:      "tg:bob",
			ExpiresAt: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
		},
	}
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), paid()))
	require.Empty(t, s.files, "same key, no new config")
	require.Contains(t, s.sent[0].Text, "продлён до 03.11.2026 15:00 по Москве")
	require.Contains(t, s.sent[1].Text, "150 ⭐", "admin alert")
}

func TestPaymentNewKeyDeliversIt(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			payRes: &service.PayResult{
				Peer: &service.Peer{
					PublicKey: "PUB1=",
					Name:      "tg:bob",
				},
				NewKey: true,
			},
		},
	)

	require.NoError(t, r.Handle(context.Background(), paid()))
	require.Len(t, s.files, 2, "config and QR; the lists are the next step")
}

func TestPaymentRepeatDoesNothing(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			payRes: &service.PayResult{
				Repeat: true,
			},
		},
	)

	require.NoError(t, r.Handle(context.Background(), paid()))
	require.Empty(t, s.sent)
	require.Empty(t, s.files)
}

func TestPaymentNeedsReviewTellsUserAndAdmins(t *testing.T) {
	svc := adminService()
	svc.admins = []*service.User{
		{
			ID: 1,
		},
	}
	svc.payRes = &service.PayResult{
		NeedsReview: true,
	}
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), paid()))
	require.Len(t, s.sent, 2, "a note for the user, one alert for the admin")
	require.Equal(t, int64(42), s.sent[0].ChatID)
	require.Equal(t, "✅ Оплата получена. Администратор проверит её и продлит ключ в ближайшее время.", s.sent[0].Text)
	require.Equal(t, int64(1), s.sent[1].ChatID)
	require.Contains(t, s.sent[1].Text, "charge1")
	require.Contains(t, s.sent[1].Text, "150 ⭐")
	require.Contains(t, s.sent[1].Text, "проверьте срок ключа и примените вручную или верните звёзды")
	require.Empty(t, s.files)
	require.Empty(t, s.refunds)
}

func TestPaymentFailureRefunds(t *testing.T) {
	svc := &fakeService{
		payErr: errors.New("awg down"),
	}
	r, s := newRouter(svc)

	err := r.Handle(context.Background(), paid())
	require.ErrorContains(t, err, "awg down")
	require.Equal(
		t,
		[]refundInput{
			{
				UserID:   42,
				ChargeID: "charge1",
				Stars:    150,
			},
		},
		s.refunds,
	)
	require.Equal(t, []string{"charge1"}, svc.refunded)
	require.Equal(
		t,
		[]service.RefundInput{
			{
				ChargeID: "charge1",
				UserID:   42,
				Stars:    150,
			},
		},
		svc.refundedAs,
		"with the payer and the amount: the record may have to be created",
	)
	require.Contains(t, s.sent[0].Text, "звёзды возвращены")
}

func TestPaymentRefundFailureAsksToContact(t *testing.T) {
	svc := &fakeService{
		payErr: errors.New("awg down"),
	}
	r, s := newRouter(svc)
	s.refundErr = errors.New("telegram down")

	require.Error(t, r.Handle(context.Background(), paid()))
	require.Empty(t, svc.refunded)
	require.Contains(t, s.sent[0].Text, "/paysupport")
}

func TestTermsSupportPaySupport(t *testing.T) {
	r, s := newRouter(&fakeService{})
	ctx := context.Background()

	for _, cmd := range []string{
		"/terms",
		"/support",
		"/paysupport",
	} {
		require.NoError(t, r.Handle(ctx, startUpdate(cmd)))
	}
	terms, support, paySupport := s.sent[0].Text, s.sent[1].Text, s.sent[2].Text
	for _, want := range []string{
		"Stars",
		"пробный",
		"незакон",
		"храним",
		"@help_me",
	} {
		require.Contains(t, terms, want)
	}
	require.Contains(t, support, "@help_me")
	require.Contains(t, paySupport, "@help_me")
	require.Contains(t, paySupport, "Поддержка Telegram не может помочь", "Telegram requires this")
}

func TestTermsAndSupportButtons(t *testing.T) {
	r, s := newRouter(&fakeService{})
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("terms")))
	require.NoError(t, r.Handle(ctx, press("support")))
	require.Contains(t, s.sent[0].Text, "Условия")
	require.Contains(t, s.sent[1].Text, "@help_me")
}

func TestUnknownCommandGetsHint(t *testing.T) {
	r, s := newRouter(&fakeService{})

	require.NoError(t, r.Handle(context.Background(), startUpdate("/foo")))
	require.Contains(t, s.sent[0].Text, "/menu")
}

func TestCommandsForTelegramMenu(t *testing.T) {
	var names []string
	for _, c := range Commands() {
		names = append(names, c.Command)
		require.NotEmpty(t, c.Description)
	}
	require.Equal(
		t,
		[]string{
			"menu",
			"support",
			"terms",
			"paysupport",
		},
		names,
	)
}

func TestPaymentRefundWorksWhileShuttingDown(t *testing.T) {
	svc := &fakeService{
		payErr: errors.New("context canceled"),
	}
	r, s := newRouter(svc)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // SIGTERM arrived while the payment was applied

	require.Error(t, r.Handle(ctx, paid()))
	require.Len(t, s.refunds, 1, "the Stars still go back")
	require.Equal(t, []string{"charge1"}, svc.refunded)
}

func TestPaymentAlreadyRefundedIsNotRefundedAgain(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			payErr: service.ErrAlreadyRefunded,
		},
	)

	require.NoError(t, r.Handle(context.Background(), paid()))
	require.Empty(t, s.refunds)
	require.Empty(t, s.sent)
}

func TestReconcileReportsUnfinishedPayments(t *testing.T) {
	svc := &fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
		report: &service.ReconcileReport{},
		unfinished: []*service.Payment{
			{
				ChargeID:  "stuck",
				UserID:    42,
				Stars:     150,
				Days:      30,
				CreatedAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
			},
		},
	}
	r, s := newRouter(svc)

	r.Reconcile(context.Background())
	require.Len(t, s.sent, 1)
	require.Contains(t, s.sent[0].Text, "не отмечены как применённые")
	require.Contains(t, s.sent[0].Text, "id 42")
	require.Contains(t, s.sent[0].Text, "150 ⭐")
}

func TestBuyButtonHasNoKey(t *testing.T) {
	r, s := newRouter(&fakeService{})

	require.NoError(t, r.Handle(context.Background(), press("buy")))
	require.Equal(t, "buy:30", s.sent[0].Keyboard.InlineKeyboard[0][0].CallbackData)
}

func preCheckout() tgbot.Update {
	return tgbot.Update{
		PreCheckoutQuery: &tgbot.PreCheckoutQuery{
			ID:             "pc1",
			From:           tgbot.User{ID: 42},
			Currency:       "XTR",
			TotalAmount:    150,
			InvoicePayload: "PAYLOAD",
		},
	}
}

func paid() tgbot.Update {
	return tgbot.Update{
		Message: &tgbot.Message{
			Chat: tgbot.Chat{
				ID:   42,
				Type: "private",
			},
			From: &tgbot.User{
				ID:       42,
				Username: "bob",
			},
			SuccessfulPayment: &tgbot.SuccessfulPayment{
				Currency:                "XTR",
				TotalAmount:             150,
				InvoicePayload:          "PAYLOAD",
				TelegramPaymentChargeID: "charge1",
			},
		},
	}
}

// The automatic refund and the admin button are one sequence: when the
// Stars went back but the record failed, the admins hear that too.
func TestPaymentRefundNotRecordedIsToldToAdmins(t *testing.T) {
	svc := &fakeService{
		payErr:        errors.New("awg down"),
		refundMarkErr: errors.New("mongo down"),
		admins: []*service.User{
			{
				ID: 1,
			},
		},
	}
	r, s := newRouter(svc)

	require.Error(t, r.Handle(context.Background(), paid()))

	require.Len(t, s.refunds, 1)
	alert := s.sentTo(1)[0].Text
	require.Contains(t, alert, "звёзды возвращены")
	require.Contains(t, alert, "не записан")
	require.Contains(t, alert, "mongo down")
	require.Contains(t, s.sentTo(42)[0].Text, "звёзды возвращены", "for the user it is a plain refund")
}

func TestInvoiceErrorsExplained(t *testing.T) {
	cases := map[error]string{
		service.ErrNoTariff:   noTariffText,
		service.ErrNotFound:   keyNotFoundText,
		service.ErrNotForSale: notForSaleText,
	}
	for cause, want := range cases {
		r, s := newRouter(
			&fakeService{
				invoiceErr: cause,
			},
		)

		require.NoError(t, r.Handle(context.Background(), press("buy:30")), "expected: not logged")
		require.Equal(t, want, s.sent[0].Text)
		require.Empty(t, s.invoices)
	}
}

func TestPreCheckoutDeclineSaysWhy(t *testing.T) {
	cases := map[error]string{
		service.ErrNotForSale: notForSaleText,
		service.ErrBlocked:    blockedKeyText,
		service.ErrUnreadable: unreadableKeyBuyText,
		service.ErrNotFound:   staleInvoiceText,
	}
	for cause, want := range cases {
		r, s := newRouter(
			&fakeService{
				checkErr: cause,
			},
		)

		require.NoError(t, r.Handle(context.Background(), preCheckout()))
		require.False(t, s.answers[0].OK)
		require.Equal(t, want, s.answers[0].Error)
	}
}

func TestPaidButDeliveryFailedStillTellsTheUser(t *testing.T) {
	svc := adminService()
	svc.admins = []*service.User{
		{
			ID: 1,
		},
	}
	svc.payRes = &service.PayResult{
		Peer: &service.Peer{
			PublicKey: "PUB1=",
			Name:      "tg:bob",
		},
		NewKey: true,
		Days:   30,
	}
	svc.configErr = errors.New("awg timeout")
	r, s := newRouter(svc)

	require.Error(t, r.Handle(context.Background(), paid()))
	require.Equal(t, int64(42), s.sent[0].ChatID)
	require.Contains(t, s.sent[0].Text, "Оплата получена")
	require.Contains(t, s.sent[0].Text, "Мой доступ")
	require.Contains(t, s.sent[1].Text, "awg timeout", "admins see why")
}

func TestInvoiceFailureSaysInvoice(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			invoiceErr: errors.New("db down"),
		},
	)

	require.Error(t, r.Handle(context.Background(), press("buy:30")))
	require.Contains(t, s.sent[0].Text, "счёт")
}
