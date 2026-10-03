package service

import (
	"context"
	"log"
	"net/netip"
)

// Issue creates a new key: fresh keys, the lowest free IP, a peer on the
// server and a DB record. The DB record is saved inside the server update,
// so a DB failure leaves the server untouched; a failed server apply
// removes the DB record again.
func (s *Service) Issue(ctx context.Context, in IssueInput) (*Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.issue(ctx, in)
	return p.public(), err
}

// Extend adds days to a key: counted from the end date, or from now if the
// key already expired. A disabled key is enabled again with the same keys
// and IP. A key that never expires stays so.
func (s *Service) Extend(ctx context.Context, in ExtendInput) (*Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.extend(ctx, in)
	return p.public(), err
}

// Disable is the admin's block: it removes the key from the server and
// marks it Blocked, so buying can't turn it back on. The key stays in the
// DB (with its IP reserved) so Enable or Extend can bring it back.
func (s *Service) Disable(ctx context.Context, publicKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ourPeer(ctx, publicKey)
	if err != nil {
		return err
	}
	p.Blocked = true
	if !p.Enabled { // already off (e.g. expired): only the block is new
		return s.peers.Save(ctx, p)
	}
	return s.disablePeer(ctx, p)
}

// Enable puts a disabled key back on the server.
func (s *Service) Enable(ctx context.Context, publicKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ourPeer(ctx, publicKey)
	if err != nil || p.Enabled {
		return err
	}
	if !p.ExpiresAt.IsZero() && !p.ExpiresAt.After(s.now()) {
		return ErrExpired // Maintain would disable it again within a minute
	}
	p.Blocked = false
	return s.enableAndSave(ctx, p)
}

// Delete removes the key from the server and the DB for good.
func (s *Service) Delete(ctx context.Context, publicKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ourPeer(ctx, publicKey)
	if err != nil {
		return err
	}
	return s.deletePeer(ctx, p)
}

// deletePeer removes a loaded key from the server and the DB. The caller
// holds s.mu.
func (s *Service) deletePeer(ctx context.Context, p *Peer) error {
	if p.Enabled {
		if err := s.vpn.RemovePeer(ctx, p.vpnPeer()); err != nil {
			return err
		}
	}
	if err := s.peers.Delete(ctx, p.PublicKey); err != nil {
		return err
	}
	s.countKeys(
		ctx,
		KeysDelta{
			UserID: p.UserID,
			Delta:  -1,
		},
	)
	return nil
}

// ClientConfig renders the .conf file for a key.
func (s *Service) ClientConfig(ctx context.Context, publicKey string) (string, error) {
	p, err := s.ourPeer(ctx, publicKey)
	if err != nil {
		return "", err
	}
	return s.renderConfig(ctx, p)
}

// Key returns one of the bot's keys, or ErrNotFound.
func (s *Service) Key(ctx context.Context, publicKey string) (*Peer, error) {
	p, err := s.ourPeer(ctx, publicKey)
	return p.public(), err
}

// issue is Issue without the lock, for callers that already hold it.
func (s *Service) issue(ctx context.Context, in IssueInput) (*Peer, error) {
	keys, err := s.vpn.GenKeys(ctx)
	if err != nil {
		return nil, err
	}
	reserved, err := s.reservedIPs(ctx)
	if err != nil {
		return nil, err
	}
	name, err := s.keyName(ctx, in)
	if err != nil {
		return nil, err
	}
	now := s.now()
	p := &Peer{
		PublicKey:  keys.Public,
		ServerID:   s.cfg.ServerID,
		UserID:     in.UserID,
		Name:       name,
		PrivateKey: keys.Private,
		PSK:        keys.PSK,
		Enabled:    true,
		CreatedAt:  now,
	}
	if in.Days > 0 {
		p.ExpiresAt = now.AddDate(0, 0, in.Days)
	}

	err = s.vpn.AddPeer(
		ctx,
		&AddPeerInput{
			Peer:     p.vpnPeer(),
			Reserved: reserved,
			Save: func(ip string) error {
				p.IP = ip
				return s.peers.Save(ctx, p)
			},
		},
	)
	if err != nil {
		// WithoutCancel: the rollback must run even when the failure was
		// ctx itself being cancelled (e.g. SIGTERM during syncconf).
		if delErr := s.peers.Delete(context.WithoutCancel(ctx), p.PublicKey); delErr != nil {
			log.Printf("service: issue rollback for %s: %v", p.IP, delErr)
		}
		return nil, err
	}
	s.countKeys(
		ctx,
		KeysDelta{
			UserID: p.UserID,
			Delta:  1,
		},
	)
	return p, nil
}

