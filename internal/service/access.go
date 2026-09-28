package service

import (
	"context"
	"net/netip"
	"slices"
	"time"

	"github.com/irbgeo/geoirb-vpn-bot/internal/vpn/amnezia"
)

// onlineWindow: a key with a handshake this recent counts as online.
// WireGuard re-handshakes every ~2 minutes while traffic flows.
const onlineWindow = 3 * time.Minute

// Access returns the user's keys with live handshake and traffic, sorted
// by IP.
func (s *Service) Access(ctx context.Context, userID int64) ([]KeyInfo, error) {
	ps, err := s.peers.ByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.withStats(ctx, ps)
}

// UserConfig renders a key's config for its owner. Someone else's key is
// reported as ErrNotFound, so a forged button can't fetch it.
func (s *Service) UserConfig(ctx context.Context, k UserKey) (*KeyConfig, error) {
	p, err := s.ownPeer(ctx, k)
	if err != nil {
		return nil, err
	}
	conf, err := s.renderConfig(ctx, p)
	if err != nil {
		return nil, err
	}
	return &KeyConfig{
		Peer: p,
		Conf: conf,
	}, nil
}

// withStats joins keys with `awg show dump` and sorts them by IP.
func (s *Service) withStats(ctx context.Context, ps []*Peer) ([]KeyInfo, error) {
	stats, err := s.vpn.Stats(ctx)
	if err != nil {
		return nil, err
	}
	live := make(map[string]amnezia.PeerStat, len(stats))
	for _, st := range stats {
		live[st.PublicKey] = st
	}
	now := s.now()
	out := make([]KeyInfo, 0, len(ps))
	for _, p := range ps {
		st := live[p.PublicKey]
		out = append(
			out,
			KeyInfo{
				Peer:          p,
				Online:        !st.LatestHandshake.IsZero() && now.Sub(st.LatestHandshake) < onlineWindow,
				LastHandshake: st.LatestHandshake,
				Sent:          st.RX,
				Received:      st.TX,
			},
		)
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
