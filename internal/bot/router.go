package bot

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
	"github.com/irbgeo/geoirb-vpn-bot/internal/sysload"
	"github.com/irbgeo/geoirb-vpn-bot/internal/tunnel"
)

// The bot's needs from the business logic, split by topic so each handler
// depends only on what it uses. The value service.New returns implements them all.

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
// normal (the sysload monitor).
type ServerLoad interface {
	Check() ([]sysload.Alert, error)
}

// tunnelChecker is the exit tunnel watcher (tunnel.New).
type tunnelChecker interface {
	Check(ctx context.Context) (tunnel.State, bool, error)
}

// Callback data of inline buttons (Telegram allows up to 64 bytes).
const (
	cbMenu       = "menu"       // back to the main menu, in the same message
	cbFeedback   = "feedback"   // reviews and suggestions: asks for the text
	cbCreateKey  = "key:create" // step 1: which app to install
	cbIssueKey   = "key:issue"  // step 2: ask for the key's name
	cbKeyNoName  = "key:noname" // skip the name: the key and how to add it
	cbMyAccess   = "my"
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
	cbSplitAsk   = "split:ask"
	cbSplitVideo = "split:video"
	cbSplitNone  = "split:none"
)

// keyActionsPerMinute: how many reissues, deletes and config resends one
// user may ask for in a minute (see costlyKeyAction).
const keyActionsPerMinute = 5

// router turns Telegram updates into service calls and replies. Its own
// state is kept in small types with their own locks (state.go).
type router struct {
	users    Users
	keys     Keys
	billing  Billing
	ops      Ops
	feedback Feedback
	send     Sender
	notify   *notifier
	support  string // support contact
	// splitVideo: the video on app split tunneling; empty = no such step.
	splitVideo *videoFile

	// dialogs: what each chat's next input is (a broadcast text, a key
	// name). In memory only: after a restart the button is pressed again.
	dialogs *dialogs
	// jobs: the one background mass send; Close waits for it.
	jobs  *jobs
	maint *maintFlag
	// refunds: charge IDs an admin refund is running for.
	refunds *inFlight
	// feedbackLimit: reviews one user may send per hour.
	feedbackLimit *rateLimit[int64]
	// keyActions: reissues, deletes and config resends of one user per minute.
	keyActions *rateLimit[int64]
	// adminRepeats: admin buttons that add something on every press, by
	// button data: one press per repeatPressGap.
	adminRepeats *rateLimit[string]
	// pause between broadcast messages (Telegram allows ~30 per second).
	pause time.Duration
}

// New creates a router.
func New(
	d *Deps,
) *router {
	dialogs := newDialogs()
	jobs := newJobs()
	maint := newMaintFlag(d.Config.MaintenanceFlag)
	refunds := newInFlight()
	splitVideo := newVideoFile(d.SplitVideo)
	feedbackLimit := newRateLimit[int64](
		feedbackPerHour,
		time.Hour,
	)
	keyActions := newRateLimit[int64](
		keyActionsPerMinute,
		time.Minute,
	)
	adminRepeats := newRateLimit[string](
		1,
		repeatPressGap,
	)
	return &router{
		users:         d.Users,
		keys:          d.Keys,
		billing:       d.Billing,
		ops:           d.Ops,
		feedback:      d.Feedback,
		send:          d.Sender,
		support:       d.Config.SupportContact,
		splitVideo:    splitVideo,
		notify:        d.Notifier,
		dialogs:       dialogs,
		jobs:          jobs,
		maint:         maint,
		refunds:       refunds,
		feedbackLimit: feedbackLimit,
		keyActions:    keyActions,
		adminRepeats:  adminRepeats,
		pause:         50 * time.Millisecond,
	}
}

