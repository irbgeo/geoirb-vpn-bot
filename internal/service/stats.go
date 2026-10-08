package service

import (
	"context"
	"slices"
	"time"
)

const (
	statsExpiringWindow = 7 * 24 * time.Hour
	statsRevenueDays    = 30
	statsTopTraffic     = 10
)

// Stats builds the admin overview: users, keys by state, online now,
// subnet use, 30-day revenue and the heaviest keys by traffic. Traffic
// counters restart when the VPN server restarts.
func (s *Service) Stats(ctx context.Context) (*Stats, error) {
	page := Page{
		Limit: 1,
	}
	_, users, err := s.users.List(ctx, page)
	if err != nil {
		return nil, err
	}
	ps, err := s.peers.ByServer(ctx, s.cfg.ServerID)
	if err != nil {
		return nil, err
	}
	keys, err := s.withStats(ctx, ps)
	if err != nil {
		return nil, err
	}
	st := &Stats{
		Users: users,
	}
	now := s.now()
	for _, k := range keys {
		if !k.Peer.Enabled {
			st.Disabled++
			continue
		}
		st.Active++
		if !k.Peer.ExpiresAt.IsZero() && k.Peer.ExpiresAt.Sub(now) <= statsExpiringWindow {
			st.Expiring7d++
		}
		if k.Online {
			st.Online++
		}
	}
	st.SubnetUsed, st.SubnetTotal, err = s.subnetUsage(ctx)
	if err != nil {
		return nil, err
	}
	err = s.addRevenue(ctx, st)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(keys, func(a, b KeyInfo) int {
		return int((b.Sent + b.Received) - (a.Sent + a.Received))
	})
	st.TopTraffic = keys[:min(statsTopTraffic, len(keys))]
	return st, nil
}

// BroadcastRecipients returns every Telegram user with an enabled key, once.
func (s *Service) BroadcastRecipients(ctx context.Context) ([]int64, error) {
	ps, err := s.peers.ByServer(ctx, s.cfg.ServerID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, p := range ps {
		if p.Enabled && p.UserID != 0 && !slices.Contains(ids, p.UserID) {
			ids = append(ids, p.UserID)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// addRevenue sums applied, not refunded payments of the last 30 days.
func (s *Service) addRevenue(ctx context.Context, st *Stats) error {
	ps, err := s.payments.Since(ctx, s.now().AddDate(0, 0, -statsRevenueDays))
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.Applied && p.RefundedAt.IsZero() {
			st.Revenue30d += p.Stars
			st.Payments30d++
		}
	}
	return nil
}
