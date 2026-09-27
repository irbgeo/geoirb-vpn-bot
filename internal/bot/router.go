package bot

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/irbgeo/geoirb-vpn-bot/internal/bypass"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
	"github.com/irbgeo/geoirb-vpn-bot/internal/sysload"
)

// Service is what the bot needs from the business logic.
type Service interface {
	Register(ctx context.Context, in service.RegisterInput) (*service.User, error)
	Admins(ctx context.Context) ([]*service.User, error)
	Reconcile(ctx context.Context) (*service.ReconcileReport, error)
	CreateKey(ctx context.Context, in service.CreateKeyInput) (*service.Peer, error)
	CheckCreateKey(ctx context.Context, userID int64) error
	ClientConfig(ctx context.Context, publicKey string) (string, error)
	Access(ctx context.Context, userID int64) ([]service.KeyInfo, error)
	UserConfig(ctx context.Context, k service.UserKey) (*service.KeyConfig, error)

	// admin panel
	User(ctx context.Context, id int64) (*service.User, error)
	Users(ctx context.Context, p service.Page) ([]*service.User, int64, error)
	Key(ctx context.Context, publicKey string) (*service.Peer, error)
	Disable(ctx context.Context, publicKey string) error
	Enable(ctx context.Context, publicKey string) error
	Delete(ctx context.Context, publicKey string) error
	Extend(ctx context.Context, in service.ExtendInput) (*service.Peer, error)
	Issue(ctx context.Context, in service.IssueInput) (*service.Peer, error)

	// payments
	Tariffs() []service.Tariff
	Invoice(ctx context.Context, in service.PurchaseInput) (*service.Invoice, error)
	CheckPurchase(ctx context.Context, in service.PaymentInput) error
	Pay(ctx context.Context, in service.PaymentInput) (*service.PayResult, error)
	MarkRefunded(ctx context.Context, chargeID string) error
	Payments(ctx context.Context, userID int64) ([]*service.Payment, error)
	UnfinishedPayments(ctx context.Context) ([]*service.Payment, error)
	Stats(ctx context.Context) (*service.Stats, error)
	BroadcastRecipients(ctx context.Context) ([]int64, error)
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
	cbCreateKey = "key:create" // step 1: which app to install
	cbIssueKey  = "key:issue"  // step 2: ask for the key's name
	cbKeyNoName = "key:noname" // skip the name: the key and how to add it
	cbMyAccess  = "my"
	cbBypass    = "bypass"
	cbSupport   = "support"
	cbTerms     = "terms"
	cbBuy       = "buy"   // tariffs for "my key"
	cbBuyKey    = "buyk:" // + public key: tariffs for that key
	cbTariff    = "buy:"  // + days [+ ":" + public key]: send the invoice
	cbConfig    = "cfg:"  // + public key (44 chars)
)

// Router turns Telegram updates into service calls and replies.
type Router struct {
	svc     Service
	send    Sender
	bypass  Bypass
	support string // support contact

	// pending: what an admin's next text message is (a key name, a
	// broadcast text), by admin chat. In memory only: after a restart the
	// admin presses the button again.
	mu      sync.Mutex
	pending map[int64]pendingInput
	// subnetAlerted: the "subnet almost full" alert went out and usage is
	// still high; cleared when it drops, so the next rise alerts again.
	subnetAlerted bool
	// backupStamp is touched by every good backup; backupAlerted works like
	// subnetAlerted for "no fresh backup".
	backupStamp   string
	backupAlerted bool
	load          ServerLoad
	// maintFlag / maintOn: the maintenance state, in a file (survives a
	// restart) or, without one, in memory.
	maintFlag string
	maintOn   bool
	// pause between broadcast messages (Telegram allows ~30 per second).
	pause time.Duration
	// refunding: charge IDs an admin refund is running for, so a double
	// press doesn't call Telegram twice.
	refunding map[string]bool
	// configsRunning: a "send everyone a fresh config" run is going; a
	// second press waits for it to end.
	configsRunning bool
	// jobs: background work (broadcasts) that Close waits for. They run on
	// life, not on an update's ctx: go-tgbot's Dispatcher cancels that one
	// as soon as the handler returns.
	jobs sync.WaitGroup
	life context.Context
	stop context.CancelFunc
}

