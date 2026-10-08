package amnezia

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"slices"
	"strings"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// VPN is service.VPN on an Amnezia server. It keeps the config file, the
// live interface and the app's client list (clientsTable) together, and
// undoes a change that failed half way before returning its error.
type VPN struct {
	srv *Server
}

// NewVPN wraps an opened Server.
func NewVPN(
	srv *Server,
) *VPN {
	return &VPN{
		srv: srv,
	}
}

// GenKeys makes a fresh key set.
func (s *VPN) GenKeys(ctx context.Context) (service.VPNKeys, error) {
	k, err := s.srv.GenKeys(ctx)
	if err != nil {
		return service.VPNKeys{}, err
	}
	return service.VPNKeys{
		Private: k.Private,
		Public:  k.Public,
		PSK:     k.PSK,
	}, nil
}

// AddPeer picks the lowest free IP, lets in.Save store the key and puts
// the peer on the server, all in one config update: a failed Save leaves
// the server untouched.
func (s *VPN) AddPeer(ctx context.Context, in *service.AddPeerInput) error {
	p := *in.Peer
	err := s.srv.Update(ctx, func(c *ServerConf) error {
		ip, err := c.FreeIP(in.Reserved)
		if err != nil {
			return err
		}
		p.IP = ip.String()
		if err := in.Save(p.IP); err != nil {
			return err
		}
		c.AddPeer(serverPeer(&p))
		return nil
	})
	if err != nil {
		s.undo(
			ctx,
			&takeOffInput{
				Peer:  &p,
				Cause: err,
			},
		)
		return err
	}
	s.showInApp(ctx, &p)
	return nil
}

// PutPeer puts a known key back on its IP, unless another peer (e.g. one
// made in the Amnezia app) took the IP meanwhile: service.ErrIPTaken.
func (s *VPN) PutPeer(ctx context.Context, p *service.VPNPeer) error {
	err := s.srv.Update(ctx, func(c *ServerConf) error {
		for _, other := range c.Peers {
			if other.PublicKey == p.PublicKey {
				return nil
			}
			if slices.Contains(allowedIPs(other.AllowedIPs), p.IP+"/32") {
				return fmt.Errorf("%w: %s", service.ErrIPTaken, p.IP)
			}
		}
		c.AddPeer(serverPeer(p))
		return nil
	})
	if errors.Is(err, service.ErrIPTaken) {
		return err // nothing was changed
	}
	if err != nil {
		s.undo(
			ctx,
			&takeOffInput{
				Peer:  p,
				Cause: err,
			},
		)
		return err
	}
	s.showInApp(ctx, p)
	return nil
}

// RemovePeer takes a key off the server and the app's list.
func (s *VPN) RemovePeer(ctx context.Context, p *service.VPNPeer) error {
	err := s.srv.Update(ctx, func(c *ServerConf) error {
		c.RemovePeer(p.PublicKey)
		return nil
	})
	if err != nil {
		return err
	}
	s.hideInApp(ctx, p)
	return nil
}

// ReplacePeer swaps in.Old for in.New in one config update. On failure
// the new peer is taken off and the old one put back.
func (s *VPN) ReplacePeer(ctx context.Context, in *service.ReplacePeerInput) error {
	err := s.srv.Update(ctx, func(c *ServerConf) error {
		c.RemovePeer(in.Old.PublicKey)
		c.AddPeer(serverPeer(in.New))
		return nil
	})
	if err != nil {
		s.undo(
			ctx,
			&takeOffInput{
				Peer:  in.New,
				Cause: err,
			},
		)
		if err := s.PutPeer(context.WithoutCancel(ctx), in.Old); err != nil {
			log.Printf("amnezia: put %s back: %v", in.Old.IP, err)
		}
		return err
	}
	s.hideInApp(ctx, in.Old)
	s.showInApp(ctx, in.New)
	return nil
}

// PeerKeys returns the public key of every peer in the config.
func (s *VPN) PeerKeys(ctx context.Context) ([]string, error) {
	c, err := s.srv.ReadConf(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(c.Peers))
	for _, p := range c.Peers {
		out = append(out, p.PublicKey)
	}
	return out, nil
}

