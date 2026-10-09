package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNoTariff     = errors.New("service: no such tariff")
	ErrNotForSale   = errors.New("service: this role does not buy access")
	ErrBadPayload   = errors.New("service: unknown invoice payload")
	ErrWrongPayer   = errors.New("service: invoice was made for another user")
	ErrPriceChanged = errors.New("service: tariff or price changed since the invoice")
	// ErrAlreadyRefunded: this charge was refunded; it is not applied now.
	ErrAlreadyRefunded = errors.New("service: payment was already refunded")
)

// unfinishedLookbackDays: how far back UnfinishedPayments looks.
const unfinishedLookbackDays = 30

// payloadVersion starts every invoice payload: "v1|user|days|stars|key".
const payloadVersion = "v1"

// Tariffs lists what a RoleUser can buy.
func (s *service) Tariffs() []Tariff {
	return s.cfg.Tariffs
}

// Invoice checks a purchase and prices it. Only RoleUser buys access:
// unlimited and admin keys never expire.
func (s *service) Invoice(ctx context.Context, in PurchaseInput) (*Invoice, error) {
	t, ok := s.tariff(in.Days)
	if !ok {
		return nil, ErrNoTariff
	}
	err := s.checkBuyer(ctx, in)
	if err != nil {
		return nil, err
	}
	return &Invoice{
		Days:  t.Days,
		Stars: t.Stars,
		Payload: strings.Join(
			[]string{
				payloadVersion,
				strconv.FormatInt(in.UserID, 10),
				strconv.Itoa(t.Days),
				strconv.Itoa(t.Stars),
				in.PublicKey,
			},
			"|",
		),
	}, nil
}

// CheckPurchase answers Telegram's pre-checkout query: the payer is the
// user the invoice was made for, the tariff and price still hold, and the
// role still buys. A failed check means Telegram charges nothing.
func (s *service) CheckPurchase(ctx context.Context, in PaymentInput) error {
	_, err := s.purchase(ctx, in)
	return err
}

// Pay applies a successful payment once per charge ID. Telegram delivers
// it once (there is no retry), so the whole step runs under s.mu and in
// this order:
//   - a charge already recorded is looked up first: applied → Repeat;
//     refunded → ErrAlreadyRefunded. Neither is checked again, so a later
//     price or role change can't turn a repeat into a refund;
//   - a record that is neither applied nor refunded (a crash in the
//     middle) is not applied again: NeedsReview, admins check the key;
//   - a new charge is checked, recorded unapplied, the days added (or a
//     key issued), then marked applied.
//
// On error nothing was bought and the caller refunds. A crash in the
// middle leaves an unapplied record: UnfinishedPayments finds it.
func (s *service) Pay(ctx context.Context, in PaymentInput) (*PayResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pay, err := s.payments.Get(ctx, in.ChargeID)
	if err != nil {
		return nil, err
	}
	switch {
	case pay != nil && !pay.Applied && pay.RefundedAt.IsZero():
		// A record without a result: a crash in the middle may have added
		// the days already, so applying again could double them. Admins decide.
		return &PayResult{NeedsReview: true}, nil
	case pay != nil && pay.Applied:
		// A repeat must never turn into a refund, so a failed lookup of the
		// key (only shown, not needed) is logged, not returned.
		p, err := s.peers.Get(ctx, pay.PeerKey)
		if err != nil {
			log.Printf("service: repeat of payment %s: key lookup: %v", in.ChargeID, err)
		}
		return &PayResult{
			Peer:   p.public(),
			Days:   pay.Days,
			Repeat: true,
		}, nil
	case pay != nil && !pay.RefundedAt.IsZero():
		return nil, ErrAlreadyRefunded
	}

	pu, err := s.purchase(ctx, in)
	if err != nil {
		return nil, err
	}
	err = s.pickKey(ctx, pu)
	if err != nil {
		return nil, err
	}
	// Every recorded charge returned above: this one is new.
	// PeerKey is the key to extend ("" = a new key), known before the
	// days are added: if the bot stops in the middle, admins see which
	// key to check before refunding.
	pay = &Payment{
		ChargeID:  in.ChargeID,
		UserID:    pu.UserID,
		PeerKey:   pu.PublicKey,
		Stars:     in.Stars,
		Days:      pu.Days,
		CreatedAt: time.Now(),
	}
	_, err = s.payments.Add(ctx, pay)
	if err != nil {
		return nil, err
	}

	res, err := s.applyPurchase(ctx, pu)
	if err != nil {
		return nil, err
	}
	res.Days = pu.Days
	mark := PaymentMark{
		ChargeID: in.ChargeID,
		PeerKey:  res.Peer.PublicKey,
	}
	err = s.payments.MarkApplied(ctx, mark)
	if err != nil {
		// The days are given: no refund. One more try on a context that a
		// shutdown can't cancel; if that fails too, the record stays
		// unapplied and UnfinishedPayments shows it to the admins.
		err = s.payments.MarkApplied(context.WithoutCancel(ctx), mark)
		if err != nil {
			log.Printf("service: payment %s applied but not marked: %v", in.ChargeID, err)
		}
	}
	res.Peer = res.Peer.public()
	return res, nil
}

// UnfinishedPayments returns recent payments that were neither applied
// nor refunded (the bot stopped in the middle). Admins see them at
// startup and refund them from the user card.
func (s *service) UnfinishedPayments(ctx context.Context) ([]*Payment, error) {
	ps, err := s.payments.Since(ctx, time.Now().AddDate(0, 0, -unfinishedLookbackDays))
	if err != nil {
		return nil, err
	}
	out := ps[:0]
	for _, p := range ps {
		if !p.Applied && p.RefundedAt.IsZero() {
			out = append(out, p)
		}
	}
	return out, nil
}

