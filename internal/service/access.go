package service

import (
	"context"
	"net/netip"
	"slices"
	"time"
)

// onlineWindow: a key with a handshake this recent counts as online.
// WireGuard re-handshakes every ~2 minutes while traffic flows.
const onlineWindow = 3 * time.Minute

// Access returns the user's keys with live handshake and traffic, sorted
// by IP.
func (s *service) Access(ctx context.Context, userID int64) ([]KeyInfo, error) {
	ps, err := s.peers.ByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.withStats(ctx, ps)
}

// UserConfig renders a key's config for its owner. Someone else's key is
// reported as ErrNotFound, so a forged button can't fetch it.
func (s *service) UserConfig(ctx context.Context, k UserKey) (*KeyConfig, error) {
	p, err := s.ownPeer(ctx, k)
	if err != nil {
		return nil, err
	}
	conf, err := s.renderConfig(ctx, p)
	if err != nil {
		return nil, err
	}
	return &KeyConfig{
		Peer: p.public(),
		Conf: conf,
	}, nil
}

// withStats joins keys with `awg show dump` and sorts them by IP.
func (s *service) withStats(ctx context.Context, ps []*Peer) ([]KeyInfo, error) {
	stats, err := s.vpn.Stats(ctx)
	if err != nil {
		return nil, err
	}
	live := make(map[string]PeerStat, len(stats))
	for _, st := range stats {
		live[st.PublicKey] = st
	}
	now := time.Now()
	out := make([]KeyInfo, 0, len(ps))
	for _, p := range ps {
		st := live[p.PublicKey]
		keyInfo := KeyInfo{
			Peer:          p.public(),
			Online:        st.onlineAt(now),
			LastHandshake: st.LastHandshake,
			Sent:          st.Sent,
			Received:      st.Received,
		}
		out = append(out, keyInfo)
	}
	slices.SortFunc(out, func(a, b KeyInfo) int {
		return parseIP(a.Peer.IP).Compare(parseIP(b.Peer.IP))
	})
	return out, nil
}

func parseIP(s string) netip.Addr {
	ip, _ := netip.ParseAddr(s)
	return ip
}

// onlineCount is how many peers on the server (keys and manual ones) had a
// handshake within onlineWindow.
func (s *service) onlineCount(ctx context.Context) (int, error) {
	stats, err := s.vpn.Stats(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	n := 0
	for _, st := range stats {
		if st.onlineAt(now) {
			n++
		}
	}
	return n, nil
}

// onlineAt reports a handshake within onlineWindow before now.
func (s PeerStat) onlineAt(now time.Time) bool {
	return !s.LastHandshake.IsZero() && now.Sub(s.LastHandshake) < onlineWindow
}
