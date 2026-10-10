package bot

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"strings"
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
	// SendVideo returns Telegram's file ID of the sent video, to send it
	// again without an upload.
	SendVideo(ctx context.Context, v *outVideo) (string, error)
	Answer(ctx context.Context, callbackID string) error
	SendInvoice(ctx context.Context, inv *outInvoice) error
	AnswerPreCheckout(ctx context.Context, a preCheckoutAnswer) error
	Refund(ctx context.Context, in refundInput) error
}

// uploadTimeout is the whole-request limit of the upload client: the video
// is several megabytes and does not fit the usual 15 seconds on a slow link.
const uploadTimeout = 5 * time.Minute

// telegramSender adapts *tgbot.Client to Sender. uploads is a second
// client with a long timeout, used only to upload the video; everything
// else keeps the short one, so a hung call fails fast.
type telegramSender struct {
	client  *tgbot.Client
	uploads *tgbot.Client
}

var _ Sender = (*telegramSender)(nil)

// NewTelegramClient connects to the Bot API.
func NewTelegramClient(
	cfg *config.Config,
) (*tgbot.Client, error) {
	if cfg.TelegramTestEnv {
		log.Println("telegram: TEST environment")
	}
	return tgbot.NewClient(cfg.BotToken, clientOptions(cfg)...)
}

// NewUploadClient is NewTelegramClient with uploadTimeout instead of
// go-tgbot's 15 seconds per request (see telegramSender).
func NewUploadClient(
	cfg *config.Config,
) (*tgbot.Client, error) {
	httpClient := http.Client{
		Timeout: uploadTimeout,
	}
	opts := append(clientOptions(cfg), tgbot.WithHTTPClient(&httpClient))
	return tgbot.NewClient(cfg.BotToken, opts...)
}

// NewTelegramSender creates a telegramSender.
func NewTelegramSender(
	client *tgbot.Client,
	uploads *tgbot.Client,
) *telegramSender {
	return &telegramSender{
		client:  client,
		uploads: uploads,
	}
}

// Send sends a text message, with inline buttons if any. This is the one
// place that knows Telegram's message size: a longer text goes out as
// several messages, cut at line breaks, with the buttons under the last.
func (s *telegramSender) Send(ctx context.Context, m outMessage) error {
	parts := tgbot.SplitText(m.Text)
	for i, part := range parts {
		var opts *tgbot.SendMessageOptions
		if m.Keyboard != nil && i == len(parts)-1 {
			opts = &tgbot.SendMessageOptions{
				ReplyMarkup: m.Keyboard,
			}
		}
		_, err := s.client.SendMessage(ctx, m.ChatID, part, opts)
		if err != nil {
			return err
		}
	}
	return nil
}

// Edit replaces a message's text and buttons in place. "Message is not
// modified" (same content twice) is not an error. Of a text too long for
// one message, the message gets the first part and the rest follows as new
// messages, the buttons under the last one.
func (s *telegramSender) Edit(ctx context.Context, m editMessage) error {
	parts := tgbot.SplitText(m.Text)
	editMessageTextOptions := tgbot.EditMessageTextOptions{
		ReplyMarkup: m.Keyboard,
	}
	if len(parts) > 1 {
		// An empty keyboard takes the old buttons off the first part.
		editMessageTextOptions.ReplyMarkup = &tgbot.InlineKeyboardMarkup{
			InlineKeyboard: [][]tgbot.InlineKeyboardButton{},
		}
	}
	_, err := s.client.EditMessageText(ctx, m.ChatID, m.MessageID, parts[0], &editMessageTextOptions)
	if tgbot.IsNotModified(err) {
		err = nil
	}
	if err != nil || len(parts) == 1 {
		return err
	}
	rest := outMessage{
		ChatID:   m.ChatID,
		Text:     strings.Join(parts[1:], ""),
		Keyboard: m.Keyboard,
	}
	return s.Send(ctx, rest)
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

// SendVideo sends a video: by its file ID, or as an upload of v.Data (on
// the upload client). The returned ID is the video's, or the document's
// when Telegram filed a video without sound as an animation.
func (s *telegramSender) SendVideo(ctx context.Context, v *outVideo) (string, error) {
	client := s.client
	inputFile := tgbot.InputFile{
		FileID: v.FileID,
	}
	if v.FileID == "" {
		client = s.uploads
		inputFile.Reader = bytes.NewReader(v.Data)
		inputFile.Filename = v.Name
	}
	sendVideoOptions := tgbot.SendVideoOptions{
		Caption: v.Caption,
	}
	m, err := client.SendVideo(ctx, v.ChatID, inputFile, &sendVideoOptions)
	switch {
	case err != nil:
		return "", err
	case m.Video != nil:
		return m.Video.FileID, nil
	case m.Document != nil:
		return m.Document.FileID, nil
	}
	return "", nil
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

// clientOptions are the options of both Bot API clients. RetryAfter: a 429
// "too many requests" waits (≤10 s) and retries once instead of losing the
// message.
func clientOptions(cfg *config.Config) []tgbot.Option {
	opts := []tgbot.Option{
		tgbot.WithRetryAfter(10 * time.Second),
	}
	if cfg.TelegramTestEnv {
		opts = append(opts, tgbot.WithTestEnvironment())
	}
	return opts
}

func inputFile(f outFile) tgbot.InputFile {
	return tgbot.InputFile{
		Reader:   bytes.NewReader(f.Data),
		Filename: f.Name,
	}
}
