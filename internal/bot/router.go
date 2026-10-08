package bot

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/irbgeo/geoirb-vpn-bot/internal/bypass"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
	"github.com/irbgeo/geoirb-vpn-bot/internal/sysload"
)

// The bot's needs from the business logic, split by topic so each handler
// depends only on what it uses. *service.Service implements them all.

// Users is who uses the bot.
type Users interface {
	Register(ctx context.Context, in service.RegisterInput) (*service.User, error)
	User(ctx context.Context, id int64) (*service.User, error)
	Users(ctx context.Context, p service.Page) ([]*service.User, int64, error)
	Admins(ctx context.Context) ([]*service.User, error)
}

// Keys are VPN keys: a user's own, and the admin panel's.
type Keys interface {
	CreateKey(ctx context.Context, in service.CreateKeyInput) (*service.Peer, error)
	CheckCreateKey(ctx context.Context, userID int64) error
	Access(ctx context.Context, userID int64) ([]service.KeyInfo, error)
	UserConfig(ctx context.Context, k service.UserKey) (*service.KeyConfig, error)
	ReissueKey(ctx context.Context, k service.UserKey) (*service.Peer, error)
	DeleteOwnKey(ctx context.Context, k service.UserKey) error

	// admin panel
	Key(ctx context.Context, publicKey string) (*service.Peer, error)
	ClientConfig(ctx context.Context, publicKey string) (string, error)
	Disable(ctx context.Context, publicKey string) error
	Enable(ctx context.Context, publicKey string) error
	Delete(ctx context.Context, publicKey string) error
	Extend(ctx context.Context, in service.ExtendInput) (*service.Peer, error)
	Issue(ctx context.Context, in service.IssueInput) (*service.Peer, error)
}

// Billing is buying access with Stars and refunds.
type Billing interface {
	Tariffs() []service.Tariff
	Invoice(ctx context.Context, in service.PurchaseInput) (*service.Invoice, error)
	CheckPurchase(ctx context.Context, in service.PaymentInput) error
	Pay(ctx context.Context, in service.PaymentInput) (*service.PayResult, error)
	MarkRefunded(ctx context.Context, chargeID string) error
	Payments(ctx context.Context, userID int64) ([]*service.Payment, error)
	UnfinishedPayments(ctx context.Context) ([]*service.Payment, error)
}

// Ops is the admins' view of the server as a whole.
type Ops interface {
	Reconcile(ctx context.Context) (*service.ReconcileReport, error)
	Stats(ctx context.Context) (*service.Stats, error)
	BroadcastRecipients(ctx context.Context) ([]int64, error)
}

// Feedback is users' reviews and suggestions.
type Feedback interface {
	AddFeedback(ctx context.Context, in service.FeedbackInput) error
	Feedbacks(ctx context.Context, p service.Page) ([]*service.Feedback, int64, error)
}

// ServerLoad says which server limits were just passed or are back to
// normal (sysload.Monitor).
type ServerLoad interface {
	Check() ([]sysload.Alert, error)
}

// Bypass provides the split-tunneling lists sent with every key.
type Bypass interface {
	Files(ctx context.Context) ([]bypass.File, error)
}

// Callback data of inline buttons (Telegram allows up to 64 bytes).
const (
	cbMenu       = "menu"       // back to the main menu, in the same message
	cbFeedback   = "feedback"   // reviews and suggestions: asks for the text
	cbCreateKey  = "key:create" // step 1: which app to install
	cbIssueKey   = "key:issue"  // step 2: ask for the key's name
	cbKeyNoName  = "key:noname" // skip the name: the key and how to add it
	cbMyAccess   = "my"
	cbBypass     = "bypass"
	cbSupport    = "support"
	cbTerms      = "terms"
	cbBuy        = "buy"   // tariffs for "my key"
	cbBuyKey     = "buyk:" // + public key: tariffs for that key
	cbTariff     = "buy:"  // + days [+ ":" + public key]: send the invoice
	cbConfig     = "cfg:"  // + public key (44 chars)
	cbReissueAsk = "kr?:"  // + public key: confirm reissuing the key
	cbReissue    = "kr:"   // + public key: reissue it
	cbDeleteAsk  = "kd?:"  // + public key: confirm deleting the key
	cbDelete     = "kd:"   // + public key: delete it
)

