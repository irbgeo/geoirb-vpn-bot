package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// Admin actions: buttons are "a:<action>:<arg>". Each action name is
// written once here; the buttons and the dispatch both use it.
const (
	actUsers       = "users"  // + page (from 0)
	actUser        = "user"   // + user ID
	actDisable     = "dis"    // + public key
	actEnable      = "en"     // + public key
	actExtend      = "ext"    // + public key
	actConfig      = "cfg"    // + public key
	actDelete      = "del"    // + public key: asks to confirm
	actDeleteOK    = "delok"  // + public key: confirmed
	actIssue       = "iss"    // + user ID: pick a term
	actIssueDays   = "issd"   // + user ID + ":" + days (0 = never expires)
	actStats       = "stats"  //
	actBroadcast   = "bc"     // asks for the broadcast text
	actBroadcastOK = "bcok"   // + preview token: sends the previewed broadcast
	actCancel      = "cancel" // drops what the bot waits for (broadcast text)
	actFeedback    = "fb"     // + page (from 0): reviews and suggestions
	actConfigs     = "cfgs"   // asks to send every user a fresh config
	actConfigsOK   = "cfgsok" // + preview token: confirmed, send them
	actMaint       = "mnt"    // previews "maintenance started" or, while on, "over"
	actRefund      = "ref"    // + user ID + ":" + payRef: asks to confirm
	actRefundOK    = "refok"  // confirmed: return the Stars
)

const (
	cbAdmin      = "a:"
	cbAdminUsers = cbAdmin + actUsers + ":"
	cbAdminUser  = cbAdmin + actUser + ":"
	cbAdminDis   = cbAdmin + actDisable + ":"
	cbAdminEn    = cbAdmin + actEnable + ":"
	cbAdminExt   = cbAdmin + actExtend + ":"
	cbAdminCfg   = cbAdmin + actConfig + ":"
	cbAdminDel   = cbAdmin + actDelete + ":"
	cbAdminDelOK = cbAdmin + actDeleteOK + ":"
	cbAdminIss   = cbAdmin + actIssue + ":"
	cbAdminIssD  = cbAdmin + actIssueDays + ":"
	cbAdminStats = cbAdmin + actStats
	cbAdminBc    = cbAdmin + actBroadcast
	cbAdminBcOK  = cbAdmin + actBroadcastOK
	cbAdminCanc  = cbAdmin + actCancel
	cbAdminFb    = cbAdmin + actFeedback + ":"
	cbAdminCfgs  = cbAdmin + actConfigs
	cbAdminCfgOK = cbAdmin + actConfigsOK
	cbAdminMnt   = cbAdmin + actMaint
	cbAdminRef   = cbAdmin + actRefund + ":"
	cbAdminRefOK = cbAdmin + actRefundOK + ":"

	adminPageSize   = 10
	adminExtendDays = 30
)

// repeatPressGap: "+30 days" and "issue a key" add something on every
// press, and the updates of one chat run one after another, so a double
// press would do it twice. The same button within this time is skipped.
const repeatPressGap = 10 * time.Second

// errPaymentNotFound: a refund button points at a payment that is gone.
var errPaymentNotFound = errors.New("bot: payment not found")

