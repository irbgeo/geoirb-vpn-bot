package service

import (
	"context"
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

// Disable removes the key from the server; it stays in the DB (with its IP
// reserved) so Enable or Extend can bring it back unchanged.
func (s *Service) Disable(ctx context.Context, publicKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ourPeer(ctx, publicKey)
	if err != nil || !p.Enabled {
		return err
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
	return s.peers.Delete(ctx, p.PublicKey)
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
		if rmErr := s.removePeer(rollback, p.PublicKey); rmErr != nil {
			log.Printf("service: issue rollback on the server for %s: %v", p.IP, rmErr)
		}
		return nil, err
	}
	s.showInApp(ctx, p)
	return p, nil
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

// removePeer takes one peer off the server config (no clientsTable change)
// if it is there; a peer that never got on costs no syncconf.
func (s *Service) removePeer(ctx context.Context, publicKey string) error {
	c, err := s.vpn.ReadConf(ctx)
	if err != nil {
		return err
	}
	if c.FindPeer(publicKey) == nil {
		return nil
	}
	return s.vpn.Update(ctx, func(c *amnezia.ServerConf) error {
		c.RemovePeer(publicKey)
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
// key the DB calls disabled (it would never be expired). The caller holds
// s.mu.
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
			if rmErr := s.removePeer(context.WithoutCancel(ctx), p.PublicKey); rmErr != nil {
				log.Printf("service: roll back enabling %s: %v", p.IP, rmErr)
			}
		}
		return err
	}
	return nil
}

// addToServer adds the key's peer back, unless its IP was taken meanwhile
// (e.g. by a peer created in the Amnezia app).
func (s *Service) addToServer(ctx context.Context, p *Peer) error {
	err := s.vpn.Update(ctx, func(c *amnezia.ServerConf) error {
		for _, other := range c.Peers {
			if other.PublicKey == p.PublicKey {
				return nil
			}
			if slices.Contains(allowedIPs(other.AllowedIPs), p.IP+"/32") {
				return fmt.Errorf("service: IP %s is taken on the server by another peer", p.IP)
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
