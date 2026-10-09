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

// vpn is service.VPN on an Amnezia server. It keeps the config file, the
// live interface together, and
// undoes a change that failed half way before returning its error.
type vpn struct {
	srv *server
}

// NewVPN wraps an opened server.
func NewVPN(
	srv *server,
) *vpn {
	return &vpn{
		srv: srv,
	}
}

// GenKeys makes a fresh key set.
func (s *vpn) GenKeys(ctx context.Context) (service.VPNKeys, error) {
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
func (s *vpn) AddPeer(ctx context.Context, in *service.AddPeerInput) error {
	p := *in.Peer
	err := s.srv.Update(ctx, func(c *serverConf) error {
		ip, err := c.FreeIP(in.Reserved)
		if err != nil {
			return err
		}
		p.IP = ip.String()
		err = in.Save(p.IP)
		if err != nil {
			return err
		}
		c.AddPeer(serverPeer(&p))
		return nil
	})
	if err != nil {
		takeOffInput := &takeOffInput{
			Peer:  &p,
			Cause: err,
		}
		s.undo(ctx, takeOffInput)
		return err
	}
	return nil
}

// ReplacePeer swaps in.Old for in.New in one config update. On failure
// the new peer is taken off and the old one put back.
func (s *vpn) ReplacePeer(ctx context.Context, in *service.ReplacePeerInput) error {
	err := s.srv.Update(ctx, func(c *serverConf) error {
		c.RemovePeer(in.Old.PublicKey)
		c.AddPeer(serverPeer(in.New))
		return nil
	})
	if err != nil {
		takeOffInput := &takeOffInput{
			Peer:  in.New,
			Cause: err,
		}
		s.undo(ctx, takeOffInput)
		putErr := s.PutPeer(context.WithoutCancel(ctx), in.Old)
		if putErr != nil {
			log.Printf("amnezia: put %s back: %v", in.Old.IP, putErr)
		}
		return err
	}
	return nil
}

// PutPeer puts a known key back on its IP, unless another peer (e.g. one
// made in the Amnezia app) took the IP meanwhile: service.ErrIPTaken.
func (s *vpn) PutPeer(ctx context.Context, p *service.VPNPeer) error {
	err := s.srv.Update(ctx, func(c *serverConf) error {
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
		takeOffInput := &takeOffInput{
			Peer:  p,
			Cause: err,
		}
		s.undo(ctx, takeOffInput)
		return err
	}
	return nil
}

// RemovePeer takes a key off the server.
func (s *vpn) RemovePeer(ctx context.Context, p *service.VPNPeer) error {
	return s.srv.Update(ctx, func(c *serverConf) error {
		c.RemovePeer(p.PublicKey)
		return nil
	})
}

// PeerKeys returns the public key of every peer in the config.
func (s *vpn) PeerKeys(ctx context.Context) ([]string, error) {
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
func (s *vpn) SubnetUsage(ctx context.Context, reserved []netip.Addr) (used, total int, err error) {
	c, err := s.srv.ReadConf(ctx)
	if err != nil {
		return 0, 0, err
	}
	return c.SubnetUsage(reserved)
}

// Stats returns live data per peer. The server's RX is what the client
// sent, its TX what the client received.
func (s *vpn) Stats(ctx context.Context) ([]service.PeerStat, error) {
	stats, err := s.srv.Stats(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.PeerStat, 0, len(stats))
	for _, st := range stats {
		peerStat := service.PeerStat{
			PublicKey:     st.PublicKey,
			LastHandshake: st.LatestHandshake,
			Sent:          st.RX,
			Received:      st.TX,
		}
		out = append(out, peerStat)
	}
	return out, nil
}

// ClientConfig renders a client .conf with the server's obfuscation
// params, public key and port.
func (s *vpn) ClientConfig(ctx context.Context, spec *service.ClientSpec) (string, error) {
	c, err := s.srv.ReadConf(ctx)
	if err != nil {
		return "", err
	}
	serverKey, err := s.srv.ServerPublicKey(ctx)
	if err != nil {
		return "", err
	}
	clientConf := &clientConf{
		Address:         spec.IP + "/32",
		DNS:             spec.DNS,
		MTU:             spec.MTU,
		PrivateKey:      spec.PrivateKey,
		Params:          c.ClientParams(),
		ServerPublicKey: serverKey,
		PresharedKey:    spec.PSK,
		Endpoint:        net.JoinHostPort(spec.EndpointHost, c.Get("ListenPort")),
	}
	return RenderClient(clientConf), nil
}

func serverPeer(p *service.VPNPeer) peer {
	return peer{
		PublicKey:    p.PublicKey,
		PresharedKey: p.PSK,
		AllowedIPs:   p.IP + "/32",
	}
}

// undo takes a peer back off after a failed change. A docker timeout can
// come after the command already ran in the container, so the peer may be
// there. It runs even when ctx is cancelled (the failure may be ctx
// itself); errors are logged, this is an error path already.
func (s *vpn) undo(ctx context.Context, in *takeOffInput) {
	ctx = context.WithoutCancel(ctx)
	err := s.takeOff(ctx, in)
	if err != nil {
		log.Printf("amnezia: roll back %s: %v", in.Peer.IP, err)
	}
}

// takeOff removes the peer from the config. A peer missing from the file
// costs no syncconf, unless the failure left the live interface ahead of
// the file (ErrNotPersisted): then the update re-syncs it from the file.
func (s *vpn) takeOff(ctx context.Context, in *takeOffInput) error {
	c, err := s.srv.ReadConf(ctx)
	if err != nil {
		return err
	}
	if c.FindPeer(in.Peer.PublicKey) == nil && !errors.Is(in.Cause, ErrNotPersisted) {
		return nil
	}
	return s.srv.Update(ctx, func(c *serverConf) error {
		c.RemovePeer(in.Peer.PublicKey)
		return nil
	})
}

// allowedIPs splits "10.8.1.2/32, fd00::2/128" into its entries.
func allowedIPs(list string) []string {
	parts := strings.Split(list, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