// admin handles admin buttons. The role is checked in the DB on every
// press, so a forged button from a non-admin does nothing.
func (s *router) admin(ctx context.Context, cq *tgbot.CallbackQuery) error {
	u, err := s.users.User(ctx, cq.SenderID())
	if err != nil && !errors.Is(err, service.ErrNotFound) {
		return err
	}
	if u == nil || u.Role != service.RoleAdmin {
		log.Printf("bot: admin button from non-admin %d ignored", cq.SenderID())
		return nil
	}
	name, arg, _ := strings.Cut(strings.TrimPrefix(cq.Data, cbAdmin), ":")
	a := adminAction{
		ChatID:    cq.ChatID(),
		MessageID: cq.MessageID(),
		Name:      name,
		Arg:       arg,
	}
	if (name == actExtend || name == actIssueDays) && !s.adminRepeats.allow(cq.Data) {
		outMessage := outMessage{
			ChatID: a.ChatID,
			Text:   repeatedPressText,
		}
		return s.send.Send(ctx, outMessage)
	}
	switch name {
	case actUsers:
		return s.adminUsers(ctx, a)
	case actUser:
		return s.adminUser(ctx, a)
	case actDisable, actEnable, actExtend:
		return s.adminKeyAction(ctx, a)
	case actConfig:
		return s.adminConfig(ctx, a)
	case actDelete:
		return s.adminDeleteAsk(ctx, a)
	case actDeleteOK:
		return s.adminDelete(ctx, a)
	case actIssue:
		return s.adminIssueTerm(ctx, a)
	case actIssueDays:
		return s.adminIssue(ctx, a)
	case actFeedback:
		return s.adminFeedback(ctx, a)
	case actStats:
		return s.adminStats(ctx, a)
	case actBroadcast:
		return s.adminBroadcastAsk(ctx, a)
	case actBroadcastOK:
		return s.adminBroadcast(ctx, a)
	case actCancel:
		return s.adminCancel(ctx, a)
	case actConfigs:
		return s.adminConfigsAsk(ctx, a)
	case actConfigsOK:
		return s.adminConfigs(ctx, a)
	case actMaint:
		return s.adminMaintenance(ctx, a)
	case actRefund:
		return s.adminRefundAsk(ctx, a)
	case actRefundOK:
		return s.adminRefund(ctx, a)
	}
	log.Printf("bot: unknown admin action %q", name)
	return nil
}

// adminText handles a text an admin sent after "📣 Рассылка": the
// broadcast text. A new text at a preview replaces it and keeps what the
// preview does to maintenance (own wording for the same switch). Any other
// text, text at the "update configs" question, text from someone who is
// not an admin, or an old prompt (pendingTTL) is ignored. A message without text (a photo, a sticker)
// gets "send text" and the bot keeps waiting.
func (s *router) adminText(ctx context.Context, m *tgbot.Message) error {
	p, waiting := s.dialogs.peek(m.Chat.ID)
	if !waiting || p.Kind == readyConfigs {
		return nil
	}
	u, err := s.users.User(ctx, m.From.ID)
	if err != nil || u.Role != service.RoleAdmin {
		return nil //nolint:nilerr // not an admin (any more): ignore the text
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		outMessage := outMessage{
			ChatID: m.Chat.ID,
			Text:   needTextText,
		}
		return s.send.Send(ctx, outMessage)
	}
	pendingInput := pendingInput{
		ChatID: m.Chat.ID,
		Text:   text,
		Maint:  p.Maint,
	}
	return s.adminBroadcastPreview(ctx, pendingInput)
}

// adminUsers shows one page of users: plain users, then unlimited, then
// admins; newest first inside a role.
func (s *router) adminUsers(ctx context.Context, a adminAction) error {
	page, _ := strconv.ParseInt(a.Arg, 10, 64)
	page = max(page, 0)
	servicePage := service.Page{
		Skip:  page * adminPageSize,
		Limit: adminPageSize,
	}
	users, total, err := s.users.Users(ctx, servicePage)
	if err != nil {
		return err
	}
	v := usersView{
		Users: users,
		Total: total,
		Page:  page,
	}
	editMessage := editMessage{
		ChatID:    a.ChatID,
		MessageID: a.MessageID,
		Text:      usersText(v),
		Keyboard:  usersKeyboard(v),
	}
	return s.send.Edit(ctx, editMessage)
}

// adminUser shows a user card: role, trial, and every key with actions.
func (s *router) adminUser(ctx context.Context, a adminAction) error {
	id, err := strconv.ParseInt(a.Arg, 10, 64)
	if err != nil {
		return fmt.Errorf("bot: bad user id in button: %w", err)
	}
	u, err := s.users.User(ctx, id)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	keys, err := s.keys.Access(ctx, id)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	payments, err := s.billing.Payments(ctx, id)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	cardView := cardView{
		UserID:   id,
		Keys:     keys,
		Payments: payments,
	}
	editMessage := editMessage{
		ChatID:    a.ChatID,
		MessageID: a.MessageID,
		Text:      userCardText(u) + keysText(keys) + paymentsText(payments),
		Keyboard:  userCardKeyboard(cardView),
	}
	return s.send.Edit(ctx, editMessage)
}

// reportError explains a failed admin action in the chat. An unexpected
// error is also returned, for the log; an expected one is not.
func (s *router) reportError(ctx context.Context, e errorReport) error {
	text, known := adminErrorText(e.Err)
	userError := userError{
		ChatID: e.ChatID,
		Err:    e.Err,
		Text:   text,
		Known:  known,
	}
	return s.replyError(ctx, userError)
}

