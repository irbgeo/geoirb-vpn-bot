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
	actBroadcastOK = "bcok"   // sends the previewed broadcast
	actCancel      = "cancel" // drops what the bot waits for (broadcast text)
	actConfigs     = "cfgs"   // asks to send every user a fresh config
	actConfigsOK   = "cfgsok" // confirmed: send them
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
	cbAdminCfgs  = cbAdmin + actConfigs
	cbAdminCfgOK = cbAdmin + actConfigsOK
	cbAdminRef   = cbAdmin + actRefund + ":"
	cbAdminRefOK = cbAdmin + actRefundOK + ":"

	adminPageSize   = 10
	adminExtendDays = 30
)

// errPaymentNotFound: a refund button points at a payment that is gone.
var errPaymentNotFound = errors.New("bot: payment not found")

// admin handles admin buttons. The role is checked in the DB on every
// press, so a forged button from a non-admin does nothing.
func (r *Router) admin(ctx context.Context, cq *tgbot.CallbackQuery) error {
	u, err := r.svc.User(ctx, cq.SenderID())
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
		return r.adminUsers(ctx, a)
	case actUser:
		return r.adminUser(ctx, a)
	case actDisable, actEnable, actExtend:
		return r.adminKeyAction(ctx, a)
	case actConfig:
		return r.adminConfig(ctx, a)
	case actDelete:
		return r.adminDeleteAsk(ctx, a)
	case actDeleteOK:
		return r.adminDelete(ctx, a)
	case actIssue:
		return r.adminIssueTerm(ctx, a)
	case actIssueDays:
		return r.adminIssue(ctx, a)
	case actStats:
		return r.adminStats(ctx, a)
	case actBroadcast:
		return r.adminBroadcastAsk(ctx, a)
	case actBroadcastOK:
		return r.adminBroadcast(ctx, a)
	case actCancel:
		return r.adminCancel(ctx, a)
	case actConfigs:
		return r.adminConfigsAsk(ctx, a)
	case actConfigsOK:
		return r.adminConfigs(ctx, a)
	case actRefund:
		return r.adminRefundAsk(ctx, a)
	case actRefundOK:
		return r.adminRefund(ctx, a)
	}
	log.Printf("bot: unknown admin action %q", name)
	return nil
}

