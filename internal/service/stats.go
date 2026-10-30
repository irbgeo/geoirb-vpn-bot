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
	usersQuery := UsersQuery{
		Page: Page{
			Limit: 1,
		},
	}
	_, users, err := s.users.List(ctx, usersQuery)
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

// BroadcastRecipients returns, once each, the members of a company, or
// every user of a role, with a key or not (news for plain users also
// reaches those whose access ended), or else every Telegram user with an
// enabled key.
func (s *service) BroadcastRecipients(ctx context.Context, a Audience) ([]int64, error) {
	if a.CompanyID != "" {
		us, err := s.users.ByCompany(ctx, a.CompanyID)
		if err != nil {
			return nil, err
		}
		ids := userIDs(us)
		return ids, nil
	}
	if a.Role != "" {
		return s.roleIDs(ctx, a.Role)
	}
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

// roleIDs is the sorted IDs of the users with role r.
func (s *service) roleIDs(ctx context.Context, r Role) ([]int64, error) {
	us, err := s.users.ByRole(ctx, r)
	if err != nil {
		return nil, err
	}
	ids := userIDs(us)
	return ids, nil
}

// userIDs is the sorted IDs of us.
func userIDs(us []*User) []int64 {
	ids := make([]int64, 0, len(us))
	for _, u := range us {
		ids = append(ids, u.ID)
	}
	slices.Sort(ids)
	return ids
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