// adminKeyAction disables, enables or extends a key, then redraws the
// owner's card.
func (s *router) adminKeyAction(ctx context.Context, a adminAction) error {
	var err error
	switch a.Name {
	case actDisable:
		err = s.keys.Disable(ctx, a.Arg)
	case actEnable:
		err = s.keys.Enable(ctx, a.Arg)
	case actExtend:
		extendInput := service.ExtendInput{
			PublicKey: a.Arg,
			Days:      adminExtendDays,
		}
		_, err = s.keys.Extend(ctx, extendInput)
	default:
		return fmt.Errorf("bot: unknown key action %q", a.Name)
	}
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	return s.adminOwnerCard(ctx, a)
}

// adminOwnerCard redraws the card of the user who owns key a.Arg.
func (s *router) adminOwnerCard(ctx context.Context, a adminAction) error {
	p, err := s.keys.Key(ctx, a.Arg)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	a.Arg = strconv.FormatInt(p.UserID, 10)
	return s.adminUser(ctx, a)
}

// adminConfig sends a key's config and QR code to the admin.
func (s *router) adminConfig(ctx context.Context, a adminAction) error {
	p, err := s.keys.Key(ctx, a.Arg)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	conf, err := s.keys.ClientConfig(ctx, a.Arg)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	configDelivery := configDelivery{
		ChatID: a.ChatID,
		Key: &service.KeyConfig{
			Peer: p,
			Conf: conf,
		},
	}
	return s.sendConfig(ctx, configDelivery)
}

// adminDeleteAsk asks to confirm deleting a key.
func (s *router) adminDeleteAsk(ctx context.Context, a adminAction) error {
	p, err := s.keys.Key(ctx, a.Arg)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	editMessage := editMessage{
		ChatID:    a.ChatID,
		MessageID: a.MessageID,
		Text:      deleteConfirmText(p),
		Keyboard:  deleteConfirmKeyboard(p),
	}
	return s.send.Edit(ctx, editMessage)
}

// adminDelete deletes a key for good and shows the owner's card.
func (s *router) adminDelete(ctx context.Context, a adminAction) error {
	p, err := s.keys.Key(ctx, a.Arg)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	err = s.keys.Delete(ctx, a.Arg)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	a.Arg = strconv.FormatInt(p.UserID, 10)
	return s.adminUser(ctx, a)
}

// adminIssueTerm asks for the term of a key issued to a user by hand.
func (s *router) adminIssueTerm(ctx context.Context, a adminAction) error {
	id, err := strconv.ParseInt(a.Arg, 10, 64)
	if err != nil {
		return fmt.Errorf("bot: bad user id in button: %w", err)
	}
	editMessage := editMessage{
		ChatID:    a.ChatID,
		MessageID: a.MessageID,
		Text:      issueTermText,
		Keyboard:  issueTermKeyboard(id),
	}
	return s.send.Edit(ctx, editMessage)
}

// adminIssue issues a key to a bot user without payment, trial or limits.
// The key goes straight to the user; if they blocked the bot, to the admin.
func (s *router) adminIssue(ctx context.Context, a adminAction) error {
	idText, daysText, _ := strings.Cut(a.Arg, ":")
	id, err1 := strconv.ParseInt(idText, 10, 64)
	days, err2 := strconv.Atoi(daysText)
	if err1 != nil || err2 != nil || id <= 0 {
		return fmt.Errorf("bot: bad issue button %q", a.Arg)
	}
	issueInput := service.IssueInput{
		UserID: id,
		Days:   days,
	}
	p, err := s.keys.Issue(ctx, issueInput)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	toUser := keyDelivery{
		ChatID: id,
		Peer:   p,
	}
	err = s.deliverKey(ctx, toUser)
	if err != nil {
		log.Printf("bot: deliver key to %d: %v", id, err)
		toAdmin := keyDelivery{
			ChatID: a.ChatID,
			Peer:   p,
		}
		err = s.deliverKey(ctx, toAdmin)
		if err != nil {
			return err
		}
	}
	a.Arg = idText
	return s.adminUser(ctx, a)
}

