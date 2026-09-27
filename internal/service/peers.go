package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"slices"
	"strings"

	"github.com/irbgeo/geoirb-vpn-bot/internal/vpn/amnezia"
)

// Issue creates a new key: fresh keys, the lowest free IP, a peer on the
// server and a DB record. The DB record is saved inside the server update,
// so a DB failure leaves the server untouched; a failed server apply
// removes the DB record again.
func (s *Service) Issue(ctx context.Context, in IssueInput) (*Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issue(ctx, in)
}

// Extend adds days to a key: counted from the end date, or from now if the
// key already expired. A disabled key is enabled again with the same keys
// and IP. A key that never expires stays so.
func (s *Service) Extend(ctx context.Context, in ExtendInput) (*Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.extend(ctx, in)
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
	if p.Enabled {
		if err := s.removeFromServer(ctx, p); err != nil {
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
	return s.ourPeer(ctx, publicKey)
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

	err = s.vpn.Update(ctx, func(c *amnezia.ServerConf) error {
		ip, err := c.FreeIP(reserved)
		if err != nil {
			return err
		}
		p.IP = ip.String()
		if err := s.peers.Save(ctx, p); err != nil {
			return err
		}
		c.AddPeer(serverPeer(p))
		return nil
	})
	if err != nil {
		// WithoutCancel: the rollback must run even when the failure was
		// ctx itself being cancelled (e.g. SIGTERM during syncconf).
		rollback := context.WithoutCancel(ctx)
		if delErr := s.peers.Delete(rollback, p.PublicKey); delErr != nil {
			log.Printf("service: issue rollback for %s: %v", p.IP, delErr)
		}
		// A docker timeout can come after the command already ran in the
		// container: take the peer off again, or it stays with no owner.
		s.takeOff(
			rollback,
			takeOffInput{
				Peer:  p,
				Cause: err,
			},
		)
		return nil, err
	}
	s.countKeys(
		ctx,
		KeysDelta{
			UserID: p.UserID,
			Delta:  1,
		},
	)
	s.showInApp(ctx, p)
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

func serverPeer(p *Peer) amnezia.Peer {
	return amnezia.Peer{
		PublicKey:    p.PublicKey,
		PresharedKey: p.PSK,
		AllowedIPs:   p.IP + "/32",
	}
}

// takeOff undoes a failed attempt to put a key on the server: it removes
// the peer from the config and the key from the Amnezia app's list. A peer
// missing from the file costs no syncconf, unless the failure left the
// live interface ahead of the file (amnezia.ErrNotPersisted): then the
// update re-syncs the live interface from the file. Errors are logged:
// this runs on an error path already.
func (s *Service) takeOff(ctx context.Context, in takeOffInput) {
	p := in.Peer
	if err := s.removePeer(ctx, in); err != nil {
		log.Printf("service: roll back %s on the server: %v", p.IP, err)
	}
	if err := s.vpn.RemoveClient(ctx, p.PublicKey); err != nil {
		log.Printf("service: roll back %s in clientsTable: %v", p.IP, err)
	}
}

func (s *Service) removePeer(ctx context.Context, in takeOffInput) error {
	c, err := s.vpn.ReadConf(ctx)
	if err != nil {
		return err
	}
	if c.FindPeer(in.Peer.PublicKey) == nil && !errors.Is(in.Cause, amnezia.ErrNotPersisted) {
		return nil
	}
	return s.vpn.Update(ctx, func(c *amnezia.ServerConf) error {
		c.RemovePeer(in.Peer.PublicKey)
		return nil
	})
}

// showInApp lists the key in the Amnezia app. Failing here is not fatal:
// the key works, it is only missing from the app's list.
func (s *Service) showInApp(ctx context.Context, p *Peer) {
	err := s.vpn.SetClient(
		ctx,
		amnezia.ClientEntry{
			PublicKey:  p.PublicKey,
			Name:       p.Name,
			AllowedIPs: p.IP + "/32",
			CreatedAt:  p.CreatedAt,
		},
	)
	if err != nil {
		log.Printf("service: clientsTable set %s: %v", p.IP, err)
	}
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
// either step fails, the peer is taken off again: the server must not run
// a key the DB calls disabled (it would never be expired). A docker
// timeout can come after the command already ran, so a failed add is
// undone too. The caller holds s.mu.
func (s *Service) enableAndSave(ctx context.Context, p *Peer) error {
	wasEnabled := p.Enabled
	if !wasEnabled {
		if err := s.addToServer(ctx, p); err != nil {
			s.undoEnable(
				ctx,
				takeOffInput{
					Peer:  p,
					Cause: err,
				},
			)
			return err
		}
		p.Enabled = true
	}
	if err := s.peers.Save(ctx, p); err != nil {
		if !wasEnabled {
			p.Enabled = false
			s.undoEnable(
				ctx,
				takeOffInput{
					Peer:  p,
					Cause: err,
				},
			)
		}
		return err
	}
	return nil
}

// undoEnable takes a key back off after a failed enable, even when ctx
// is cancelled (the failure may be ctx itself). ErrIPTaken means nothing
// was changed, so there is nothing to undo.
func (s *Service) undoEnable(ctx context.Context, in takeOffInput) {
	if errors.Is(in.Cause, ErrIPTaken) || errors.Is(in.Cause, ErrUnreadable) {
		return
	}
	s.takeOff(context.WithoutCancel(ctx), in)
}

// addToServer adds the key's peer back, unless its IP was taken meanwhile
// (e.g. by a peer created in the Amnezia app).
func (s *Service) addToServer(ctx context.Context, p *Peer) error {
	if p.Unreadable {
		return ErrUnreadable // no PSK to put on the server
	}
	err := s.vpn.Update(ctx, func(c *amnezia.ServerConf) error {
		for _, other := range c.Peers {
			if other.PublicKey == p.PublicKey {
				return nil
			}
			if slices.Contains(allowedIPs(other.AllowedIPs), p.IP+"/32") {
				return fmt.Errorf("%w: %s", ErrIPTaken, p.IP)
			}
		}
		c.AddPeer(serverPeer(p))
		return nil
	})
	if err != nil {
		return err
	}
	s.showInApp(ctx, p)
	return nil
}

// allowedIPs splits "10.8.1.2/32, fd00::2/128" into its entries.
func allowedIPs(list string) []string {
	parts := strings.Split(list, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// disablePeer removes an enabled key from the server and marks it
// disabled. The caller holds s.mu.
func (s *Service) disablePeer(ctx context.Context, p *Peer) error {
	if err := s.removeFromServer(ctx, p); err != nil {
		return err
	}
	p.Enabled = false
	return s.peers.Save(ctx, p)
}

func (s *Service) removeFromServer(ctx context.Context, p *Peer) error {
	err := s.vpn.Update(ctx, func(c *amnezia.ServerConf) error {
		c.RemovePeer(p.PublicKey)
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.vpn.RemoveClient(ctx, p.PublicKey); err != nil {
		log.Printf("service: clientsTable remove %s: %v", p.IP, err)
	}
	return nil
}

// renderConfig builds the .conf text for a loaded key.
func (s *Service) renderConfig(ctx context.Context, p *Peer) (string, error) {
	if p.PrivateKey == "" {
		return "", ErrNoPrivateKey
	}
	c, err := s.vpn.ReadConf(ctx)
	if err != nil {
		return "", err
	}
	serverKey, err := s.vpn.ServerPublicKey(ctx)
	if err != nil {
		return "", err
	}
	return amnezia.RenderClient(
		&amnezia.ClientConf{
			Address:         p.IP + "/32",
			DNS:             s.cfg.DNS,
			PrivateKey:      p.PrivateKey,
			Params:          c.ClientParams(),
			ServerPublicKey: serverKey,
			PresharedKey:    p.PSK,
			Endpoint:        net.JoinHostPort(s.cfg.EndpointHost, c.Get("ListenPort")),
		},
	), nil
}