// SubnetUsage counts taken client IPs: peers plus reserved.
func (s *VPN) SubnetUsage(ctx context.Context, reserved []netip.Addr) (used, total int, err error) {
	c, err := s.srv.ReadConf(ctx)
	if err != nil {
		return 0, 0, err
	}
	return c.SubnetUsage(reserved)
}

// Stats returns live data per peer. The server's RX is what the client
// sent, its TX what the client received.
func (s *VPN) Stats(ctx context.Context) ([]service.PeerStat, error) {
	stats, err := s.srv.Stats(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.PeerStat, 0, len(stats))
	for _, st := range stats {
		out = append(
			out,
			service.PeerStat{
				PublicKey:     st.PublicKey,
				LastHandshake: st.LatestHandshake,
				Sent:          st.RX,
				Received:      st.TX,
			},
		)
	}
	return out, nil
}

// ClientConfig renders a client .conf with the server's obfuscation
// params, public key and port.
func (s *VPN) ClientConfig(ctx context.Context, spec *service.ClientSpec) (string, error) {
	c, err := s.srv.ReadConf(ctx)
	if err != nil {
		return "", err
	}
	serverKey, err := s.srv.ServerPublicKey(ctx)
	if err != nil {
		return "", err
	}
	return RenderClient(
		&ClientConf{
			Address:         spec.IP + "/32",
			DNS:             spec.DNS,
			MTU:             spec.MTU,
			PrivateKey:      spec.PrivateKey,
			Params:          c.ClientParams(),
			ServerPublicKey: serverKey,
			PresharedKey:    spec.PSK,
			Endpoint:        net.JoinHostPort(spec.EndpointHost, c.Get("ListenPort")),
		},
	), nil
}

// undo takes a peer back off after a failed change. A docker timeout can
// come after the command already ran in the container, so the peer may be
// there. It runs even when ctx is cancelled (the failure may be ctx
// itself); errors are logged, this is an error path already.
func (s *VPN) undo(ctx context.Context, in *takeOffInput) {
	ctx = context.WithoutCancel(ctx)
	if err := s.takeOff(ctx, in); err != nil {
		log.Printf("amnezia: roll back %s: %v", in.Peer.IP, err)
	}
	s.hideInApp(ctx, in.Peer)
}

// takeOff removes the peer from the config. A peer missing from the file
// costs no syncconf, unless the failure left the live interface ahead of
// the file (ErrNotPersisted): then the update re-syncs it from the file.
func (s *VPN) takeOff(ctx context.Context, in *takeOffInput) error {
	c, err := s.srv.ReadConf(ctx)
	if err != nil {
		return err
	}
	if c.FindPeer(in.Peer.PublicKey) == nil && !errors.Is(in.Cause, ErrNotPersisted) {
		return nil
	}
	return s.srv.Update(ctx, func(c *ServerConf) error {
		c.RemovePeer(in.Peer.PublicKey)
		return nil
	})
}

// showInApp lists the key in the Amnezia app. Failing here is not fatal:
// the key works, it is only missing from the app's list.
func (s *VPN) showInApp(ctx context.Context, p *service.VPNPeer) {
	err := s.srv.SetClient(
		ctx,
		ClientEntry{
			PublicKey:  p.PublicKey,
			Name:       p.Name,
			AllowedIPs: p.IP + "/32",
			CreatedAt:  p.CreatedAt,
		},
	)
	if err != nil {
		log.Printf("amnezia: clientsTable set %s: %v", p.IP, err)
	}
}

// hideInApp drops the key from the Amnezia app's list; a failure is logged.
func (s *VPN) hideInApp(ctx context.Context, p *service.VPNPeer) {
	if err := s.srv.RemoveClient(ctx, p.PublicKey); err != nil {
		log.Printf("amnezia: clientsTable remove %s: %v", p.IP, err)
	}
}

func serverPeer(p *service.VPNPeer) Peer {
	return Peer{
		PublicKey:    p.PublicKey,
		PresharedKey: p.PSK,
		AllowedIPs:   p.IP + "/32",
	}
}

// allowedIPs splits "10.8.1.2/32, fd00::2/128" into its entries.
func allowedIPs(list string) []string {
	parts := strings.Split(list, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
