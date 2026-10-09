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
func (s *router) buyMenu(ctx context.Context, cq *tgbot.CallbackQuery) error {
	key := ""
	if strings.HasPrefix(cq.Data, cbBuyKey) {
		key = strings.TrimPrefix(cq.Data, cbBuyKey)
	}
	tariffsView := tariffsView{
		Tariffs:   s.billing.Tariffs(),
		PublicKey: key,
	}
	outMessage := outMessage{
		ChatID:   cq.ChatID(),
		Text:     buyText,
		Keyboard: tariffsKeyboard(tariffsView),
	}
	return s.send.Send(ctx, outMessage)
}

// invoice sends a Stars invoice for "buy:<days>[:<key>]".
func (s *router) invoice(ctx context.Context, cq *tgbot.CallbackQuery) error {
	daysText, key, _ := strings.Cut(strings.TrimPrefix(cq.Data, cbTariff), ":")
	days, err := strconv.Atoi(daysText)
	if err != nil {
		return fmt.Errorf("bot: bad tariff button %q", cq.Data)
	}
	purchaseInput := service.PurchaseInput{
		UserID:    cq.SenderID(),
		Days:      days,
		PublicKey: key,
	}
	inv, err := s.billing.Invoice(ctx, purchaseInput)
	if err != nil {
		text, known := invoiceErrorText(err)
		userError := userError{
			ChatID: cq.ChatID(),
			Err:    err,
			Text:   text,
			Known:  known,
		}
		return s.replyError(ctx, userError)
	}
	outInvoice := outInvoice{
		ChatID:      cq.ChatID(),
		Title:       invoiceTitle(inv.Days),
		Description: invoiceDescription(inv.Days),
		Payload:     inv.Payload,
		Label:       tariffLabel(inv.Days),
		Stars:       inv.Stars,
	}
	return s.send.SendInvoice(ctx, &outInvoice)
}

// preCheckout accepts the payment only if the purchase still holds;
// otherwise Telegram charges nothing. Must answer within 10 seconds.
func (s *router) preCheckout(ctx context.Context, q *tgbot.PreCheckoutQuery) error {
	paymentInput := service.PaymentInput{
		PayerID: q.From.ID,
		Payload: q.InvoicePayload,
		Stars:   int(q.TotalAmount),
	}
	err := s.billing.CheckPurchase(ctx, paymentInput)
	a := preCheckoutAnswer{
		ID: q.ID,
		OK: err == nil,
	}
	if err != nil {
		log.Printf("bot: pre-checkout from %d declined: %v", q.From.ID, err)
		a.Error = preCheckoutErrorText(err)
	}
	return s.send.AnswerPreCheckout(ctx, a)
}

// paid applies a successful payment. If it can't be applied, the Stars
// go back: a user is never charged for nothing.
func (s *router) paid(ctx context.Context, m *tgbot.Message) error {
	sp := m.SuccessfulPayment
	paymentInput := service.PaymentInput{
		ChargeID: sp.TelegramPaymentChargeID,
		PayerID:  m.From.ID,
		Payload:  sp.InvoicePayload,
		Stars:    int(sp.TotalAmount),
	}
	res, err := s.billing.Pay(ctx, paymentInput)
	if errors.Is(err, service.ErrAlreadyRefunded) {
		log.Printf("bot: payment %s was already refunded, not applied", sp.TelegramPaymentChargeID)
		return nil
	}
	if err != nil {
		failedPayment := failedPayment{
			Message: m,
			Cause:   err,
		}
		return s.refund(ctx, failedPayment)
	}
	if res.Repeat {
		return nil
	}
	if res.NeedsReview {
		reviewInput := paymentReviewInput{
			ChargeID: sp.TelegramPaymentChargeID,
			UserID:   m.From.ID,
			Stars:    int(sp.TotalAmount),
		}
		s.notify.NotifyAdmins(ctx, paymentReviewText(reviewInput))
		return nil
	}
	// The user first: they are waiting for the result of their payment.
	if res.NewKey {
		keyDelivery := keyDelivery{
			ChatID: m.Chat.ID,
			Peer:   res.Peer,
		}
		err = s.deliverKey(ctx, keyDelivery)
	} else {
		outMessage := outMessage{
			ChatID: m.Chat.ID,
			Text:   extendedText(res.Peer),
		}
		err = s.send.Send(ctx, outMessage)
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
		outMessage := outMessage{
			ChatID: m.Chat.ID,
			Text:   paidDeliveryFailedText,
		}
		sendErr := s.send.Send(ctx, outMessage)
		if sendErr != nil {
			log.Printf("bot: tell %d the payment worked: %v", m.Chat.ID, sendErr)
		}
	}
	s.notify.NotifyAdmins(ctx, paymentAlertText(alert))
	return err
}

// refund returns the Stars of a payment that could not be applied, tells
// the user and the admins, and returns the cause for the log. It runs on
// a context that can't be cancelled: Telegram won't deliver the payment
// again, so a refund skipped at shutdown would be lost.
func (s *router) refund(ctx context.Context, f failedPayment) error {
	ctx = context.WithoutCancel(ctx)
	m := f.Message
	a := refundAlert{
		ChargeID: m.SuccessfulPayment.TelegramPaymentChargeID,
		UserID:   m.From.ID,
		Cause:    f.Cause,
	}
	text := refundedText
	refundInput := refundInput{
		UserID:   a.UserID,
		ChargeID: a.ChargeID,
	}
	a.RefundErr = s.send.Refund(ctx, refundInput)
	if a.RefundErr != nil {
		text = refundFailedText
	} else {
		err := s.billing.MarkRefunded(ctx, a.ChargeID)
		if err != nil {
			log.Printf("bot: mark refunded %s: %v", a.ChargeID, err)
		}
	}
	s.notify.NotifyAdmins(ctx, refundAlertText(a))
	outMessage := outMessage{
		ChatID: m.Chat.ID,
		Text:   text,
	}
	sendErr := s.send.Send(ctx, outMessage)
	if sendErr != nil {
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
	case errors.Is(err, service.ErrBlocked):
		return blockedKeyText, true
	case errors.Is(err, service.ErrNoTariff):
		return noTariffText, true
	case errors.Is(err, service.ErrNotFound):
		return keyNotFoundText, true
	}
	return invoiceFailedText, false
}

func preCheckoutErrorText(err error) string {
	switch {
	case errors.Is(err, service.ErrNotForSale):
		return notForSaleText
	case errors.Is(err, service.ErrBlocked):
		return blockedKeyText
	}
	return staleInvoiceText
}
