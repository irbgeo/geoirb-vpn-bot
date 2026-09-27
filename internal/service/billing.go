package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
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
func (s *Service) Tariffs() []Tariff {
	return s.cfg.Tariffs
}

// Invoice checks a purchase and prices it. Only RoleUser buys access:
// unlimited and admin keys never expire.
func (s *Service) Invoice(ctx context.Context, in PurchaseInput) (*Invoice, error) {
	t, ok := s.tariff(in.Days)
	if !ok {
		return nil, ErrNoTariff
	}
	if err := s.checkBuyer(ctx, in); err != nil {
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
func (s *Service) CheckPurchase(ctx context.Context, in PaymentInput) error {
	_, err := s.purchase(ctx, in)
	return err
}

// Pay applies a successful payment once per charge ID. Telegram delivers
// it once (there is no retry), so the whole step runs under s.mu and in
// this order:
//   - a charge already recorded is looked up first: applied → Repeat;
//     refunded → ErrAlreadyRefunded. Neither is checked again, so a later
//     price or role change can't turn a repeat into a refund;
//   - a new charge is checked, recorded unapplied, the days added (or a
//     key issued), then marked applied.
//
// On error nothing was bought and the caller refunds. A crash in the
// middle leaves an unapplied record: UnfinishedPayments finds it.
func (s *Service) Pay(ctx context.Context, in PaymentInput) (*PayResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pay, err := s.payments.Get(ctx, in.ChargeID)
	if err != nil {
		return nil, err
	}
	switch {
	case pay != nil && pay.Applied:
		// A repeat must never turn into a refund, so a failed lookup of the
		// key (only shown, not needed) is logged, not returned.
		p, err := s.peers.Get(ctx, pay.PeerKey)
		if err != nil {
			log.Printf("service: repeat of payment %s: key lookup: %v", in.ChargeID, err)
		}
		return &PayResult{
			Peer:   p,
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
	if pay == nil {
		pay = &Payment{
			ChargeID:  in.ChargeID,
			UserID:    pu.UserID,
			Stars:     in.Stars,
			Days:      pu.Days,
			CreatedAt: s.now(),
		}
		if _, err := s.payments.Add(ctx, pay); err != nil {
			return nil, err
		}
	}

	res, err := s.applyPurchase(ctx, pu)
	if err != nil {
		return nil, err
	}
	res.Days = pu.Days
	pay.Applied = true
	pay.PeerKey = res.Peer.PublicKey
	if err := s.payments.Save(ctx, pay); err != nil {
		// The days are given: no refund. One more try on a context that a
		// shutdown can't cancel; if that fails too, the record stays
		// unapplied and UnfinishedPayments shows it to the admins.
		if err := s.payments.Save(context.WithoutCancel(ctx), pay); err != nil {
			log.Printf("service: payment %s applied but not marked: %v", in.ChargeID, err)
		}
	}
	return res, nil
}

// UnfinishedPayments returns recent payments that were neither applied
// nor refunded (the bot stopped in the middle). Admins see them at
// startup and refund them from the user card.
func (s *Service) UnfinishedPayments(ctx context.Context) ([]*Payment, error) {
	ps, err := s.payments.Since(ctx, s.now().AddDate(0, 0, -unfinishedLookbackDays))
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
func (s *Service) Payments(ctx context.Context, userID int64) ([]*Payment, error) {
	return s.payments.ByUser(ctx, userID)
}

// MarkRefunded records that the Stars of a charge were returned. A charge
// that never got a record (refused before saving) is fine.
func (s *Service) MarkRefunded(ctx context.Context, chargeID string) error {
	p, err := s.payments.Get(ctx, chargeID)
	if err != nil || p == nil {
		return err
	}
	p.RefundedAt = s.now()
	return s.payments.Save(ctx, p)
}

func (s *Service) tariff(days int) (Tariff, bool) {
	for _, t := range s.cfg.Tariffs {
		if t.Days == days {
			return t, true
		}
	}
	return Tariff{}, false
}

// checkBuyer: the user exists, is a RoleUser, and owns the chosen key.
func (s *Service) checkBuyer(ctx context.Context, in PurchaseInput) error {
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
		keys, err := s.peers.ByUser(ctx, in.UserID)
		if err != nil {
			return err
		}
		if len(keys) > 0 && firstTimed(keys) == nil {
			return ErrNotForSale
		}
		return nil
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
	return nil
}

// firstTimed returns the key with the lowest IP among those that can
// end, or nil: a key that never ends can't be extended.
func firstTimed(ps []*Peer) *Peer {
	var first *Peer
	for _, p := range ps {
		if p.ExpiresAt.IsZero() {
			continue
		}
		if first == nil || parseIP(p.IP).Less(parseIP(first.IP)) {
			first = p
		}
	}
	return first
}

// purchase parses and checks an invoice payload against the payment.
func (s *Service) purchase(ctx context.Context, in PaymentInput) (*PurchaseInput, error) {
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
	if t, ok := s.tariff(days); !ok || t.Stars != stars || stars != in.Stars {
		return nil, ErrPriceChanged
	}
	pu := &PurchaseInput{
		UserID:    userID,
		Days:      days,
		PublicKey: f[4],
	}
	if err := s.checkBuyer(ctx, *pu); err != nil {
		return nil, err
	}
	return pu, nil
}

// applyPurchase adds the paid days: to the chosen key, else to the user's
// first key, else to a new key. Buying ends any chance of a trial. The
// caller holds s.mu.
func (s *Service) applyPurchase(ctx context.Context, pu *PurchaseInput) (*PayResult, error) {
	key := pu.PublicKey
	if key == "" {
		keys, err := s.peers.ByUser(ctx, pu.UserID)
		if err != nil {
			return nil, err
		}
		if p := firstTimed(keys); p != nil {
			key = p.PublicKey
		}
	}

	res := &PayResult{}
	var err error
	if key != "" {
		res.Peer, err = s.extend(
			ctx,
			ExtendInput{
				PublicKey: key,
				Days:      pu.Days,
			},
		)
	} else {
		res.NewKey = true
		res.Peer, err = s.issue(
			ctx,
			IssueInput{
				UserID: pu.UserID,
				Days:   pu.Days,
			},
		)
	}
	if err != nil {
		return nil, fmt.Errorf("service: apply purchase: %w", err)
	}
	s.markTrialUsed(ctx, pu.UserID)
	return res, nil
}

// markTrialUsed: after a purchase there is no free trial any more. A
// failure only means a later trial check still sees the key.
func (s *Service) markTrialUsed(ctx context.Context, userID int64) {
	if err := s.users.SetTrialUsed(ctx, userID); err != nil {
		log.Printf("service: mark trial used for %d: %v", userID, err)
	}
}
