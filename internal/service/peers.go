package service

import (
	"context"
	"log"
	"net/netip"
	"time"
)

// Issue creates a new key: fresh keys, the lowest free IP, a peer on the
// server and a DB record. The DB record is saved inside the server update,
// so a DB failure leaves the server untouched; a failed server apply
// removes the DB record again.
func (s *service) Issue(ctx context.Context, in IssueInput) (*Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.issue(ctx, in)
	if err != nil {
		return nil, err
	}
	if in.UserID != 0 {
		// A key issued by an admin counts as the plain user's free trial, so
		// deleting it does not give a new one. Best effort, like startTrial:
		// the key itself stops a second trial, and DeleteOwnKey marks again.
		err = s.closeTrial(ctx, in.UserID)
		if err != nil {
			log.Printf("service: mark trial used for %d: %v", in.UserID, err)
		}
	}
	return p.public(), nil
}

// Extend adds days to a key: counted from the end date, or from now if the
// key already expired. A disabled key is enabled again with the same keys
// and IP. A key that never expires stays so.
func (s *service) Extend(ctx context.Context, in ExtendInput) (*Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.extend(ctx, in)
	return p.public(), err
}

// Disable is the admin's block: it removes the key from the server and
// marks it Blocked, so buying can't turn it back on. The key stays in the
// DB (with its IP reserved) so Enable or Extend can bring it back.
func (s *service) Disable(ctx context.Context, publicKey string) error {
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
func (s *service) Enable(ctx context.Context, publicKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ourPeer(ctx, publicKey)
	if err != nil || p.Enabled {
		return err
	}
	if !p.ExpiresAt.IsZero() && !p.ExpiresAt.After(time.Now()) {
		return ErrExpired // Maintain would disable it again within a minute
	}
	p.Blocked = false
	return s.enableAndSave(ctx, p)
}

// Delete removes the key from the server and the DB for good.
func (s *service) Delete(ctx context.Context, publicKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ourPeer(ctx, publicKey)
	if err != nil {
		return err
	}
	return s.deletePeer(ctx, p)
}

// ClientConfig renders the .conf file for a key.
func (s *service) ClientConfig(ctx context.Context, publicKey string) (string, error) {
	p, err := s.ourPeer(ctx, publicKey)
	if err != nil {
		return "", err
	}
	return s.renderConfig(ctx, p)
}

// Key returns one of the bot's keys, or ErrNotFound.
func (s *service) Key(ctx context.Context, publicKey string) (*Peer, error) {
	p, err := s.ourPeer(ctx, publicKey)
	return p.public(), err
}

// issue is Issue without the lock, for callers that already hold it.
func (s *service) issue(ctx context.Context, in IssueInput) (*Peer, error) {
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
	now := time.Now()
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
	p.resetReminders()

	addPeerInput := &AddPeerInput{
		Peer:     p.vpnPeer(),
		Reserved: reserved,
		Save: func(ip string) error {
			p.IP = ip
			return s.peers.Save(ctx, p)
		},
	}
	err = s.vpn.AddPeer(ctx, addPeerInput)
	if err != nil {
		// WithoutCancel: the rollback must run even when the failure was
		// ctx itself being cancelled (e.g. SIGTERM during syncconf).
		delErr := s.peers.Delete(context.WithoutCancel(ctx), p.PublicKey)
		if delErr != nil {
			log.Printf("service: issue rollback for %s: %v", p.IP, delErr)
		}
		return nil, err
	}
	keysDelta := KeysDelta{
		UserID: p.UserID,
		Delta:  1,
	}
	s.countKeys(ctx, keysDelta)
	return p, nil
}

// reservedIPs are the IPs of every DB key on this server, enabled or not:
// a disabled key has no peer on the server but keeps its IP.
func (s *service) reservedIPs(ctx context.Context) ([]netip.Addr, error) {
	ips, err := s.peers.ServerIPs(ctx, s.cfg.ServerID)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, v := range ips {
		ip, err := netip.ParseAddr(v)
		if err == nil {
			out = append(out, ip)
		}
	}
	return out, nil
}

// keyName is in.Name, or "tg:<username or ID>" for a Telegram user's key
// issued without a name.
func (s *service) keyName(ctx context.Context, in IssueInput) (string, error) {
	if in.Name != "" || in.UserID == 0 {
		return in.Name, nil
	}
	u, err := s.User(ctx, in.UserID)
	if err != nil {
		return "", err
	}
	return "tg:" + displayName(u), nil
}

// countKeys updates the owner's KeysCount. A failure is only logged: the
// count is fixed at the next start (Reconcile), and limits never read it.
func (s *service) countKeys(ctx context.Context, d KeysDelta) {
	if d.UserID == 0 {
		return
	}
	err := s.users.AddKeys(ctx, d)
	if err != nil {
		log.Printf("service: keys count of %d: %v", d.UserID, err)
	}
}

// extend is Extend without the lock, for callers that already hold it.
func (s *service) extend(ctx context.Context, in ExtendInput) (*Peer, error) {
	p, err := s.ourPeer(ctx, in.PublicKey)
	if err != nil {
		return nil, err
	}
	if !p.ExpiresAt.IsZero() {
		from := p.ExpiresAt
		now := time.Now()
		if from.Before(now) {
			from = now
		}
		p.ExpiresAt = from.AddDate(0, 0, in.Days)
	}
	p.resetReminders()
	p.Blocked = false // only admins and paying users get here; checkBuyer and chooseKey stop a blocked buyer, with or without a key named
	err = s.enableAndSave(ctx, p)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ourPeer loads a key from the DB, or ErrNotFound.
func (s *service) ourPeer(ctx context.Context, publicKey string) (*Peer, error) {
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
// key the DB calls disabled (it would never be expired). A save that
// failed but is stored counts as done (see stored). A failed add undoes
// itself (see VPN). The caller holds s.mu.
func (s *service) enableAndSave(ctx context.Context, p *Peer) error {
	wasEnabled := p.Enabled
	if !wasEnabled {
		err := s.addToServer(ctx, p)
		if err != nil {
			return err
		}
		p.Enabled = true
	}
	err := s.peers.Save(ctx, p)
	if err != nil && s.stored(ctx, p) {
		// The write went through and only its reply was lost: the change is
		// made, so the caller must not undo it (Pay would refund paid days).
		log.Printf("service: save of %s failed but is stored: %v", p.IP, err)
		return nil
	}
	if err != nil {
		if !wasEnabled {
			p.Enabled = false
			s.undoEnable(ctx, p)
		}
		return err
	}
	return nil
}

// addToServer adds the key's peer back, unless its IP was taken meanwhile
// (e.g. by a peer created in the Amnezia app).
func (s *service) addToServer(ctx context.Context, p *Peer) error {
	if !p.hasSecrets() {
		return ErrUnreadable // no PSK to put on the server
	}
	return s.vpn.PutPeer(ctx, p.vpnPeer())
}

// stored reports whether the DB holds p's state: after a failed save it
// tells a write that never happened from one whose reply was lost
// (shutdown, network cut). It reads even when ctx is cancelled; a failed
// read is false.
func (s *service) stored(ctx context.Context, p *Peer) bool {
	got, err := s.peers.Get(context.WithoutCancel(ctx), p.PublicKey)
	if err != nil || got == nil {
		return false
	}
	// The DB keeps milliseconds, so the end dates are compared that coarsely.
	return got.Enabled == p.Enabled &&
		got.Blocked == p.Blocked &&
		got.Reminded3d == p.Reminded3d &&
		got.Reminded1d == p.Reminded1d &&
		got.ExpiresAt.Sub(p.ExpiresAt).Abs() < time.Millisecond
}

// undoEnable takes a key back off when its enabled state could not be
// saved, even when ctx is cancelled (the failure may be ctx itself).
func (s *service) undoEnable(ctx context.Context, p *Peer) {
	err := s.vpn.RemovePeer(context.WithoutCancel(ctx), p.vpnPeer())
	if err != nil {
		log.Printf("service: roll back %s on the server: %v", p.IP, err)
	}
}

// disablePeer removes an enabled key from the server and marks it
// disabled. The caller holds s.mu.
func (s *service) disablePeer(ctx context.Context, p *Peer) error {
	err := s.vpn.RemovePeer(ctx, p.vpnPeer())
	if err != nil {
		return err
	}
	p.Enabled = false
	return s.peers.Save(ctx, p)
}

// deletePeer removes a loaded key from the server and the DB. The caller
// holds s.mu.
func (s *service) deletePeer(ctx context.Context, p *Peer) error {
	if p.Enabled {
		err := s.vpn.RemovePeer(ctx, p.vpnPeer())
		if err != nil {
			return err
		}
	}
	err := s.peers.Delete(ctx, p.PublicKey)
	if err != nil {
		return err
	}
	keysDelta := KeysDelta{
		UserID: p.UserID,
		Delta:  -1,
	}
	s.countKeys(ctx, keysDelta)
	return nil
}

// renderConfig builds the .conf text for a loaded key.
func (s *service) renderConfig(ctx context.Context, p *Peer) (string, error) {
	if p.PrivateKey == "" {
		return "", ErrNoPrivateKey
	}
	clientSpec := &ClientSpec{
		IP:           p.IP,
		PrivateKey:   p.PrivateKey,
		PSK:          p.PSK,
		DNS:          s.cfg.DNS,
		MTU:          s.cfg.MTU,
		EndpointHost: s.cfg.EndpointHost,
	}
	return s.vpn.ClientConfig(ctx, clientSpec)
}
