package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// buyMenu shows the tariffs; "buyk:<key>" makes them extend that key.
func (r *Router) buyMenu(ctx context.Context, cq *tgbot.CallbackQuery) error {
	key := ""
	if strings.HasPrefix(cq.Data, cbBuyKey) {
		key = strings.TrimPrefix(cq.Data, cbBuyKey)
	}
	return r.send.Send(
		ctx,
		OutMessage{
			ChatID: cq.ChatID(),
			Text:   buyText,
			Keyboard: tariffsKeyboard(
				tariffsView{
					Tariffs:   r.svc.Tariffs(),
					PublicKey: key,
				},
			),
		},
	)
}

// invoice sends a Stars invoice for "buy:<days>[:<key>]".
func (r *Router) invoice(ctx context.Context, cq *tgbot.CallbackQuery) error {
	daysText, key, _ := strings.Cut(strings.TrimPrefix(cq.Data, cbTariff), ":")
	days, err := strconv.Atoi(daysText)
	if err != nil {
		return fmt.Errorf("bot: bad tariff button %q", cq.Data)
	}
	inv, err := r.svc.Invoice(
		ctx,
		service.PurchaseInput{
			UserID:    cq.SenderID(),
			Days:      days,
			PublicKey: key,
		},
	)
	if err != nil {
		text, known := invoiceErrorText(err)
		return r.replyError(
			ctx,
			userError{
				ChatID: cq.ChatID(),
				Err:    err,
				Text:   text,
				Known:  known,
			},
		)
	}
	return r.send.SendInvoice(
		ctx,
		&OutInvoice{
			ChatID:      cq.ChatID(),
			Title:       invoiceTitle(inv.Days),
			Description: invoiceDescription(inv.Days),
			Payload:     inv.Payload,
			Label:       tariffLabel(inv.Days),
			Stars:       inv.Stars,
		},
	)
}

// preCheckout accepts the payment only if the purchase still holds;
// otherwise Telegram charges nothing. Must answer within 10 seconds.
func (r *Router) preCheckout(ctx context.Context, q *tgbot.PreCheckoutQuery) error {
	err := r.svc.CheckPurchase(
		ctx,
		service.PaymentInput{
			PayerID: q.From.ID,
			Payload: q.InvoicePayload,
			Stars:   int(q.TotalAmount),
		},
	)
	a := PreCheckoutAnswer{
		ID: q.ID,
		OK: err == nil,
	}
	if err != nil {
		log.Printf("bot: pre-checkout from %d declined: %v", q.From.ID, err)
		a.Error = preCheckoutErrorText(err)
	}
	return r.send.AnswerPreCheckout(ctx, a)
}

// paid applies a successful payment. If it can't be applied, the Stars
// go back: a user is never charged for nothing.
func (r *Router) paid(ctx context.Context, m *tgbot.Message) error {
	sp := m.SuccessfulPayment
	res, err := r.svc.Pay(
		ctx,
		service.PaymentInput{
			ChargeID: sp.TelegramPaymentChargeID,
			PayerID:  m.From.ID,
			Payload:  sp.InvoicePayload,
			Stars:    int(sp.TotalAmount),
		},
	)
	if errors.Is(err, service.ErrAlreadyRefunded) {
		log.Printf("bot: payment %s was already refunded, not applied", sp.TelegramPaymentChargeID)
		return nil
	}
	if err != nil {
		return r.refund(
			ctx,
			failedPayment{
				Message: m,
				Cause:   err,
			},
		)
	}
	if res.Repeat {
		return nil
	}
	// The user first: they are waiting for the result of their payment.
	if res.NewKey {
		err = r.deliverKey(
			ctx,
			keyDelivery{
				ChatID: m.Chat.ID,
				Peer:   res.Peer,
			},
		)
	} else {
		err = r.send.Send(
			ctx,
			OutMessage{
				ChatID: m.Chat.ID,
				Text:   extendedText(res.Peer),
			},
		)
	}
	alert := paymentAlert{
		Payer:       m.From,
		Stars:       int(sp.TotalAmount),
		Result:      res,
		DeliveryErr: err,
	}
	if err != nil {
		// Paid, but the key or the message did not get through (a docker
		// timeout, Telegram down): the user must still hear that the
		// payment worked and where the key is.
		ctx = context.WithoutCancel(ctx)
		if sendErr := r.send.Send(
			ctx,
			OutMessage{
				ChatID: m.Chat.ID,
				Text:   paidDeliveryFailedText,
			},
		); sendErr != nil {
			log.Printf("bot: tell %d the payment worked: %v", m.Chat.ID, sendErr)
		}
	}
	r.NotifyAdmins(ctx, paymentAlertText(alert))
	return err
}

// refund returns the Stars of a payment that could not be applied, tells
// the user and the admins, and returns the cause for the log. It runs on
// a context that can't be cancelled: Telegram won't deliver the payment
// again, so a refund skipped at shutdown would be lost.
func (r *Router) refund(ctx context.Context, f failedPayment) error {
	ctx = context.WithoutCancel(ctx)
	m := f.Message
	a := refundAlert{
		ChargeID: m.SuccessfulPayment.TelegramPaymentChargeID,
		UserID:   m.From.ID,
		Cause:    f.Cause,
	}
	text := refundedText
	a.RefundErr = r.send.Refund(
		ctx,
		RefundInput{
			UserID:   a.UserID,
			ChargeID: a.ChargeID,
		},
	)
	if a.RefundErr != nil {
		text = refundFailedText
	} else if err := r.svc.MarkRefunded(ctx, a.ChargeID); err != nil {
		log.Printf("bot: mark refunded %s: %v", a.ChargeID, err)
	}
	r.NotifyAdmins(ctx, refundAlertText(a))
	if sendErr := r.send.Send(
		ctx,
		OutMessage{
			ChatID: m.Chat.ID,
			Text:   text,
		},
	); sendErr != nil {
		log.Printf("bot: %v", sendErr)
	}
	return f.Cause
}

// invoiceErrorText explains expected Invoice errors; known is false for
// unexpected ones.
func invoiceErrorText(err error) (text string, known bool) {
	switch {
	case errors.Is(err, service.ErrNotForSale):
		return notForSaleText, true
	case errors.Is(err, service.ErrNoTariff):
		return noTariffText, true
	case errors.Is(err, service.ErrNotFound):
		return keyNotFoundText, true
	}
	return invoiceFailedText, false
}

func preCheckoutErrorText(err error) string {
	if errors.Is(err, service.ErrNotForSale) {
		return notForSaleText
	}
	return staleInvoiceText
}
