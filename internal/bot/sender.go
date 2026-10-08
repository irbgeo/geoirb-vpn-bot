package bot

import (
	"bytes"
	"context"
	"errors"
	"log"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
)

// Sender talks to Telegram. Faked in tests.
type Sender interface {
	Send(ctx context.Context, m outMessage) error
	Edit(ctx context.Context, m editMessage) error
	SendDocument(ctx context.Context, f outFile) error
	SendPhoto(ctx context.Context, f outFile) error
	Answer(ctx context.Context, callbackID string) error
	SendInvoice(ctx context.Context, inv *outInvoice) error
	AnswerPreCheckout(ctx context.Context, a preCheckoutAnswer) error
	Refund(ctx context.Context, in refundInput) error
}

// telegramSender adapts *tgbot.Client to Sender.
type telegramSender struct {
	client *tgbot.Client
}

var _ Sender = (*telegramSender)(nil)

// NewTelegramClient connects to the Bot API. RetryAfter: a 429 "too many
// requests" waits (≤10 s) and retries once instead of losing the message.
func NewTelegramClient(
	cfg *config.Config,
) (*tgbot.Client, error) {
	opts := []tgbot.Option{
		tgbot.WithRetryAfter(10 * time.Second),
	}
	if cfg.TelegramTestEnv {
		opts = append(opts, tgbot.WithTestEnvironment())
		log.Println("telegram: TEST environment")
	}
	return tgbot.NewClient(cfg.BotToken, opts...)
}

// NewTelegramSender creates a telegramSender.
func NewTelegramSender(
	client *tgbot.Client,
) *telegramSender {
	return &telegramSender{
		client: client,
	}
}

// Send sends a text message, with inline buttons if any.
func (s *telegramSender) Send(ctx context.Context, m outMessage) error {
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
func (s *telegramSender) Edit(ctx context.Context, m editMessage) error {
	editMessageTextOptions := tgbot.EditMessageTextOptions{
		ReplyMarkup: m.Keyboard,
	}
	_, err := s.client.EditMessageText(ctx, m.ChatID, m.MessageID, m.Text, &editMessageTextOptions)
	var apiErr *tgbot.APIError
	if errors.As(err, &apiErr) && apiErr.IsNotModified() {
		return nil
	}
	return err
}

// SendDocument uploads a file.
func (s *telegramSender) SendDocument(ctx context.Context, f outFile) error {
	sendDocumentOptions := tgbot.SendDocumentOptions{
		Caption: f.Caption,
	}
	_, err := s.client.SendDocument(ctx, f.ChatID, inputFile(f), &sendDocumentOptions)
	return err
}

// SendPhoto uploads an image.
func (s *telegramSender) SendPhoto(ctx context.Context, f outFile) error {
	sendPhotoOptions := tgbot.SendPhotoOptions{
		Caption: f.Caption,
	}
	_, err := s.client.SendPhoto(ctx, f.ChatID, inputFile(f), &sendPhotoOptions)
	return err
}

// Answer stops the loading spinner on a pressed inline button.
func (s *telegramSender) Answer(ctx context.Context, callbackID string) error {
	_, err := s.client.AnswerCallback(ctx, callbackID)
	return err
}

// SendInvoice sends a Telegram Stars invoice: currency XTR, empty
// provider token.
func (s *telegramSender) SendInvoice(ctx context.Context, inv *outInvoice) error {
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
func (s *telegramSender) AnswerPreCheckout(ctx context.Context, a preCheckoutAnswer) error {
	_, err := s.client.AnswerPreCheckoutQuery(ctx, a.ID, a.OK, a.Error)
	return err
}

// Refund returns the Stars of a payment to the user. A charge that is
// already refunded counts as done.
func (s *telegramSender) Refund(ctx context.Context, in refundInput) error {
	_, err := s.client.RefundStarPayment(ctx, in.UserID, in.ChargeID)
	if tgbot.IsChargeAlreadyRefunded(err) {
		return nil
	}
	return err
}

func inputFile(f outFile) tgbot.InputFile {
	return tgbot.InputFile{
		Reader:   bytes.NewReader(f.Data),
		Filename: f.Name,
	}
}