// Router turns Telegram updates into service calls and replies. Its own
// state is kept in small types with their own locks (state.go).
type Router struct {
	users    Users
	keys     Keys
	billing  Billing
	ops      Ops
	feedback Feedback
	send     Sender
	notify   *Notifier
	bypass   Bypass
	support  string // support contact

	// dialogs: what each chat's next input is (a broadcast text, a key
	// name). In memory only: after a restart the button is pressed again.
	dialogs *dialogs
	// jobs: the one background mass send; Close waits for it.
	jobs  *jobs
	maint *maintFlag
	// refunds: charge IDs an admin refund is running for.
	refunds *inFlight
	// pause between broadcast messages (Telegram allows ~30 per second).
	pause time.Duration
}

// New creates a Router.
func New(
	d *Deps,
) *Router {
	return &Router{
		users:    d.Users,
		keys:     d.Keys,
		billing:  d.Billing,
		ops:      d.Ops,
		feedback: d.Feedback,
		send:     d.Sender,
		bypass:   d.Bypass,
		support:  d.SupportContact,
		notify:   d.Notifier,
		dialogs:  newDialogs(),
		jobs:     newJobs(),
		maint:    newMaintFlag(d.MaintenanceFlag),
		refunds:  newInFlight(),
		pause:    50 * time.Millisecond,
	}
}

// Handle processes one update. Updates it doesn't know are ignored.
func (s *Router) Handle(ctx context.Context, upd tgbot.Update) error {
	if upd.PreCheckoutQuery != nil {
		return s.preCheckout(ctx, upd.PreCheckoutQuery)
	}
	if upd.Message != nil && upd.Message.SuccessfulPayment != nil {
		return s.paid(ctx, upd.Message)
	}
	if upd.CallbackQuery != nil {
		if !private(upd.CallbackQuery.Message) {
			return nil
		}
		return s.callback(ctx, upd.CallbackQuery)
	}
	if upd.Message == nil || upd.Message.From == nil || !private(upd.Message) {
		return nil
	}
	if cmd, ok := upd.Command(); ok {
		// /start: Telegram sends it on the first "Start"; /menu is the name
		// users see in the command list and texts.
		if cmd == "start" || cmd == "menu" {
			return s.start(ctx, upd.Message)
		}
		return s.send.Send(
			ctx,
			OutMessage{
				ChatID: upd.ChatID(),
				Text: s.commandText(
					command{
						Name:   cmd,
						UserID: upd.Message.From.ID,
					},
				),
			},
		)
	}
	if p, ok := s.dialogs.peek(upd.Message.Chat.ID); ok {
		switch p.Kind {
		case pendingKeyName:
			return s.keyNamed(ctx, upd.Message)
		case pendingFeedback:
			return s.feedbackText(ctx, upd.Message)
		}
	}
	return s.adminText(ctx, upd.Message)
}

// Close stops background work (a running broadcast stops and sends its
// report) and waits for it. Call it on shutdown, after the updates stopped.
func (s *Router) Close() {
	s.jobs.close()
}

// Wait blocks until background work is done.
func (s *Router) Wait() {
	s.jobs.wait()
}

