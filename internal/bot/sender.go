package bot

import (
	"bytes"
	"context"
	"errors"

	tgbot "github.com/irbgeo/go-tgbot"
)

// Sender talks to Telegram. Faked in tests.
type Sender interface {
	Send(ctx context.Context, m OutMessage) error
	Edit(ctx context.Context, m EditMessage) error
	SendDocument(ctx context.Context, f OutFile) error
	SendPhoto(ctx context.Context, f OutFile) error
	Answer(ctx context.Context, callbackID string) error
	SendInvoice(ctx context.Context, inv *OutInvoice) error
	AnswerPreCheckout(ctx context.Context, a PreCheckoutAnswer) error
	Refund(ctx context.Context, in RefundInput) error
}

// TelegramSender adapts *tgbot.Client to Sender.
type TelegramSender struct {
	client *tgbot.Client
}

var _ Sender = (*TelegramSender)(nil)

// NewTelegramSender creates a TelegramSender.
func NewTelegramSender(
	client *tgbot.Client,
) *TelegramSender {
	return &TelegramSender{
		client: client,
	}
}

// Send sends a text message, with inline buttons if any.
func (s *TelegramSender) Send(ctx context.Context, m OutMessage) error {
	var opts *tgbot.SendMessageOptions
	if m.Keyboard != nil {
		opts = &tgbot.SendMessageOptions{
			ReplyMarkup: m.Keyboard,
		}
	}
	_, err := s.client.SendMessage(ctx, m.ChatID, m.Text, opts)
	return err
}

// Edit replaces a message's text and buttons in place. "Message is not
// modified" (same content twice) is not an error.
func (s *TelegramSender) Edit(ctx context.Context, m EditMessage) error {
	_, err := s.client.EditMessageText(
		ctx,
		m.ChatID,
		m.MessageID,
		m.Text,
		&tgbot.EditMessageTextOptions{
			ReplyMarkup: m.Keyboard,
		},
	)
	var apiErr *tgbot.APIError
	if errors.As(err, &apiErr) && apiErr.IsNotModified() {
		return nil
	}
	return err
}

// SendDocument uploads a file.
func (s *TelegramSender) SendDocument(ctx context.Context, f OutFile) error {
	_, err := s.client.SendDocument(
		ctx,
		f.ChatID,
		inputFile(f),
		&tgbot.SendDocumentOptions{
			Caption: f.Caption,
		},
	)
	return err
}

// SendPhoto uploads an image.
func (s *TelegramSender) SendPhoto(ctx context.Context, f OutFile) error {
	_, err := s.client.SendPhoto(
		ctx,
		f.ChatID,
		inputFile(f),
		&tgbot.SendPhotoOptions{
			Caption: f.Caption,
		},
	)
	return err
}

// Answer stops the loading spinner on a pressed inline button.
func (s *TelegramSender) Answer(ctx context.Context, callbackID string) error {
	_, err := s.client.AnswerCallback(ctx, callbackID)
	return err
}

// SendInvoice sends a Telegram Stars invoice: currency XTR, empty
// provider token.
func (s *TelegramSender) SendInvoice(ctx context.Context, inv *OutInvoice) error {
	_, err := s.client.SendInvoice(
		ctx,
		inv.ChatID,
		inv.Title,
		inv.Description,
		inv.Payload,
		"XTR",
		"",
		[]tgbot.LabeledPrice{
			{
				Label:  inv.Label,
				Amount: inv.Stars,
			},
		},
		nil,
	)
	return err
}

// AnswerPreCheckout accepts or declines a payment before Telegram charges.
func (s *TelegramSender) AnswerPreCheckout(ctx context.Context, a PreCheckoutAnswer) error {
	_, err := s.client.AnswerPreCheckoutQuery(ctx, a.ID, a.OK, a.Error)
	return err
}

// Refund returns the Stars of a payment to the user. A charge that is
// already refunded counts as done.
func (s *TelegramSender) Refund(ctx context.Context, in RefundInput) error {
	_, err := s.client.RefundStarPayment(ctx, in.UserID, in.ChargeID)
	if tgbot.IsChargeAlreadyRefunded(err) {
		return nil
	}
	return err
}

func inputFile(f OutFile) tgbot.InputFile {
	return tgbot.InputFile{
		Reader:   bytes.NewReader(f.Data),
		Filename: f.Name,
	}
}