// Handle processes one update. Updates it doesn't know are ignored.
func (s *router) Handle(ctx context.Context, upd tgbot.Update) error {
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
	cmd, ok := upd.Command()
	if ok {
		// /start: Telegram sends it on the first "Start"; /menu is the name
		// users see in the command list and texts.
		if cmd == "start" || cmd == "menu" {
			return s.start(ctx, upd.Message)
		}
		command := command{
			Name:   cmd,
			UserID: upd.Message.From.ID,
		}
		outMessage := outMessage{
			ChatID: upd.ChatID(),
			Text:   s.commandText(command),
		}
		return s.send.Send(ctx, outMessage)
	}
	p, ok := s.dialogs.peek(upd.Message.Chat.ID)
	if ok {
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
func (s *router) Close() {
	s.jobs.close()
}

// Wait blocks until background work is done.
func (s *router) Wait() {
	s.jobs.wait()
}

// Reconcile runs at startup: payments left half-done by a stop, then the
// DB against the server. Problems are logged and sent to admins; nothing
// is changed.
func (s *router) Reconcile(ctx context.Context) {
	ps, err := s.billing.UnfinishedPayments(ctx)
	if err != nil {
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

// private reports whether m is in a private chat. Keys, configs and the
// admin panel are never posted to groups, even if the bot is added to one.
func private(m *tgbot.Message) bool {
	return m != nil && m.Chat.Type == "private"
}

func (s *router) callback(ctx context.Context, cq *tgbot.CallbackQuery) error {
	err := s.send.Answer(ctx, cq.ID)
	if err != nil {
		log.Printf("bot: answer callback: %v", err)
	}
	if costlyKeyAction(cq.Data) && !s.keyActions.allow(cq.SenderID()) {
		outMessage := outMessage{
			ChatID: cq.ChatID(),
			Text:   tooOftenText,
		}
		return s.send.Send(ctx, outMessage)
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
		return s.skipKeyName(ctx, cq)
	case cq.Data == cbMyAccess:
		return s.myAccess(ctx, cq)
	case cq.Data == cbSplitAsk:
		return s.askDevice(ctx, cq.ChatID())
	case cq.Data == cbSplitVideo:
		return s.sendSplitVideo(ctx, cq.ChatID())
	case cq.Data == cbSplitNone:
		return s.splitNone(ctx, cq.ChatID())
	case cq.Data == cbSupport, cq.Data == cbTerms:
		command := command{
			Name:   cq.Data,
			UserID: cq.SenderID(),
		}
		outMessage := outMessage{
			ChatID:   cq.ChatID(),
			Text:     s.commandText(command),
			Keyboard: menuKeyboard(),
		}
		return s.send.Send(ctx, outMessage)
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

// costlyKeyAction: the button makes the server work for one user's key (a
// reissue or delete rewrites the server config, a resend reads it). The
// "are you sure" buttons (kr?:, kd?:) are not among them.
func costlyKeyAction(data string) bool {
	return strings.HasPrefix(data, cbReissue) || strings.HasPrefix(data, cbDelete) || strings.HasPrefix(data, cbConfig)
}

// start registers the user (first /start adds them to the bot) and sends
// the main menu. /start and /menu are a way out of any prompt.
func (s *router) start(ctx context.Context, m *tgbot.Message) error {
	s.dialogs.drop(m.Chat.ID)
	menu, err := s.mainMenu(ctx, m.From)
	if err != nil {
		return err
	}
	outMessage := outMessage{
		ChatID:   m.Chat.ID,
		Text:     menu.Text,
		Keyboard: menu.Keyboard,
	}
	return s.send.Send(ctx, outMessage)
}

// keyNamed creates the key with the name the user sent. A bad name asks
// again and keeps waiting; anything else ends the question. (Only private
// chats are handled, so the chat is the user who was asked.)
func (s *router) keyNamed(ctx context.Context, m *tgbot.Message) error {
	if strings.TrimSpace(m.Text) == "" { // a sticker or a photo: only "skip" means no name
		outMessage := outMessage{
			ChatID:   m.Chat.ID,
			Text:     askKeyNameText,
			Keyboard: skipKeyNameKeyboard(),
		}
		return s.send.Send(ctx, outMessage)
	}
	s.dialogs.drop(m.Chat.ID) // before issuing: a second text is not a second key
	keyRequest := keyRequest{
		ChatID: m.Chat.ID,
		UserID: m.From.ID,
		Name:   m.Text,
	}
	err := s.issueKey(ctx, keyRequest)
	if !errors.Is(err, service.ErrBadKeyName) {
		return err
	}
	pendingInput := pendingInput{
		ChatID: m.Chat.ID,
		Kind:   pendingKeyName,
	}
	s.dialogs.set(pendingInput)
	outMessage := outMessage{
		ChatID:   m.Chat.ID,
		Text:     badKeyNameText,
		Keyboard: skipKeyNameKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}

// backToMenu is the "◀️ Меню" button: it turns the same message back into
// the main menu, so the chat does not fill up with menus. If Telegram does
// not let the bot edit it (e.g. too old), the menu comes as a new message.
func (s *router) backToMenu(ctx context.Context, cq *tgbot.CallbackQuery) error {
	s.dialogs.drop(cq.ChatID())
	menu, err := s.mainMenu(ctx, &cq.From)
	if err != nil {
		return err
	}
	editMessage := editMessage{
		ChatID:    cq.ChatID(),
		MessageID: cq.MessageID(),
		Text:      menu.Text,
		Keyboard:  menu.Keyboard,
	}
	err = s.send.Edit(ctx, editMessage)
	if err == nil {
		return nil
	}
	log.Printf("bot: menu in place: %v", err)
	outMessage := outMessage{
		ChatID:   cq.ChatID(),
		Text:     menu.Text,
		Keyboard: menu.Keyboard,
	}
	return s.send.Send(ctx, outMessage)
}

// mainMenu registers the user (or refreshes the username) and builds the
// menu for their role.
func (s *router) mainMenu(ctx context.Context, from *tgbot.User) (*menuScreen, error) {
	registerInput := service.RegisterInput{
		ID:       from.ID,
		Username: from.Username,
	}
	u, err := s.users.Register(ctx, registerInput)
	if err != nil {
		return nil, err
	}
	menuView := menuView{
		Role:        u.Role,
		Maintenance: u.Role == service.RoleAdmin && s.maint.on(),
		SplitVideo:  !s.splitVideo.empty(),
	}
	return &menuScreen{
		Text:     greeting(u),
		Keyboard: mainKeyboard(menuView),
	}, nil
}

// keyStepApps is step 1 of getting a key: which app to install, with
// store links and "next". It first checks that a key can be given, so no
// one installs an app to learn their trial is used up.
func (s *router) keyStepApps(ctx context.Context, cq *tgbot.CallbackQuery) error {
	err := s.keys.CheckCreateKey(ctx, cq.SenderID())
	if err != nil {
		text, known := createKeyErrorText(err)
		userError := userError{
			ChatID: cq.ChatID(),
			Err:    err,
			Text:   text,
			Known:  known,
		}
		return s.replyError(ctx, userError)
	}
	outMessage := outMessage{
		ChatID:   cq.ChatID(),
		Text:     appsText,
		Keyboard: appsKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
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

// configAgain resends one of the user's own keys.
func (s *router) configAgain(ctx context.Context, cq *tgbot.CallbackQuery) error {
	userKey := service.UserKey{
		UserID:    cq.SenderID(),
		PublicKey: strings.TrimPrefix(cq.Data, cbConfig),
	}
	kc, err := s.keys.UserConfig(ctx, userKey)
	if err != nil {
		text, known := configErrorText(err)
		userError := userError{
			ChatID: cq.ChatID(),
			Err:    err,
			Text:   text,
			Known:  known,
		}
		return s.replyError(ctx, userError)
	}
	configDelivery := configDelivery{
		ChatID: cq.ChatID(),
		Key:    kc,
	}
	return s.sendConfig(ctx, configDelivery)
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

// replyError tells the user what went wrong. An expected error (Known)
// ends there; an unexpected one is also returned, for the log. A failed
// send is logged.
func (s *router) replyError(ctx context.Context, e userError) error {
	outMessage := outMessage{
		ChatID: e.ChatID,
		Text:   e.Text,
	}
	err := s.send.Send(ctx, outMessage)
	if err != nil {
		log.Printf("bot: %v", err)
	}
	if e.Known {
		return nil
	}
	return e.Err
}

// askKeyName is step 2: it waits for the key's name, with a "skip" button.
func (s *router) askKeyName(ctx context.Context, cq *tgbot.CallbackQuery) error {
	pendingInput := pendingInput{
		ChatID: cq.ChatID(),
		Kind:   pendingKeyName,
	}
	s.dialogs.set(pendingInput)
	outMessage := outMessage{
		ChatID:   cq.ChatID(),
		Text:     askKeyNameText,
		Keyboard: skipKeyNameKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}

// skipKeyName issues a key with the default name, but only while a key-name
// dialog is open for this chat; an old Skip button just shows the menu.
func (s *router) skipKeyName(ctx context.Context, cq *tgbot.CallbackQuery) error {
	p, ok := s.dialogs.peek(cq.ChatID())
	if !ok || p.Kind != pendingKeyName {
		menu, err := s.mainMenu(ctx, &cq.From)
		if err != nil {
			return err
		}
		outMessage := outMessage{
			ChatID:   cq.ChatID(),
			Text:     staleButtonText + "\n\n" + menu.Text,
			Keyboard: menu.Keyboard,
		}
		return s.send.Send(ctx, outMessage)
	}
	s.dialogs.drop(cq.ChatID())
	keyRequest := keyRequest{
		ChatID: cq.ChatID(),
		UserID: cq.SenderID(),
	}
	return s.issueKey(ctx, keyRequest)
}

// issueKey creates the user's key and sends it with the import steps.
// ErrBadKeyName is returned as is (the caller asks again); other known
// errors are explained to the user.
func (s *router) issueKey(ctx context.Context, k keyRequest) error {
	createKeyInput := service.CreateKeyInput{
		UserID: k.UserID,
		Name:   k.Name,
	}
	p, err := s.keys.CreateKey(ctx, createKeyInput)
	if errors.Is(err, service.ErrBadKeyName) {
		return err
	}
	if err != nil {
		text, known := createKeyErrorText(err)
		userError := userError{
			ChatID: k.ChatID,
			Err:    err,
			Text:   text,
			Known:  known,
		}
		return s.replyError(ctx, userError)
	}
	keyDelivery := keyDelivery{
		ChatID: k.ChatID,
		Peer:   p,
	}
	err = s.deliverKey(ctx, keyDelivery)
	if err != nil {
		// The key exists: say where it is, or a second press makes another.
		outMessage := outMessage{
			ChatID:   k.ChatID,
			Text:     keyDeliveryFailedText,
			Keyboard: myAccessKeyboard(),
		}
		sendErr := s.send.Send(ctx, outMessage)
		if sendErr != nil {
			log.Printf("bot: %v", sendErr)
		}
		return err
	}
	return nil
}

// deliverKey sends a new key (step 2): config, QR code and how to add it
// to the app; then step 3 (askDevice) while there is a video for it.
func (s *router) deliverKey(ctx context.Context, d keyDelivery) error {
	conf, err := s.keys.ClientConfig(ctx, d.Peer.PublicKey)
	if err != nil {
		return err
	}
	configDelivery := configDelivery{
		ChatID: d.ChatID,
		Key: &service.KeyConfig{
			Peer: d.Peer,
			Conf: conf,
		},
	}
	err = s.sendConfig(ctx, configDelivery)
	if err != nil {
		return err
	}
	outMessage := outMessage{
		ChatID: d.ChatID,
		Text:   importText,
	}
	if s.splitVideo.empty() {
		outMessage.Keyboard = menuKeyboard()
		return s.send.Send(ctx, outMessage)
	}
	err = s.send.Send(ctx, outMessage)
	if err != nil {
		return err
	}
	return s.askDevice(ctx, d.ChatID)
}

// sendConfig sends the .conf file and its QR code. A config too long for a
// QR code only gets a note: the file alone is enough.
func (s *router) sendConfig(ctx context.Context, d configDelivery) error {
	confFile := outFile{
		ChatID:  d.ChatID,
		Name:    configFileName(d.Key.Peer),
		Data:    []byte(d.Key.Conf),
		Caption: keyCaption(d.Key.Peer),
	}
	err := s.send.SendDocument(ctx, confFile)
	if err != nil {
		return err
	}
	qrFile := outFile{
		ChatID: d.ChatID,
		Data:   []byte(d.Key.Conf),
	}
	return s.sendQR(ctx, qrFile)
}

// sendQR sends the config (in.Data) as a QR code, or a note when it
// doesn't fit into one.
func (s *router) sendQR(ctx context.Context, in outFile) error {
	png, err := qrcode.Encode(string(in.Data), qrcode.Low, 768)
	if err != nil {
		outMessage := outMessage{
			ChatID: in.ChatID,
			Text:   qrTooLongText,
		}
		return s.send.Send(ctx, outMessage)
	}
	outFile := outFile{
		ChatID:  in.ChatID,
		Name:    "qr.png",
		Data:    png,
		Caption: qrCaption,
	}
	return s.send.SendPhoto(ctx, outFile)
}

// myAccess lists the user's keys with status, end date, last connection
// and traffic, with a "config again" button per key.
func (s *router) myAccess(ctx context.Context, cq *tgbot.CallbackQuery) error {
	keys, err := s.keys.Access(ctx, cq.SenderID())
	if err != nil {
		return err
	}
	u, err := s.users.User(ctx, cq.SenderID())
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		outMessage := outMessage{
			ChatID:   cq.ChatID(),
			Text:     noKeysText,
			Keyboard: createKeyKeyboard(),
		}
		return s.send.Send(ctx, outMessage)
	}
	accessView := accessView{
		Keys:   keys,
		CanBuy: u.Role == service.RoleUser,
	}
	outMessage := outMessage{
		ChatID:   cq.ChatID(),
		Text:     accessText(keys),
		Keyboard: accessKeyboard(accessView),
	}
	return s.send.Send(ctx, outMessage)
}

// commandText answers /terms, /support, /paysupport (Telegram requires the
// first and the last for bots that take Stars) and any unknown command.
func (s *router) commandText(c command) string {
	switch c.Name {
	case cbTerms:
		return termsText(s.support)
	case cbSupport:
		supportView := supportView{
			Contact: s.support,
			UserID:  c.UserID,
		}
		return supportText(supportView)
	case "paysupport":
		return paySupportText(s.support)
	}
	return unknownCommandText
}