// Reconcile runs at startup: payments left half-done by a stop, then the
// DB against the server. Problems are logged and sent to admins; nothing
// is changed.
func (s *Router) Reconcile(ctx context.Context) {
	if ps, err := s.billing.UnfinishedPayments(ctx); err != nil {
		log.Printf("reconcile: unfinished payments: %v", err)
	} else if len(ps) > 0 {
		s.notify.NotifyAdmins(ctx, unfinishedPaymentsText(ps))
	}

	rep, err := s.ops.Reconcile(ctx)
	if err != nil {
		log.Printf("reconcile: %v", err)
		s.notify.NotifyAdmins(ctx, reconcileFailedText(err))
		return
	}
	log.Printf(
		"reconcile: missing on server %d, disabled but on server %d, manual %d",
		len(rep.MissingOnServer),
		len(rep.DisabledButOnServer),
		rep.Manual,
	)
	if !rep.OK() {
		s.notify.NotifyAdmins(ctx, ReconcileText(rep))
	}
}

// start registers the user (first /start adds them to the bot) and sends
// the main menu. /start and /menu are a way out of any prompt.
func (s *Router) start(ctx context.Context, m *tgbot.Message) error {
	s.dialogs.drop(m.Chat.ID)
	menu, err := s.mainMenu(ctx, m.From)
	if err != nil {
		return err
	}
	return s.send.Send(
		ctx,
		OutMessage{
			ChatID:   m.Chat.ID,
			Text:     menu.Text,
			Keyboard: menu.Keyboard,
		},
	)
}

// backToMenu is the "◀️ Меню" button: it turns the same message back into
// the main menu, so the chat does not fill up with menus. If Telegram does
// not let the bot edit it (e.g. too old), the menu comes as a new message.
func (s *Router) backToMenu(ctx context.Context, cq *tgbot.CallbackQuery) error {
	s.dialogs.drop(cq.ChatID())
	menu, err := s.mainMenu(ctx, &cq.From)
	if err != nil {
		return err
	}
	err = s.send.Edit(
		ctx,
		EditMessage{
			ChatID:    cq.ChatID(),
			MessageID: cq.MessageID(),
			Text:      menu.Text,
			Keyboard:  menu.Keyboard,
		},
	)
	if err == nil {
		return nil
	}
	log.Printf("bot: menu in place: %v", err)
	return s.send.Send(
		ctx,
		OutMessage{
			ChatID:   cq.ChatID(),
			Text:     menu.Text,
			Keyboard: menu.Keyboard,
		},
	)
}

// mainMenu registers the user (or refreshes the username) and builds the
// menu for their role.
func (s *Router) mainMenu(ctx context.Context, from *tgbot.User) (*menuScreen, error) {
	u, err := s.users.Register(
		ctx,
		service.RegisterInput{
			ID:       from.ID,
			Username: from.Username,
		},
	)
	if err != nil {
		return nil, err
	}
	return &menuScreen{
		Text: greeting(u),
		Keyboard: mainKeyboard(
			menuView{
				Role:        u.Role,
				Maintenance: u.Role == service.RoleAdmin && s.maint.on(),
			},
		),
	}, nil
}

