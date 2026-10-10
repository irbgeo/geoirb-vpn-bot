package service

import (
	"context"
	"log"
)

// ReissueKey gives a user's key new secrets (a lost phone, a leaked
// file): the old ones stop working at once. IP, name, term, reminders and
// an admin block stay. It returns the public view of the new key; the caller
// renders the config from the stored secrets. A key without readable secrets
// is ErrUnreadable.
func (s *service) ReissueKey(ctx context.Context, k UserKey) (*Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	old, err := s.ownPeer(ctx, k)
	if err != nil {
		return nil, err
	}
	if !old.hasSecrets() {
		return nil, ErrUnreadable
	}
	keys, err := s.vpn.GenKeys(ctx)
	if err != nil {
		return nil, err
	}
	p := *old
	p.PublicKey, p.PrivateKey, p.PSK = keys.Public, keys.Private, keys.PSK
	peerSwap := PeerSwap{
		Old: old,
		New: &p,
	}
	err = s.swapKey(ctx, peerSwap)
	if err != nil {
		return nil, err
	}
	log.Printf("service: user %d reissued key %s", k.UserID, p.IP)
	return p.public(), nil
}

// DeleteOwnKey deletes one of the user's own keys for good. A key an admin
// disabled is ErrBlocked: the block lives on the key, so deleting it would
// let the user start over with a new one. Admins delete it with Delete.
func (s *service) DeleteOwnKey(ctx context.Context, k UserKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ownPeer(ctx, k)
	if err != nil {
		return err
	}
	if p.Blocked {
		return ErrBlocked
	}
	err = s.deletePeer(ctx, p)
	if err != nil {
		return err
	}
	log.Printf("service: user %d deleted key %s", k.UserID, p.IP)
	return nil
}

// ownPeer loads a key of this user; another user's key is ErrNotFound, so
// its existence doesn't leak.
func (s *service) ownPeer(ctx context.Context, k UserKey) (*Peer, error) {
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
// enabled key, on the server. The DB records are swapped first (one
// write), so a DB failure leaves the server as it was. On any failure the
// old key is put back. The caller holds s.mu.
func (s *service) swapKey(ctx context.Context, in PeerSwap) error {
	err := s.peers.Replace(ctx, in)
	if err == nil && in.Old.Enabled {
		replacePeerInput := &ReplacePeerInput{
			Old: in.Old.vpnPeer(),
			New: in.New.vpnPeer(),
		}
		err = s.vpn.ReplacePeer(ctx, replacePeerInput)
	}
	if err != nil {
		s.unswap(ctx, in)
		return err
	}
	return nil
}

// unswap undoes a failed swapKey in the DB: the old record comes back in
// place of the new one, again in one write (the VPN already put the old
// peer back). It runs even when ctx is cancelled; an error is logged.
func (s *service) unswap(ctx context.Context, in PeerSwap) {
	back := PeerSwap{
		Old: in.New,
		New: in.Old,
	}
	err := s.peers.Replace(context.WithoutCancel(ctx), back)
	if err != nil {
		log.Printf("service: undo reissue of %s: %v", in.Old.IP, err)
	}
}