// New creates a Router.
func New(
	d *Deps,
) *Router {
	life, stop := context.WithCancel(context.Background())
	return &Router{
		svc:         d.Service,
		send:        d.Sender,
		bypass:      d.Bypass,
		support:     d.SupportContact,
		backupStamp: d.BackupStamp,
		load:        d.Load,
		maintFlag:   d.MaintenanceFlag,
		pending:     map[int64]pendingInput{},
		pause:       50 * time.Millisecond,
		refunding:   map[string]bool{},
		life:        life,
		stop:        stop,
	}
}

// Handle processes one update. Updates it doesn't know are ignored.
func (r *Router) Handle(ctx context.Context, upd tgbot.Update) error {
	if upd.PreCheckoutQuery != nil {
		return r.preCheckout(ctx, upd.PreCheckoutQuery)
	}
	if upd.Message != nil && upd.Message.SuccessfulPayment != nil {
		return r.paid(ctx, upd.Message)
	}
	if upd.CallbackQuery != nil {
		return r.callback(ctx, upd.CallbackQuery)
	}
	if upd.Message == nil || upd.Message.From == nil {
		return nil
	}
	if cmd, ok := upd.Command(); ok {
		// /start: Telegram sends it on the first "Start"; /menu is the name
		// users see in the command list and texts.
		if cmd == "start" || cmd == "menu" {
			return r.start(ctx, upd.Message)
		}
		return r.send.Send(
			ctx,
			OutMessage{
				ChatID: upd.ChatID(),
				Text: r.commandText(
					command{
						Name:   cmd,
						UserID: upd.Message.From.ID,
					},
				),
			},
		)
	}
	if r.pendingKind(upd.Message.Chat.ID) == pendingKeyName {
		return r.keyNamed(ctx, upd.Message)
	}
	return r.adminText(ctx, upd.Message)
}

// Close stops background work (a running broadcast stops and sends its
// report) and waits for it. Call it on shutdown, after the updates stopped.
func (r *Router) Close() {
	r.stop()
	r.Wait()
}

// Wait blocks until background work is done.
func (r *Router) Wait() {
	r.jobs.Wait()
}

// Reconcile runs at startup: payments left half-done by a stop, then the
// DB against the server. Problems are logged and sent to admins; nothing
// is changed.
func (r *Router) Reconcile(ctx context.Context) {
	if ps, err := r.svc.UnfinishedPayments(ctx); err != nil {
		log.Printf("reconcile: unfinished payments: %v", err)
	} else if len(ps) > 0 {
		r.NotifyAdmins(ctx, unfinishedPaymentsText(ps))
	}

	rep, err := r.svc.Reconcile(ctx)
	if err != nil {
		log.Printf("reconcile: %v", err)
		r.NotifyAdmins(ctx, reconcileFailedText(err))
		return
	}
	log.Printf(
		"reconcile: missing on server %d, disabled but on server %d, manual %d",
		len(rep.MissingOnServer),
		len(rep.DisabledButOnServer),
		rep.Manual,
	)
	if !rep.OK() {
		r.NotifyAdmins(ctx, ReconcileText(rep))
	}
}

// NotifyAdmins sends text to every admin. A failed send (e.g. an admin who
// blocked the bot) is logged and the rest still get it.
func (r *Router) NotifyAdmins(ctx context.Context, text string) {
	admins, err := r.svc.Admins(ctx)
	if err != nil {
		log.Printf("bot: list admins: %v", err)
		return
	}
	if len(admins) == 0 {
		log.Printf("bot: no admins in the DB, alert only logged: %s", text)
		return
	}
	for _, a := range admins {
		err := r.send.Send(
			ctx,
			OutMessage{
				ChatID: a.ID,
				Text:   text,
			},
		)
		if err != nil {
			log.Printf("bot: notify admin %d: %v", a.ID, err)
		}
	}
}