func (s *Router) callback(ctx context.Context, cq *tgbot.CallbackQuery) error {
	if err := s.send.Answer(ctx, cq.ID); err != nil {
		log.Printf("bot: answer callback: %v", err)
	}
	switch {
	case cq.Data == cbMenu:
		return s.backToMenu(ctx, cq)
	case cq.Data == cbFeedback:
		return s.askFeedback(ctx, cq)
	case strings.HasPrefix(cq.Data, cbReissueAsk), strings.HasPrefix(cq.Data, cbDeleteAsk):
		return s.askOwnKeyAction(ctx, cq)
	case strings.HasPrefix(cq.Data, cbReissue):
		return s.reissueKey(ctx, cq)
	case strings.HasPrefix(cq.Data, cbDelete):
		return s.deleteOwnKey(ctx, cq)
	case cq.Data == cbCreateKey:
		return s.keyStepApps(ctx, cq)
	case cq.Data == cbIssueKey:
		return s.askKeyName(ctx, cq)
	case cq.Data == cbKeyNoName:
		s.dialogs.drop(cq.ChatID())
		return s.issueKey(
			ctx,
			keyRequest{
				ChatID: cq.ChatID(),
				UserID: cq.SenderID(),
			},
		)
	case cq.Data == cbMyAccess:
		return s.myAccess(ctx, cq)
	case cq.Data == cbBypass:
		return s.sendBypass(ctx, cq.ChatID())
	case cq.Data == cbSupport, cq.Data == cbTerms:
		return s.send.Send(
			ctx,
			OutMessage{
				ChatID: cq.ChatID(),
				Text: s.commandText(
					command{
						Name:   cq.Data,
						UserID: cq.SenderID(),
					},
				),
				Keyboard: menuKeyboard(),
			},
		)
	case cq.Data == cbBuy:
		return s.buyMenu(ctx, cq)
	case strings.HasPrefix(cq.Data, cbBuyKey):
		return s.buyMenu(ctx, cq)
	case strings.HasPrefix(cq.Data, cbTariff):
		return s.invoice(ctx, cq)
	case strings.HasPrefix(cq.Data, cbConfig):
		return s.configAgain(ctx, cq)
	case strings.HasPrefix(cq.Data, cbAdmin):
		return s.admin(ctx, cq)
	}
	return nil
}

// askKeyName is step 2: it waits for the key's name, with a "skip" button.
func (s *Router) askKeyName(ctx context.Context, cq *tgbot.CallbackQuery) error {
	s.dialogs.set(
		pendingInput{
			ChatID: cq.ChatID(),
			UserID: cq.SenderID(),
			Kind:   pendingKeyName,
		},
	)
	return s.send.Send(
		ctx,
		OutMessage{
			ChatID:   cq.ChatID(),
			Text:     askKeyNameText,
			Keyboard: skipKeyNameKeyboard(),
		},
	)
}

// keyNamed creates the key with the name the user sent. A bad name asks
// again and keeps waiting; anything else ends the question. Only the user
// who asked answers.
func (s *Router) keyNamed(ctx context.Context, m *tgbot.Message) error {
	if p, _ := s.dialogs.peek(m.Chat.ID); p.UserID != m.From.ID {
		return nil
	}
	if strings.TrimSpace(m.Text) == "" { // a sticker or a photo: only "skip" means no name
		return s.send.Send(
			ctx,
			OutMessage{
				ChatID:   m.Chat.ID,
				Text:     askKeyNameText,
				Keyboard: skipKeyNameKeyboard(),
			},
		)
	}
	s.dialogs.drop(m.Chat.ID) // before issuing: a second text is not a second key
	err := s.issueKey(
		ctx,
		keyRequest{
			ChatID: m.Chat.ID,
			UserID: m.From.ID,
			Name:   m.Text,
		},
	)
	if !errors.Is(err, service.ErrBadKeyName) {
		return err
	}
	s.dialogs.set(
		pendingInput{
			ChatID: m.Chat.ID,
			UserID: m.From.ID,
			Kind:   pendingKeyName,
		},
	)
	return s.send.Send(
		ctx,
		OutMessage{
			ChatID:   m.Chat.ID,
			Text:     badKeyNameText,
			Keyboard: skipKeyNameKeyboard(),
		},
	)
}

// issueKey creates the user's key and sends it with the import steps.
// ErrBadKeyName is returned as is (the caller asks again); other known
// errors are explained to the user.
func (s *Router) issueKey(ctx context.Context, k keyRequest) error {
	p, err := s.keys.CreateKey(
		ctx,
		service.CreateKeyInput{
			UserID: k.UserID,
			Name:   k.Name,
		},
	)
	if errors.Is(err, service.ErrBadKeyName) {
		return err
	}
	if err != nil {
		text, known := createKeyErrorText(err)
		return s.replyError(
			ctx,
			userError{
				ChatID: k.ChatID,
				Err:    err,
				Text:   text,
				Known:  known,
			},
		)
	}
	if err := s.deliverKey(
		ctx,
		keyDelivery{
			ChatID: k.ChatID,
			Peer:   p,
		},
	); err != nil {
		// The key exists: say where it is, or a second press makes another.
		if sendErr := s.send.Send(
			ctx,
			OutMessage{
				ChatID:   k.ChatID,
				Text:     keyDeliveryFailedText,
				Keyboard: myAccessKeyboard(),
			},
		); sendErr != nil {
			log.Printf("bot: %v", sendErr)
		}
		return err
	}
	return nil
}