// countKeys updates the owner's KeysCount. A failure is only logged: the
// count is fixed at the next start (Reconcile), and limits never read it.
func (s *Service) countKeys(ctx context.Context, d KeysDelta) {
	if d.UserID == 0 {
		return
	}
	if err := s.users.AddKeys(ctx, d); err != nil {
		log.Printf("service: keys count of %d: %v", d.UserID, err)
	}
}

// reservedIPs are the IPs of every DB key on this server, enabled or not:
// a disabled key has no peer on the server but keeps its IP.
func (s *Service) reservedIPs(ctx context.Context) ([]netip.Addr, error) {
	ips, err := s.peers.ServerIPs(ctx, s.cfg.ServerID)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, v := range ips {
		if ip, err := netip.ParseAddr(v); err == nil {
			out = append(out, ip)
		}
	}
	return out, nil
}

// keyName is in.Name, or "tg:<username or ID>" for a Telegram user's key
// issued without a name.
func (s *Service) keyName(ctx context.Context, in IssueInput) (string, error) {
	if in.Name != "" || in.UserID == 0 {
		return in.Name, nil
	}
	u, err := s.User(ctx, in.UserID)
	if err != nil {
		return "", err
	}
	return "tg:" + displayName(u), nil
}

// extend is Extend without the lock, for callers that already hold it.
func (s *Service) extend(ctx context.Context, in ExtendInput) (*Peer, error) {
	p, err := s.ourPeer(ctx, in.PublicKey)
	if err != nil {
		return nil, err
	}
	if !p.ExpiresAt.IsZero() {
		from := p.ExpiresAt
		if now := s.now(); from.Before(now) {
			from = now
		}
		p.ExpiresAt = from.AddDate(0, 0, in.Days)
	}
	p.Reminded3d = false
	p.Reminded1d = false
	p.Blocked = false // only admins and paying users get here; checkBuyer stops a blocked buyer
	if err := s.enableAndSave(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// ourPeer loads a key from the DB, or ErrNotFound.
func (s *Service) ourPeer(ctx context.Context, publicKey string) (*Peer, error) {
	p, err := s.peers.Get(ctx, publicKey)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrNotFound
	}
	return p, nil
}

// enableAndSave puts a disabled key back on the server and saves p. If
// the save fails, the peer is taken off again: the server must not run a
// key the DB calls disabled (it would never be expired). A failed add
// undoes itself (see VPN). The caller holds s.mu.
func (s *Service) enableAndSave(ctx context.Context, p *Peer) error {
	wasEnabled := p.Enabled
	if !wasEnabled {
		if err := s.addToServer(ctx, p); err != nil {
			return err
		}
		p.Enabled = true
	}
	if err := s.peers.Save(ctx, p); err != nil {
		if !wasEnabled {
			p.Enabled = false
			s.undoEnable(ctx, p)
		}
		return err
	}
	return nil
}

// undoEnable takes a key back off when its enabled state could not be
// saved, even when ctx is cancelled (the failure may be ctx itself).
func (s *Service) undoEnable(ctx context.Context, p *Peer) {
	if err := s.vpn.RemovePeer(context.WithoutCancel(ctx), p.vpnPeer()); err != nil {
		log.Printf("service: roll back %s on the server: %v", p.IP, err)
	}
}

// addToServer adds the key's peer back, unless its IP was taken meanwhile
// (e.g. by a peer created in the Amnezia app).
func (s *Service) addToServer(ctx context.Context, p *Peer) error {
	if !p.hasSecrets() {
		return ErrUnreadable // no PSK to put on the server
	}
	return s.vpn.PutPeer(ctx, p.vpnPeer())
}

// disablePeer removes an enabled key from the server and marks it
// disabled. The caller holds s.mu.
func (s *Service) disablePeer(ctx context.Context, p *Peer) error {
	if err := s.vpn.RemovePeer(ctx, p.vpnPeer()); err != nil {
		return err
	}
	p.Enabled = false
	return s.peers.Save(ctx, p)
}

// renderConfig builds the .conf text for a loaded key.
func (s *Service) renderConfig(ctx context.Context, p *Peer) (string, error) {
	if p.PrivateKey == "" {
		return "", ErrNoPrivateKey
	}
	return s.vpn.ClientConfig(
		ctx,
		&ClientSpec{
			IP:           p.IP,
			PrivateKey:   p.PrivateKey,
			PSK:          p.PSK,
			DNS:          s.cfg.DNS,
			MTU:          s.cfg.MTU,
			EndpointHost: s.cfg.EndpointHost,
		},
	)
}