// start registers the user (first /start adds them to the bot) and shows
// the main button.
func (r *Router) start(ctx context.Context, m *tgbot.Message) error {
	r.dropPending(m.Chat.ID) // /start is a way out of any prompt
	u, err := r.svc.Register(
		ctx,
		service.RegisterInput{
			ID:       m.From.ID,
			Username: m.From.Username,
		},
	)
	if err != nil {
		return err
	}
	return r.send.Send(
		ctx,
		OutMessage{
			ChatID: m.Chat.ID,
			Text:   greeting(u),
			Keyboard: mainKeyboard(
				menuView{
					Role:        u.Role,
					Maintenance: u.Role == service.RoleAdmin && r.maintenance(),
				},
			),
		},
	)
}

func (r *Router) callback(ctx context.Context, cq *tgbot.CallbackQuery) error {
	if err := r.send.Answer(ctx, cq.ID); err != nil {
		log.Printf("bot: answer callback: %v", err)
	}
	switch {
	case cq.Data == cbCreateKey:
		return r.keyStepApps(ctx, cq)
	case cq.Data == cbIssueKey:
		return r.askKeyName(ctx, cq)
	case cq.Data == cbKeyNoName:
		r.dropPending(cq.ChatID())
		return r.issueKey(
			ctx,
			keyRequest{
				ChatID: cq.ChatID(),
				UserID: cq.SenderID(),
			},
		)
	case cq.Data == cbMyAccess:
		return r.myAccess(ctx, cq)
	case cq.Data == cbBypass:
		return r.sendBypass(ctx, cq.ChatID())
	case cq.Data == cbSupport, cq.Data == cbTerms:
		return r.send.Send(
			ctx,
			OutMessage{
				ChatID: cq.ChatID(),
				Text: r.commandText(
					command{
						Name:   cq.Data,
						UserID: cq.SenderID(),
					},
				),
			},
		)
	case cq.Data == cbBuy:
		return r.buyMenu(ctx, cq)
	case strings.HasPrefix(cq.Data, cbBuyKey):
		return r.buyMenu(ctx, cq)
	case strings.HasPrefix(cq.Data, cbTariff):
		return r.invoice(ctx, cq)
	case strings.HasPrefix(cq.Data, cbConfig):
		return r.configAgain(ctx, cq)
	case strings.HasPrefix(cq.Data, cbAdmin):
		return r.admin(ctx, cq)
	}
	return nil
}

// createKey creates the user's key (a plain user's first key starts the
// trial) and sends the config file and QR code.
func (r *Router) askKeyName(ctx context.Context, cq *tgbot.CallbackQuery) error {
	r.setPending(
		pendingInput{
			ChatID: cq.ChatID(),
			Kind:   pendingKeyName,
		},
	)
	return r.send.Send(
		ctx,
		OutMessage{
			ChatID:   cq.ChatID(),
			Text:     askKeyNameText,
			Keyboard: skipKeyNameKeyboard(),
		},
	)
}

// pendingKind is what the chat's next text is for, 0 when nothing (or a
// prompt older than pendingTTL) is waiting.
func (r *Router) pendingKind(chatID int64) pendingKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.pending[chatID]
	if !ok || time.Since(p.At) > pendingTTL {
		return 0
	}
	return p.Kind
}