// private reports whether m is in a private chat. Keys, configs and the
// admin panel are never posted to groups, even if the bot is added to one.
func private(m *tgbot.Message) bool {
	return m != nil && m.Chat.Type == "private"
}

// commandText answers /terms, /support, /paysupport (Telegram requires the
// first and the last for bots that take Stars) and any unknown command.
func (s *Router) commandText(c command) string {
	switch c.Name {
	case cbTerms:
		return termsText(s.support)
	case cbSupport:
		return supportText(
			supportView{
				Contact: s.support,
				UserID:  c.UserID,
			},
		)
	case "paysupport":
		return paySupportText(s.support)
	}
	return unknownCommandText
}

// keyStepApps is step 1 of getting a key: which app to install, with
// store links and "next". It first checks that a key can be given, so no
// one installs an app to learn their trial is used up.
func (s *Router) keyStepApps(ctx context.Context, cq *tgbot.CallbackQuery) error {
	if err := s.keys.CheckCreateKey(ctx, cq.SenderID()); err != nil {
		text, known := createKeyErrorText(err)
		return s.replyError(
			ctx,
			userError{
				ChatID: cq.ChatID(),
				Err:    err,
				Text:   text,
				Known:  known,
			},
		)
	}
	return s.send.Send(
		ctx,
		OutMessage{
			ChatID:   cq.ChatID(),
			Text:     appsText,
			Keyboard: appsKeyboard(),
		},
	)
}

// deliverKey sends a new key (step 2): config, QR code and how to add it
// to the app, with "next" to the split-tunneling step.
func (s *Router) deliverKey(ctx context.Context, d keyDelivery) error {
	conf, err := s.keys.ClientConfig(ctx, d.Peer.PublicKey)
	if err != nil {
		return err
	}
	if err := s.sendConfig(
		ctx,
		configDelivery{
			ChatID: d.ChatID,
			Key: &service.KeyConfig{
				Peer: d.Peer,
				Conf: conf,
			},
		},
	); err != nil {
		return err
	}
	return s.send.Send(
		ctx,
		OutMessage{
			ChatID:   d.ChatID,
			Text:     importText,
			Keyboard: bypassNextKeyboard(),
		},
	)
}

// myAccess lists the user's keys with status, end date, last connection
// and traffic, with a "config again" button per key.
func (s *Router) myAccess(ctx context.Context, cq *tgbot.CallbackQuery) error {
	keys, err := s.keys.Access(ctx, cq.SenderID())
	if err != nil {
		return err
	}
	u, err := s.users.User(ctx, cq.SenderID())
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return s.send.Send(
			ctx,
			OutMessage{
				ChatID:   cq.ChatID(),
				Text:     noKeysText,
				Keyboard: createKeyKeyboard(),
			},
		)
	}
	return s.send.Send(
		ctx,
		OutMessage{
			ChatID: cq.ChatID(),
			Text:   accessText(keys),
			Keyboard: accessKeyboard(
				accessView{
					Keys:   keys,
					CanBuy: u.Role == service.RoleUser,
				},
			),
		},
	)
}

