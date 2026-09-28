package service

import (
	"context"
	"log"

	"github.com/irbgeo/geoirb-vpn-bot/internal/vpn/amnezia"
)

// ReissueKey gives a user's key new secrets (a lost phone, a leaked
// file): the old ones stop working at once. IP, name, term, reminders and
// an admin block stay. The returned key carries the new private key, so the
// caller can send the new config.
func (s *Service) ReissueKey(ctx context.Context, k UserKey) (*Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	old, err := s.ownPeer(ctx, k)
	if err != nil {
		return nil, err
	}
	keys, err := s.vpn.GenKeys(ctx)
	if err != nil {
		return nil, err
	}
	p := *old
	p.PublicKey, p.PrivateKey, p.PSK, p.Unreadable = keys.Public, keys.Private, keys.PSK, false
	if err := s.swapKey(
		ctx,
		swapInput{
			Old: old,
			New: &p,
		},
	); err != nil {
		return nil, err
	}
	log.Printf("service: user %d reissued key %s", k.UserID, p.IP)
	return &p, nil
}

// DeleteOwnKey deletes one of the user's own keys for good.
func (s *Service) DeleteOwnKey(ctx context.Context, k UserKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ownPeer(ctx, k)
	if err != nil {
		return err
	}
	if err := s.deletePeer(ctx, p); err != nil {
		return err
	}
	log.Printf("service: user %d deleted key %s", k.UserID, p.IP)
	return nil
}

// ownPeer loads a key of this user; another user's key is ErrNotFound, so
// its existence doesn't leak.
func (s *Service) ownPeer(ctx context.Context, k UserKey) (*Peer, error) {
	p, err := s.ourPeer(ctx, k.PublicKey)
	if err != nil {
		return nil, err
	}
	if p.UserID != k.UserID {
		return nil, ErrNotFound
	}
	return p, nil
}

// swapKey replaces in.Old with in.New (same IP) in the DB and, for an
// enabled key, on the server in one update. The DB records are swapped
// inside the update, so a DB failure leaves the server as it was. On any
// failure the old key is put back. The caller holds s.mu.
func (s *Service) swapKey(ctx context.Context, in swapInput) error {
	err := s.swapRecords(ctx, in)
	if err == nil && in.Old.Enabled {
		err = s.vpn.Update(ctx, func(c *amnezia.ServerConf) error {
			c.RemovePeer(in.Old.PublicKey)
			c.AddPeer(serverPeer(in.New))
			return nil
		})
	}
	if err != nil {
		s.unswap(ctx, in, err)
		return err
	}
	if in.Old.Enabled {
		if err := s.vpn.RemoveClient(ctx, in.Old.PublicKey); err != nil {
			log.Printf("service: clientsTable remove %s: %v", in.Old.IP, err)
		}
		s.showInApp(ctx, in.New)
	}
	return nil
}

// swapRecords deletes the old DB record (it holds the IP) and saves the new.
func (s *Service) swapRecords(ctx context.Context, in swapInput) error {
	if err := s.peers.Delete(ctx, in.Old.PublicKey); err != nil {
		return err
	}
	return s.peers.Save(ctx, in.New)
}

// unswap undoes a failed swapKey: the new record and peer go, the old
// record comes back and, if it was enabled, the old peer is on the server
// again. It runs even when ctx is cancelled; errors are logged.
func (s *Service) unswap(ctx context.Context, in swapInput, cause error) {
	rb := context.WithoutCancel(ctx)
	if err := s.peers.Delete(rb, in.New.PublicKey); err != nil {
		log.Printf("service: undo reissue of %s: %v", in.Old.IP, err)
	}
	if err := s.peers.Save(rb, in.Old); err != nil {
		log.Printf("service: undo reissue of %s: restore old record: %v", in.Old.IP, err)
	}
	if !in.Old.Enabled {
		return
	}
	s.takeOff(
		rb,
		takeOffInput{
			Peer:  in.New,
			Cause: cause,
		},
	)
	if err := s.addToServer(rb, in.Old); err != nil {
		log.Printf("service: undo reissue of %s: old peer back: %v", in.Old.IP, err)
	}
}