// keyNamed creates the key with the name the user sent. A bad name asks
// again and keeps waiting; anything else ends the question.
func (r *Router) keyNamed(ctx context.Context, m *tgbot.Message) error {
	if strings.TrimSpace(m.Text) == "" { // a sticker or a photo: only "skip" means no name
		return r.send.Send(
			ctx,
			OutMessage{
				ChatID:   m.Chat.ID,
				Text:     askKeyNameText,
				Keyboard: skipKeyNameKeyboard(),
			},
		)
	}
	r.dropPending(m.Chat.ID) // before issuing: a second text is not a second key
	err := r.issueKey(
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
	r.setPending(
		pendingInput{
			ChatID: m.Chat.ID,
			Kind:   pendingKeyName,
		},
	)
	return r.send.Send(
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
func (r *Router) issueKey(ctx context.Context, k keyRequest) error {
	p, err := r.svc.CreateKey(
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
		return r.replyError(
			ctx,
			userError{
				ChatID: k.ChatID,
				Err:    err,
				Text:   text,
				Known:  known,
			},
		)
	}
	return r.deliverKey(
		ctx,
		keyDelivery{
			ChatID: k.ChatID,
			Peer:   p,
		},
	)
}

// commandText answers /terms, /support, /paysupport (Telegram requires the
// first and the last for bots that take Stars) and any unknown command.
func (r *Router) commandText(c command) string {
	switch c.Name {
	case cbTerms:
		return termsText(r.support)
	case cbSupport:
		return supportText(
			supportView{
				Contact: r.support,
				UserID:  c.UserID,
			},
		)
	case "paysupport":
		return paySupportText(r.support)
	}
	return unknownCommandText
}

// keyStepApps is step 1 of getting a key: which app to install, with
// store links and "next". It first checks that a key can be given, so no
// one installs an app to learn their trial is used up.
func (r *Router) keyStepApps(ctx context.Context, cq *tgbot.CallbackQuery) error {
	if err := r.svc.CheckCreateKey(ctx, cq.SenderID()); err != nil {
		text, known := createKeyErrorText(err)
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
	return r.send.Send(
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
func (r *Router) deliverKey(ctx context.Context, d keyDelivery) error {
	conf, err := r.svc.ClientConfig(ctx, d.Peer.PublicKey)
	if err != nil {
		return err
	}
	if err := r.sendConfig(
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
	return r.send.Send(
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
func (r *Router) myAccess(ctx context.Context, cq *tgbot.CallbackQuery) error {
	keys, err := r.svc.Access(ctx, cq.SenderID())
	if err != nil {
		return err
	}
	u, err := r.svc.User(ctx, cq.SenderID())
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return r.send.Send(
			ctx,
			OutMessage{
				ChatID:   cq.ChatID(),
				Text:     noKeysText,
				Keyboard: createKeyKeyboard(),
			},
		)
	}
	return r.send.Send(
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
func (r *Router) configAgain(ctx context.Context, cq *tgbot.CallbackQuery) error {
	kc, err := r.svc.UserConfig(
		ctx,
		service.UserKey{
			UserID:    cq.SenderID(),
			PublicKey: strings.TrimPrefix(cq.Data, cbConfig),
		},
	)
	if err != nil {
		text, known := configErrorText(err)
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
	return r.sendConfig(
		ctx,
		configDelivery{
			ChatID: cq.ChatID(),
			Key:    kc,
		},
	)
}

// sendConfig sends the .conf file and its QR code. A config too long for a
// QR code only gets a note: the file alone is enough.
func (r *Router) sendConfig(ctx context.Context, d configDelivery) error {
	err := r.send.SendDocument(
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
	return r.sendQR(
		ctx,
		OutFile{
			ChatID: d.ChatID,
			Data:   []byte(d.Key.Conf),
		},
	)
}

// sendQR sends the config (in.Data) as a QR code, or a note when it
// doesn't fit into one.
func (r *Router) sendQR(ctx context.Context, in OutFile) error {
	png, err := qrcode.Encode(string(in.Data), qrcode.Low, 768)
	if err != nil {
		return r.send.Send(
			ctx,
			OutMessage{
				ChatID: in.ChatID,
				Text:   qrTooLongText,
			},
		)
	}
	return r.send.SendPhoto(
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
func (r *Router) sendBypass(ctx context.Context, chatID int64) error {
	files, err := r.bypass.Files(ctx)
	if err != nil {
		log.Printf("bot: bypass lists: %v", err)
		return r.send.Send(
			ctx,
			OutMessage{
				ChatID: chatID,
				Text:   bypassDownText,
			},
		)
	}
	if err := r.send.Send(
		ctx,
		OutMessage{
			ChatID: chatID,
			Text:   bypassHowToText,
		},
	); err != nil {
		return err
	}
	for _, f := range files {
		err := r.send.SendDocument(
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
func (r *Router) replyError(ctx context.Context, e userError) error {
	err := r.send.Send(
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