// configAgain resends one of the user's own keys.
func (s *Router) configAgain(ctx context.Context, cq *tgbot.CallbackQuery) error {
	kc, err := s.keys.UserConfig(
		ctx,
		service.UserKey{
			UserID:    cq.SenderID(),
			PublicKey: strings.TrimPrefix(cq.Data, cbConfig),
		},
	)
	if err != nil {
		text, known := configErrorText(err)
		return s.replyError(
			ctx,
			userError{
				ChatID: cq.ChatID(),
				Err:    err,
				Text:   text,
				Known:  known,
			},
		)
	}
	return s.sendConfig(
		ctx,
		configDelivery{
			ChatID: cq.ChatID(),
			Key:    kc,
		},
	)
}

// sendConfig sends the .conf file and its QR code. A config too long for a
// QR code only gets a note: the file alone is enough.
func (s *Router) sendConfig(ctx context.Context, d configDelivery) error {
	err := s.send.SendDocument(
		ctx,
		OutFile{
			ChatID:  d.ChatID,
			Name:    configFileName(d.Key.Peer),
			Data:    []byte(d.Key.Conf),
			Caption: keyCaption(d.Key.Peer),
		},
	)
	if err != nil {
		return err
	}
	return s.sendQR(
		ctx,
		OutFile{
			ChatID: d.ChatID,
			Data:   []byte(d.Key.Conf),
		},
	)
}

// sendQR sends the config (in.Data) as a QR code, or a note when it
// doesn't fit into one.
func (s *Router) sendQR(ctx context.Context, in OutFile) error {
	png, err := qrcode.Encode(string(in.Data), qrcode.Low, 768)
	if err != nil {
		return s.send.Send(
			ctx,
			OutMessage{
				ChatID: in.ChatID,
				Text:   qrTooLongText,
			},
		)
	}
	return s.send.SendPhoto(
		ctx,
		OutFile{
			ChatID:  in.ChatID,
			Name:    "qr.png",
			Data:    png,
			Caption: qrCaption,
		},
	)
}

// sendBypass is the split-tunneling step: how to set it up in AmneziaVPN,
// then the lists of Russian sites/networks that must not use the VPN. If
// the lists can't be downloaded the user gets a note instead.
func (s *Router) sendBypass(ctx context.Context, chatID int64) error {
	files, err := s.bypass.Files(ctx)
	if err != nil {
		log.Printf("bot: bypass lists: %v", err)
		return s.send.Send(
			ctx,
			OutMessage{
				ChatID: chatID,
				Text:   bypassDownText,
			},
		)
	}
	if err := s.send.Send(
		ctx,
		OutMessage{
			ChatID:   chatID,
			Text:     bypassHowToText,
			Keyboard: menuKeyboard(),
		},
	); err != nil {
		return err
	}
	for _, f := range files {
		err := s.send.SendDocument(
			ctx,
			OutFile{
				ChatID:  chatID,
				Name:    f.Name,
				Data:    f.Data,
				Caption: bypassCaption(f.Name),
			},
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// replyError tells the user what went wrong. An expected error (Known)
// ends there; an unexpected one is also returned, for the log. A failed
// send is logged.
func (s *Router) replyError(ctx context.Context, e userError) error {
	err := s.send.Send(
		ctx,
		OutMessage{
			ChatID: e.ChatID,
			Text:   e.Text,
		},
	)
	if err != nil {
		log.Printf("bot: %v", err)
	}
	if e.Known {
		return nil
	}
	return e.Err
}

// configErrorText explains why a key's config can't be sent again.
func configErrorText(err error) (text string, known bool) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		return keyNotFoundText, true
	case errors.Is(err, service.ErrNoPrivateKey):
		return noPrivateKeyText, true
	}
	return configFailedText, false
}

// createKeyErrorText explains expected CreateKey errors to the user.
// known is false for unexpected errors (they also go to the log).
func createKeyErrorText(err error) (text string, known bool) {
	switch {
	case errors.Is(err, service.ErrHasKey):
		return hasKeyText, true
	case errors.Is(err, service.ErrTrialUsed):
		return trialUsedText, true
	case errors.Is(err, service.ErrKeyLimit):
		return keyLimitText, true
	}
	return internalErrorText, false
}