// adminIssueTerm asks for the term of a key issued to a user by hand.
func (r *Router) adminIssueTerm(ctx context.Context, a adminAction) error {
	id, err := strconv.ParseInt(a.Arg, 10, 64)
	if err != nil {
		return fmt.Errorf("bot: bad user id in button: %w", err)
	}
	return r.send.Edit(
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
func (r *Router) adminIssue(ctx context.Context, a adminAction) error {
	idText, daysText, _ := strings.Cut(a.Arg, ":")
	id, err1 := strconv.ParseInt(idText, 10, 64)
	days, err2 := strconv.Atoi(daysText)
	if err1 != nil || err2 != nil || id <= 0 {
		return fmt.Errorf("bot: bad issue button %q", a.Arg)
	}
	p, err := r.svc.Issue(
		ctx,
		service.IssueInput{
			UserID: id,
			Days:   days,
		},
	)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	if err := r.deliverKey(
		ctx,
		keyDelivery{
			ChatID: id,
			Peer:   p,
		},
	); err != nil {
		log.Printf("bot: deliver key to %d: %v", id, err)
		if err := r.deliverKey(
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
	return r.adminUser(ctx, a)
}

// adminText handles a text an admin sent after "📣 Рассылка": the
// broadcast text. Any other text, or text from someone who is not an admin,
// is ignored; so is a prompt older than pendingTTL. A message without text
// (a photo, a sticker) gets "send text" and the bot keeps waiting; a new
// text at the preview replaces it.
func (r *Router) adminText(ctx context.Context, m *tgbot.Message) error {
	r.mu.Lock()
	p, waiting := r.pending[m.Chat.ID]
	if waiting && time.Since(p.At) > pendingTTL {
		delete(r.pending, m.Chat.ID)
		waiting = false
	}
	r.mu.Unlock()
	if !waiting {
		return nil
	}
	u, err := r.svc.User(ctx, m.From.ID)
	if err != nil || u.Role != service.RoleAdmin {
		return nil //nolint:nilerr // not an admin (any more): ignore the text
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return r.send.Send(
			ctx,
			OutMessage{
				ChatID: m.Chat.ID,
				Text:   needTextText,
			},
		)
	}
	return r.adminBroadcastPreview(
		ctx,
		OutMessage{
			ChatID: m.Chat.ID,
			Text:   text,
		},
	)
}

// adminCancel drops what the bot waits for from this admin.
func (r *Router) adminCancel(ctx context.Context, a adminAction) error {
	r.dropPending(a.ChatID)
	return r.send.Send(
		ctx,
		OutMessage{
			ChatID: a.ChatID,
			Text:   cancelledText,
		},
	)
}

// adminStats sends the overview as a new message, so the menu stays.
func (r *Router) adminStats(ctx context.Context, a adminAction) error {
	st, err := r.svc.Stats(ctx)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	return r.send.Send(
		ctx,
		OutMessage{
			ChatID: a.ChatID,
			Text:   statsText(st),
		},
	)
}

// adminUsers shows one page of users, newest first.
func (r *Router) adminUsers(ctx context.Context, a adminAction) error {
	page, _ := strconv.ParseInt(a.Arg, 10, 64)
	page = max(page, 0)
	users, total, err := r.svc.Users(
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
	return r.send.Edit(
		ctx,
		EditMessage{
			ChatID:    a.ChatID,
			MessageID: a.MessageID,
			Text:      usersText(v),
			Keyboard:  usersKeyboard(v),
		},
	)
}

// adminUser shows a user card: role, trial, and every key with actions.
func (r *Router) adminUser(ctx context.Context, a adminAction) error {
	id, err := strconv.ParseInt(a.Arg, 10, 64)
	if err != nil {
		return fmt.Errorf("bot: bad user id in button: %w", err)
	}
	u, err := r.svc.User(ctx, id)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	keys, err := r.svc.Access(ctx, id)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	payments, err := r.svc.Payments(ctx, id)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	return r.send.Edit(
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
func (r *Router) adminKeyAction(ctx context.Context, a adminAction) error {
	var err error
	switch a.Name {
	case "dis":
		err = r.svc.Disable(ctx, a.Arg)
	case "en":
		err = r.svc.Enable(ctx, a.Arg)
	case "ext":
		_, err = r.svc.Extend(
			ctx,
			service.ExtendInput{
				PublicKey: a.Arg,
				Days:      adminExtendDays,
			},
		)
	}
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	return r.adminOwnerCard(ctx, a)
}

// adminConfig sends a key's config and QR code to the admin.
func (r *Router) adminConfig(ctx context.Context, a adminAction) error {
	p, err := r.svc.Key(ctx, a.Arg)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	conf, err := r.svc.ClientConfig(ctx, a.Arg)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	return r.sendConfig(
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
func (r *Router) adminDeleteAsk(ctx context.Context, a adminAction) error {
	p, err := r.svc.Key(ctx, a.Arg)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	return r.send.Edit(
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
func (r *Router) adminDelete(ctx context.Context, a adminAction) error {
	p, err := r.svc.Key(ctx, a.Arg)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	if err := r.svc.Delete(ctx, a.Arg); err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	a.Arg = strconv.FormatInt(p.UserID, 10)
	return r.adminUser(ctx, a)
}

// adminRefundAsk asks to confirm returning the Stars of a payment.
func (r *Router) adminRefundAsk(ctx context.Context, a adminAction) error {
	ref, err := parsePaymentRef(a.Arg)
	if err != nil {
		return err
	}
	p, err := r.findPayment(ctx, ref)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	return r.send.Edit(
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
func (r *Router) adminRefund(ctx context.Context, a adminAction) error {
	ref, err := parsePaymentRef(a.Arg)
	if err != nil {
		return err
	}
	p, err := r.findPayment(ctx, ref)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	a.Arg = strconv.FormatInt(ref.UserID, 10)
	if !p.RefundedAt.IsZero() {
		return r.adminUser(ctx, a)
	}
	if !r.startRefund(p.ChargeID) {
		return nil // a double press: the first one is refunding it
	}
	defer r.endRefund(p.ChargeID)
	if err := r.send.Refund(
		ctx,
		RefundInput{
			UserID:   p.UserID,
			ChargeID: p.ChargeID,
		},
	); err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	if err := r.svc.MarkRefunded(ctx, p.ChargeID); err != nil {
		log.Printf("bot: stars returned but not recorded for %s: %v", p.ChargeID, err)
	}
	if err := r.send.Send(
		ctx,
		OutMessage{
			ChatID: p.UserID,
			Text:   refundedToUserText(p),
		},
	); err != nil {
		log.Printf("bot: tell user %d about refund: %v", p.UserID, err)
	}
	return r.adminUser(ctx, a)
}

// startRefund marks a refund as running; false if one already is.
func (r *Router) startRefund(chargeID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refunding[chargeID] {
		return false
	}
	r.refunding[chargeID] = true
	return true
}

func (r *Router) endRefund(chargeID string) {
	r.mu.Lock()
	delete(r.refunding, chargeID)
	r.mu.Unlock()
}

// findPayment finds a user's payment by its short ref.
func (r *Router) findPayment(ctx context.Context, ref paymentRef) (*service.Payment, error) {
	ps, err := r.svc.Payments(ctx, ref.UserID)
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
func (r *Router) adminOwnerCard(ctx context.Context, a adminAction) error {
	p, err := r.svc.Key(ctx, a.Arg)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	a.Arg = strconv.FormatInt(p.UserID, 10)
	return r.adminUser(ctx, a)
}

// reportError explains a failed admin action in the chat. An unexpected
// error is also returned, for the log; an expected one is not.
func (r *Router) reportError(ctx context.Context, e errorReport) error {
	text, known := adminErrorText(e.Err)
	return r.replyError(
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
