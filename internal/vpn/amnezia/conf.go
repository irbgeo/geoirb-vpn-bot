package amnezia

import (
	"errors"
	"fmt"
	"strings"
)

// serverOnlyKeys never go into a client config.
var serverOnlyKeys = map[string]bool{
	"privatekey": true,
	"listenport": true,
	"address":    true,
	"dns":        true,
	"mtu":        true,
	"table":      true,
	"fwmark":     true,
	"preup":      true,
	"predown":    true,
	"postup":     true,
	"postdown":   true,
	"saveconfig": true,
}

// stripKeys are the awg-quick-only keys that `awg syncconf` rejects.
var stripKeys = map[string]bool{
	"address":    true,
	"dns":        true,
	"mtu":        true,
	"table":      true,
	"preup":      true,
	"predown":    true,
	"postup":     true,
	"postdown":   true,
	"saveconfig": true,
}

// commentedClientKeys are special-junk keys the Amnezia app keeps commented
// out on the server but puts active into client configs.
var commentedClientKeys = map[string]bool{
	"i1": true,
	"i2": true,
	"i3": true,
	"i4": true,
	"i5": true,
}

// ParseServerConf parses awg0.conf text.
func ParseServerConf(text string) (*ServerConf, error) {
	c := &ServerConf{}
	section := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.EqualFold(line, "[Interface]"):
			section = "interface"
		case strings.EqualFold(line, "[Peer]"):
			section = "peer"
			c.Peers = append(c.Peers, Peer{})
		case line == "":
		case section == "interface":
			c.Interface = append(c.Interface, line)
		case section == "peer":
			c.Peers[len(c.Peers)-1].addLine(line)
		default:
			return nil, fmt.Errorf("amnezia: line outside a section: %q", line)
		}
	}
	if len(c.Interface) == 0 {
		return nil, errors.New("amnezia: config has no [Interface]")
	}
	for i, p := range c.Peers {
		if p.PublicKey == "" {
			return nil, fmt.Errorf("amnezia: peer #%d has no PublicKey", i+1)
		}
	}
	return c, nil
}

// RenderClient builds the client .conf text.
func RenderClient(in *ClientConf) string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "Address = %s\n", in.Address)
	fmt.Fprintf(&b, "DNS = %s\n", in.DNS)
	if in.MTU > 0 {
		fmt.Fprintf(&b, "MTU = %d\n", in.MTU)
	}
	fmt.Fprintf(&b, "PrivateKey = %s\n", in.PrivateKey)
	for _, kv := range in.Params {
		fmt.Fprintf(&b, "%s = %s\n", kv.Key, kv.Value)
	}
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", in.ServerPublicKey)
	fmt.Fprintf(&b, "PresharedKey = %s\n", in.PresharedKey)
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	fmt.Fprintf(&b, "Endpoint = %s\n", in.Endpoint)
	// Range syntax is AmneziaWG 2.0; same value the Amnezia app uses.
	b.WriteString("PersistentKeepalive = 25-35\n")
	return b.String()
}

// Get returns the value of an active [Interface] key, or "".
func (c *ServerConf) Get(key string) string {
	for _, line := range c.Interface {
		if k, v, ok := splitKV(line); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// String renders the config back to file text.
func (c *ServerConf) String() string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	for _, line := range c.Interface {
		b.WriteString(line)
		b.WriteString("\n")
	}
	for i := range c.Peers {
		b.WriteString("\n")
		b.WriteString(c.Peers[i].String())
	}
	return b.String()
}

// Stripped renders the config without awg-quick-only keys and comments,
// ready for `awg syncconf`.
func (c *ServerConf) Stripped() string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	for _, line := range c.Interface {
		if k, _, ok := splitKV(line); ok && !stripKeys[strings.ToLower(k)] {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	for i := range c.Peers {
		b.WriteString("\n")
		b.WriteString(c.Peers[i].String())
	}
	return b.String()
}

// ClientParams returns the obfuscation keys a client must share with the
// server: every [Interface] key except server-only ones, plus commented I1–I5.
func (c *ServerConf) ClientParams() []KV {
	var out []KV
	for _, line := range c.Interface {
		if k, v, ok := splitKV(line); ok {
			if !serverOnlyKeys[strings.ToLower(k)] {
				out = append(
					out,
					KV{
						Key:   k,
						Value: v,
					},
				)
			}
			continue
		}
		body, isComment := strings.CutPrefix(line, "#")
		if !isComment {
			continue
		}
		if k, v, ok := splitKV(strings.TrimSpace(body)); ok && commentedClientKeys[strings.ToLower(k)] {
			out = append(
				out,
				KV{
					Key:   k,
					Value: v,
				},
			)
		}
	}
	return out
}

// FindPeer returns the peer with this public key, or nil.
func (c *ServerConf) FindPeer(publicKey string) *Peer {
	for i := range c.Peers {
		if c.Peers[i].PublicKey == publicKey {
			return &c.Peers[i]
		}
	}
	return nil
}

// AddPeer appends a peer.
func (c *ServerConf) AddPeer(p Peer) {
	c.Peers = append(c.Peers, p)
}

// RemovePeer deletes the peer with this public key; false if not found.
func (c *ServerConf) RemovePeer(publicKey string) bool {
	for i := range c.Peers {
		if c.Peers[i].PublicKey == publicKey {
			c.Peers = append(c.Peers[:i], c.Peers[i+1:]...)
			return true
		}
	}
	return false
}

// String renders the peer as a [Peer] section.
func (p *Peer) String() string {
	var b strings.Builder
	b.WriteString("[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", p.PublicKey)
	if p.PresharedKey != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", p.PresharedKey)
	}
	fmt.Fprintf(&b, "AllowedIPs = %s\n", p.AllowedIPs)
	for _, line := range p.Other {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// addLine sets a known key from a config line, or keeps it verbatim.
func (p *Peer) addLine(line string) {
	k, v, ok := splitKV(line)
	switch {
	case ok && strings.EqualFold(k, "PublicKey"):
		p.PublicKey = v
	case ok && strings.EqualFold(k, "PresharedKey"):
		p.PresharedKey = v
	case ok && strings.EqualFold(k, "AllowedIPs"):
		p.AllowedIPs = v
	default:
		p.Other = append(p.Other, line)
	}
}

// splitKV splits "Key = Value" at the first '='. Base64 values keep their
// trailing '=' padding. Comment lines are not key/value lines.
func splitKV(line string) (key, value string, ok bool) {
	if strings.HasPrefix(line, "#") {
		return "", "", false
	}
	k, v, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	return strings.TrimSpace(k), strings.TrimSpace(v), true
}