// adminFeedback shows one page of reviews and suggestions, newest first.
func (s *router) adminFeedback(ctx context.Context, a adminAction) error {
	page, _ := strconv.ParseInt(a.Arg, 10, 64)
	page = max(page, 0)
	servicePage := service.Page{
		Skip:  page * adminPageSize,
		Limit: adminPageSize,
	}
	list, total, err := s.feedback.Feedbacks(ctx, servicePage)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	v := feedbackView{
		List:  list,
		Total: total,
		Page:  page,
	}
	editMessage := editMessage{
		ChatID:    a.ChatID,
		MessageID: a.MessageID,
		Text:      feedbackListText(v),
		Keyboard:  feedbackKeyboard(v),
	}
	return s.send.Edit(ctx, editMessage)
}

// adminStats sends the overview as a new message, so the menu stays.
func (s *router) adminStats(ctx context.Context, a adminAction) error {
	st, err := s.ops.Stats(ctx)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	outMessage := outMessage{
		ChatID:   a.ChatID,
		Text:     statsText(st),
		Keyboard: menuKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}

// adminCancel drops what the bot waits for from this admin.
func (s *router) adminCancel(ctx context.Context, a adminAction) error {
	s.dialogs.drop(a.ChatID)
	outMessage := outMessage{
		ChatID: a.ChatID,
		Text:   cancelledText,
	}
	return s.send.Send(ctx, outMessage)
}

// adminRefundAsk asks to confirm returning the Stars of a payment.
func (s *router) adminRefundAsk(ctx context.Context, a adminAction) error {
	ref, err := parsePaymentRef(a.Arg)
	if err != nil {
		return err
	}
	p, err := s.findPayment(ctx, ref)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	editMessage := editMessage{
		ChatID:    a.ChatID,
		MessageID: a.MessageID,
		Text:      refundConfirmText(p),
		Keyboard:  refundConfirmKeyboard(ref),
	}
	return s.send.Edit(ctx, editMessage)
}

// parsePaymentRef reads "<user ID>:<payRef>".
func parsePaymentRef(arg string) (paymentRef, error) {
	idText, ref, _ := strings.Cut(arg, ":")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || ref == "" {
		return paymentRef{}, fmt.Errorf("bot: bad payment button %q", arg)
	}
	return paymentRef{
		UserID: id,
		Ref:    ref,
	}, nil
}

// findPayment finds a user's payment by its short ref.
func (s *router) findPayment(ctx context.Context, ref paymentRef) (*service.Payment, error) {
	ps, err := s.billing.Payments(ctx, ref.UserID)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if payRef(p.ChargeID) == ref.Ref {
			return p, nil
		}
	}
	return nil, errPaymentNotFound
}

// payRef is a short, stable name for a charge ID (callback data is
// limited to 64 bytes).
func payRef(chargeID string) string {
	sum := sha256.Sum256([]byte(chargeID))
	return hex.EncodeToString(sum[:8])
}

// adminRefund returns the Stars, records it, tells the user and redraws
// the card. The key is left as is: the admin disables it separately if
// needed. An already refunded payment is not refunded again; when only its
// record was missing (Telegram says "already returned"), the record is
// written and the user, who was told the first time, is not told again.
func (s *router) adminRefund(ctx context.Context, a adminAction) error {
	ref, err := parsePaymentRef(a.Arg)
	if err != nil {
		return err
	}
	p, err := s.findPayment(ctx, ref)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	a.Arg = strconv.FormatInt(ref.UserID, 10)
	if !p.RefundedAt.IsZero() {
		return s.adminUser(ctx, a)
	}
	if !s.refunds.start(p.ChargeID) {
		return nil // a double press: the first one is refunding it
	}
	defer s.refunds.end(p.ChargeID)
	// Once the Stars go back, recording it must finish even on shutdown.
	ctx = context.WithoutCancel(ctx)
	refundInput := refundInput{
		UserID:   p.UserID,
		ChargeID: p.ChargeID,
	}
	res, err := s.returnStars(ctx, refundInput)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	if res.RecordErr != nil {
		outMessage := outMessage{
			ChatID: a.ChatID,
			Text:   refundNotRecordedText(p),
		}
		err = s.send.Send(ctx, outMessage)
		if err != nil {
			log.Printf("bot: %v", err)
		}
	}
	if !res.Already {
		outMessage := outMessage{
			ChatID: p.UserID,
			Text:   refundedToUserText(p),
		}
		err = s.send.Send(ctx, outMessage)
		if err != nil {
			log.Printf("bot: tell user %d about refund: %v", p.UserID, err)
		}
	}
	return s.adminUser(ctx, a)
}
