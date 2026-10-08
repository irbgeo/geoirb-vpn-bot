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
	actBroadcastOK = "bcok"   // sends the previewed broadcast
	actCancel      = "cancel" // drops what the bot waits for (broadcast text)
	actFeedback    = "fb"     // + page (from 0): reviews and suggestions
	actConfigs     = "cfgs"   // asks to send every user a fresh config
	actConfigsOK   = "cfgsok" // confirmed: send them
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

// errPaymentNotFound: a refund button points at a payment that is gone.
var errPaymentNotFound = errors.New("bot: payment not found")

// admin handles admin buttons. The role is checked in the DB on every
// press, so a forged button from a non-admin does nothing.
func (s *Router) admin(ctx context.Context, cq *tgbot.CallbackQuery) error {
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

// adminIssueTerm asks for the term of a key issued to a user by hand.
func (s *Router) adminIssueTerm(ctx context.Context, a adminAction) error {
	id, err := strconv.ParseInt(a.Arg, 10, 64)
	if err != nil {
		return fmt.Errorf("bot: bad user id in button: %w", err)
	}
	return s.send.Edit(
		ctx,
		EditMessage{
			ChatID:    a.ChatID,
			MessageID: a.MessageID,
			Text:      issueTermText,
			Keyboard:  issueTermKeyboard(id),
		},
	)
}

// adminIssue issues a key to a bot user without payment, trial or limits.
// The key goes straight to the user; if they blocked the bot, to the admin.
func (s *Router) adminIssue(ctx context.Context, a adminAction) error {
	idText, daysText, _ := strings.Cut(a.Arg, ":")
	id, err1 := strconv.ParseInt(idText, 10, 64)
	days, err2 := strconv.Atoi(daysText)
	if err1 != nil || err2 != nil || id <= 0 {
		return fmt.Errorf("bot: bad issue button %q", a.Arg)
	}
	p, err := s.keys.Issue(
		ctx,
		service.IssueInput{
			UserID: id,
			Days:   days,
		},
	)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	if err := s.deliverKey(
		ctx,
		keyDelivery{
			ChatID: id,
			Peer:   p,
		},
	); err != nil {
		log.Printf("bot: deliver key to %d: %v", id, err)
		if err := s.deliverKey(
			ctx,
			keyDelivery{
				ChatID: a.ChatID,
				Peer:   p,
			},
		); err != nil {
			return err
		}
	}
	a.Arg = idText
	return s.adminUser(ctx, a)
}

// adminText handles a text an admin sent after "📣 Рассылка": the
// broadcast text. A new text at a preview replaces it and keeps what the
// preview does to maintenance (own wording for the same switch). Any other
// text, text from someone who is not an admin, or an old prompt
// (pendingTTL) is ignored. A message without text (a photo, a sticker)
// gets "send text" and the bot keeps waiting.
func (s *Router) adminText(ctx context.Context, m *tgbot.Message) error {
	p, waiting := s.dialogs.peek(m.Chat.ID)
	if !waiting {
		return nil
	}
	u, err := s.users.User(ctx, m.From.ID)
	if err != nil || u.Role != service.RoleAdmin {
		return nil //nolint:nilerr // not an admin (any more): ignore the text
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return s.send.Send(
			ctx,
			OutMessage{
				ChatID: m.Chat.ID,
				Text:   needTextText,
			},
		)
	}
	return s.adminBroadcastPreview(
		ctx,
		pendingInput{
			ChatID: m.Chat.ID,
			Text:   text,
			Maint:  p.Maint,
		},
	)
}

// adminCancel drops what the bot waits for from this admin.
func (s *Router) adminCancel(ctx context.Context, a adminAction) error {
	s.dialogs.drop(a.ChatID)
	return s.send.Send(
		ctx,
		OutMessage{
			ChatID: a.ChatID,
			Text:   cancelledText,
		},
	)
}

// adminStats sends the overview as a new message, so the menu stays.
func (s *Router) adminStats(ctx context.Context, a adminAction) error {
	st, err := s.ops.Stats(ctx)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	return s.send.Send(
		ctx,
		OutMessage{
			ChatID:   a.ChatID,
			Text:     statsText(st),
			Keyboard: menuKeyboard(),
		},
	)
}

// adminUsers shows one page of users: plain users, then unlimited, then
// admins; newest first inside a role.
func (s *Router) adminUsers(ctx context.Context, a adminAction) error {
	page, _ := strconv.ParseInt(a.Arg, 10, 64)
	page = max(page, 0)
	users, total, err := s.users.Users(
		ctx,
		service.Page{
			Skip:  page * adminPageSize,
			Limit: adminPageSize,
		},
	)
	if err != nil {
		return err
	}
	v := usersView{
		Users: users,
		Total: total,
		Page:  page,
	}
	return s.send.Edit(
		ctx,
		EditMessage{
			ChatID:    a.ChatID,
			MessageID: a.MessageID,
			Text:      usersText(v),
			Keyboard:  usersKeyboard(v),
		},
	)
}

// adminFeedback shows one page of reviews and suggestions, newest first.
func (s *Router) adminFeedback(ctx context.Context, a adminAction) error {
	page, _ := strconv.ParseInt(a.Arg, 10, 64)
	page = max(page, 0)
	list, total, err := s.feedback.Feedbacks(
		ctx,
		service.Page{
			Skip:  page * adminPageSize,
			Limit: adminPageSize,
		},
	)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	v := feedbackView{
		List:  list,
		Total: total,
		Page:  page,
	}
	return s.send.Edit(
		ctx,
		EditMessage{
			ChatID:    a.ChatID,
			MessageID: a.MessageID,
			Text:      feedbackListText(v),
			Keyboard:  feedbackKeyboard(v),
		},
	)
}

// adminUser shows a user card: role, trial, and every key with actions.
func (s *Router) adminUser(ctx context.Context, a adminAction) error {
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
	return s.send.Edit(
		ctx,
		EditMessage{
			ChatID:    a.ChatID,
			MessageID: a.MessageID,
			Text:      userCardText(u) + keysText(keys) + paymentsText(payments),
			Keyboard: userCardKeyboard(
				cardView{
					UserID:   id,
					Keys:     keys,
					Payments: payments,
				},
			),
		},
	)
}

// adminKeyAction disables, enables or extends a key, then redraws the
// owner's card.
func (s *Router) adminKeyAction(ctx context.Context, a adminAction) error {
	var err error
	switch a.Name {
	case actDisable:
		err = s.keys.Disable(ctx, a.Arg)
	case actEnable:
		err = s.keys.Enable(ctx, a.Arg)
	case actExtend:
		_, err = s.keys.Extend(
			ctx,
			service.ExtendInput{
				PublicKey: a.Arg,
				Days:      adminExtendDays,
			},
		)
	default:
		return fmt.Errorf("bot: unknown key action %q", a.Name)
	}
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	return s.adminOwnerCard(ctx, a)
}

// adminConfig sends a key's config and QR code to the admin.
func (s *Router) adminConfig(ctx context.Context, a adminAction) error {
	p, err := s.keys.Key(ctx, a.Arg)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	conf, err := s.keys.ClientConfig(ctx, a.Arg)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	return s.sendConfig(
		ctx,
		configDelivery{
			ChatID: a.ChatID,
			Key: &service.KeyConfig{
				Peer: p,
				Conf: conf,
			},
		},
	)
}

// adminDeleteAsk asks to confirm deleting a key.
func (s *Router) adminDeleteAsk(ctx context.Context, a adminAction) error {
	p, err := s.keys.Key(ctx, a.Arg)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	return s.send.Edit(
		ctx,
		EditMessage{
			ChatID:    a.ChatID,
			MessageID: a.MessageID,
			Text:      deleteConfirmText(p),
			Keyboard:  deleteConfirmKeyboard(p),
		},
	)
}

// adminDelete deletes a key for good and shows the owner's card.
func (s *Router) adminDelete(ctx context.Context, a adminAction) error {
	p, err := s.keys.Key(ctx, a.Arg)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	if err := s.keys.Delete(ctx, a.Arg); err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	a.Arg = strconv.FormatInt(p.UserID, 10)
	return s.adminUser(ctx, a)
}

// adminRefundAsk asks to confirm returning the Stars of a payment.
func (s *Router) adminRefundAsk(ctx context.Context, a adminAction) error {
	ref, err := parsePaymentRef(a.Arg)
	if err != nil {
		return err
	}
	p, err := s.findPayment(ctx, ref)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	return s.send.Edit(
		ctx,
		EditMessage{
			ChatID:    a.ChatID,
			MessageID: a.MessageID,
			Text:      refundConfirmText(p),
			Keyboard:  refundConfirmKeyboard(ref),
		},
	)
}

// adminRefund returns the Stars, records it, tells the user and redraws
// the card. The key is left as is: the admin disables it separately if
// needed. An already refunded payment is not refunded again.
func (s *Router) adminRefund(ctx context.Context, a adminAction) error {
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
	if err := s.send.Refund(
		ctx,
		RefundInput{
			UserID:   p.UserID,
			ChargeID: p.ChargeID,
		},
	); err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	if err := s.billing.MarkRefunded(ctx, p.ChargeID); err != nil {
		log.Printf("bot: stars returned but not recorded for %s: %v", p.ChargeID, err)
		if err := s.send.Send(
			ctx,
			OutMessage{
				ChatID: a.ChatID,
				Text:   refundNotRecordedText(p),
			},
		); err != nil {
			log.Printf("bot: %v", err)
		}
	}
	if err := s.send.Send(
		ctx,
		OutMessage{
			ChatID: p.UserID,
			Text:   refundedToUserText(p),
		},
	); err != nil {
		log.Printf("bot: tell user %d about refund: %v", p.UserID, err)
	}
	return s.adminUser(ctx, a)
}

// findPayment finds a user's payment by its short ref.
func (s *Router) findPayment(ctx context.Context, ref paymentRef) (*service.Payment, error) {
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

// adminOwnerCard redraws the card of the user who owns key a.Arg.
func (s *Router) adminOwnerCard(ctx context.Context, a adminAction) error {
	p, err := s.keys.Key(ctx, a.Arg)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	a.Arg = strconv.FormatInt(p.UserID, 10)
	return s.adminUser(ctx, a)
}

// reportError explains a failed admin action in the chat. An unexpected
// error is also returned, for the log; an expected one is not.
func (s *Router) reportError(ctx context.Context, e errorReport) error {
	text, known := adminErrorText(e.Err)
	return s.replyError(
		ctx,
		userError{
			ChatID: e.ChatID,
			Err:    e.Err,
			Text:   text,
			Known:  known,
		},
	)
}

// payRef is a short, stable name for a charge ID (callback data is
// limited to 64 bytes).
func payRef(chargeID string) string {
	sum := sha256.Sum256([]byte(chargeID))
	return hex.EncodeToString(sum[:8])
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