// Payments returns a user's payments, newest first.
func (s *service) Payments(ctx context.Context, userID int64) ([]*Payment, error) {
	return s.payments.ByUser(ctx, userID)
}

// MarkRefunded records that the Stars of a charge were returned. A charge
// that never got a record (refused before saving) is fine.
func (s *service) MarkRefunded(ctx context.Context, chargeID string) error {
	paymentMark := PaymentMark{
		ChargeID: chargeID,
		At:       time.Now(),
	}
	return s.payments.MarkRefunded(ctx, paymentMark)
}

func (s *service) tariff(days int) (Tariff, bool) {
	for _, t := range s.cfg.Tariffs {
		if t.Days == days {
			return t, true
		}
	}
	return Tariff{}, false
}

// checkBuyer: the user exists, is a RoleUser, and owns the chosen key.
func (s *service) checkBuyer(ctx context.Context, in PurchaseInput) error {
	u, err := s.User(ctx, in.UserID)
	if err != nil {
		return err
	}
	if u.Role != RoleUser {
		return ErrNotForSale
	}
	if in.PublicKey == "" {
		// Buying extends one of their keys that can end; with keys but
		// none of them timed, there is nothing to pay for.
		_, err := s.chooseKey(ctx, in.UserID)
		return err
	}
	p, err := s.ourPeer(ctx, in.PublicKey)
	if err != nil {
		return err
	}
	if p.UserID != in.UserID {
		return ErrNotFound
	}
	if p.ExpiresAt.IsZero() {
		return ErrNotForSale // the key never ends
	}
	if p.Blocked {
		return ErrBlocked
	}
	return nil
}

// chooseKey picks the key a no-key purchase extends: the unblocked key
// with the lowest IP among those that can end (a key that never ends
// can't be extended). nil means the user has no keys (a new one is
// issued); ErrNotForSale: keys, none of them timed; ErrBlocked: timed
// keys exist but an admin disabled them all. Invoice, CheckPurchase and
// Pay all go through it, so they agree.
func (s *service) chooseKey(ctx context.Context, userID int64) (*Peer, error) {
	keys, err := s.peers.ByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	var first *Peer
	timed := false
	for _, p := range keys {
		if p.ExpiresAt.IsZero() {
			continue
		}
		timed = true
		if p.Blocked {
			continue
		}
		if first == nil || parseIP(p.IP).Less(parseIP(first.IP)) {
			first = p
		}
	}
	switch {
	case first != nil:
		return first, nil
	case timed:
		return nil, ErrBlocked
	case len(keys) > 0:
		return nil, ErrNotForSale
	}
	return nil, nil
}

// purchase parses and checks an invoice payload against the payment.
func (s *service) purchase(ctx context.Context, in PaymentInput) (*PurchaseInput, error) {
	f := strings.Split(in.Payload, "|")
	if len(f) != 5 || f[0] != payloadVersion {
		return nil, ErrBadPayload
	}
	userID, err1 := strconv.ParseInt(f[1], 10, 64)
	days, err2 := strconv.Atoi(f[2])
	stars, err3 := strconv.Atoi(f[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, ErrBadPayload
	}
	if userID != in.PayerID {
		return nil, ErrWrongPayer
	}
	t, ok := s.tariff(days)
	if !ok || t.Stars != stars || stars != in.Stars {
		return nil, ErrPriceChanged
	}
	pu := &PurchaseInput{
		UserID:    userID,
		Days:      days,
		PublicKey: f[4],
	}
	err := s.checkBuyer(ctx, *pu)
	if err != nil {
		return nil, err
	}
	return pu, nil
}

// pickKey fills in the key a purchase extends when the invoice named
// none: see chooseKey, or "" for a new key.
func (s *service) pickKey(ctx context.Context, pu *PurchaseInput) error {
	if pu.PublicKey != "" {
		return nil
	}
	p, err := s.chooseKey(ctx, pu.UserID)
	if err != nil {
		return err
	}
	if p != nil {
		pu.PublicKey = p.PublicKey
	}
	return nil
}

// applyPurchase adds the paid days: to the chosen key, else to the user's
// first key, else to a new key. Buying ends any chance of a trial. The
// caller holds s.mu.
func (s *service) applyPurchase(ctx context.Context, pu *PurchaseInput) (*PayResult, error) {
	key := pu.PublicKey // picked by pickKey; "" = a new key

	res := &PayResult{}
	var err error
	if key != "" {
		extendInput := ExtendInput{
			PublicKey: key,
			Days:      pu.Days,
		}
		res.Peer, err = s.extend(ctx, extendInput)
	} else {
		res.NewKey = true
		issueInput := IssueInput{
			UserID: pu.UserID,
			Days:   pu.Days,
		}
		res.Peer, err = s.issue(ctx, issueInput)
	}
	if err != nil {
		return nil, fmt.Errorf("service: apply purchase: %w", err)
	}
	s.markTrialUsed(ctx, pu.UserID)
	return res, nil
}

// markTrialUsed: after a purchase there is no free trial any more. A
// failure only means a later trial check still sees the key.
func (s *service) markTrialUsed(ctx context.Context, userID int64) {
	err := s.users.SetTrialUsed(ctx, userID)
	if err != nil {
		log.Printf("service: mark trial used for %d: %v", userID, err)
	}
}
