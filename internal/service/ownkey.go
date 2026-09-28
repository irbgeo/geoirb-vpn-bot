package service

import (
	"context"
	"log"
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
	p.PublicKey, p.PrivateKey, p.PSK = keys.Public, keys.Private, keys.PSK
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
	return p.public(), nil
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
// enabled key, on the server. The DB records are swapped first, so a DB
// failure leaves the server as it was. On any failure the old key is put
// back. The caller holds s.mu.
func (s *Service) swapKey(ctx context.Context, in swapInput) error {
	err := s.swapRecords(ctx, in)
	if err == nil && in.Old.Enabled {
		err = s.vpn.ReplacePeer(
			ctx,
			&ReplacePeerInput{
				Old: in.Old.vpnPeer(),
				New: in.New.vpnPeer(),
			},
		)
	}
	if err != nil {
		s.unswap(ctx, in)
		return err
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

// unswap undoes a failed swapKey in the DB: the new record goes, the old
// one comes back (the VPN already put the old peer back). It runs even
// when ctx is cancelled; errors are logged.
func (s *Service) unswap(ctx context.Context, in swapInput) {
	rb := context.WithoutCancel(ctx)
	if err := s.peers.Delete(rb, in.New.PublicKey); err != nil {
		log.Printf("service: undo reissue of %s: %v", in.Old.IP, err)
	}
	if err := s.peers.Save(rb, in.Old); err != nil {
		log.Printf("service: undo reissue of %s: restore old record: %v", in.Old.IP, err)
	}
}
