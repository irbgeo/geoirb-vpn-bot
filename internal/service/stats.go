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
func (s *service) Stats(ctx context.Context) (*Stats, error) {
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
	now := time.Now()
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

// BroadcastRecipients returns every Telegram user with an enabled key,
// once; a role keeps only the users of that role ("" = everyone).
func (s *service) BroadcastRecipients(ctx context.Context, role Role) ([]int64, error) {
	ps, err := s.peers.ByServer(ctx, s.cfg.ServerID)
	if err != nil {
		return nil, err
	}
	var ofRole map[int64]bool
	if role != "" {
		ofRole, err = s.roleIDs(ctx, role)
		if err != nil {
			return nil, err
		}
	}
	var ids []int64
	for _, p := range ps {
		if !p.Enabled || p.UserID == 0 || slices.Contains(ids, p.UserID) {
			continue
		}
		if ofRole != nil && !ofRole[p.UserID] {
			continue
		}
		ids = append(ids, p.UserID)
	}
	slices.Sort(ids)
	return ids, nil
}

// roleIDs is the set of IDs of the users with role r.
func (s *service) roleIDs(ctx context.Context, r Role) (map[int64]bool, error) {
	us, err := s.users.ByRole(ctx, r)
	if err != nil {
		return nil, err
	}
	ids := make(map[int64]bool, len(us))
	for _, u := range us {
		ids[u.ID] = true
	}
	return ids, nil
}

// addRevenue sums applied, not refunded payments of the last 30 days.
func (s *service) addRevenue(ctx context.Context, st *Stats) error {
	ps, err := s.payments.Since(ctx, time.Now().AddDate(0, 0, -statsRevenueDays))
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
