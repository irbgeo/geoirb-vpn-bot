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
func ParseServerConf(text string) (*serverConf, error) {
	c := &serverConf{}
	section := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.EqualFold(line, "[Interface]"):
			section = "interface"
		case strings.EqualFold(line, "[Peer]"):
			section = "peer"
			var p peer
			c.Peers = append(c.Peers, p)
		case line == "":
		case section == "" && strings.HasPrefix(line, "#"):
			c.Header = append(c.Header, line)
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
func RenderClient(in *clientConf) string {
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
func (s *serverConf) Get(key string) string {
	for _, line := range s.Interface {
		k, v, ok := splitKV(line)
		if ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// String renders the config back to file text.
func (s *serverConf) String() string {
	var b strings.Builder
	for _, line := range s.Header {
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("[Interface]\n")
	for _, line := range s.Interface {
		b.WriteString(line)
		b.WriteString("\n")
	}
	for i := range s.Peers {
		b.WriteString("\n")
		b.WriteString(s.Peers[i].String())
	}
	return b.String()
}

// Stripped renders the config without awg-quick-only keys and comments,
// ready for `awg syncconf`.
func (s *serverConf) Stripped() string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	for _, line := range s.Interface {
		k, _, ok := splitKV(line)
		if ok && !stripKeys[strings.ToLower(k)] {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	for i := range s.Peers {
		b.WriteString("\n")
		b.WriteString(s.Peers[i].String())
	}
	return b.String()
}

// ClientParams returns the obfuscation keys a client must share with the
// server: every [Interface] key except server-only ones, plus commented I1–I5.
func (s *serverConf) ClientParams() []kv {
	var out []kv
	for _, line := range s.Interface {
		k, v, ok := splitKV(line)
		if ok {
			if !serverOnlyKeys[strings.ToLower(k)] {
				kv := kv{
					Key:   k,
					Value: v,
				}
				out = append(out, kv)
			}
			continue
		}
		body, isComment := strings.CutPrefix(line, "#")
		if !isComment {
			continue
		}
		k, v, ok = splitKV(strings.TrimSpace(body))
		if ok && commentedClientKeys[strings.ToLower(k)] {
			kv := kv{
				Key:   k,
				Value: v,
			}
			out = append(out, kv)
		}
	}
	return out
}

// FindPeer returns the peer with this public key, or nil.
func (s *serverConf) FindPeer(publicKey string) *peer {
	for i := range s.Peers {
		if s.Peers[i].PublicKey == publicKey {
			return &s.Peers[i]
		}
	}
	return nil
}

// AddPeer appends a peer.
func (s *serverConf) AddPeer(p peer) {
	s.Peers = append(s.Peers, p)
}

// RemovePeer deletes the peer with this public key; false if not found.
func (s *serverConf) RemovePeer(publicKey string) bool {
	for i := range s.Peers {
		if s.Peers[i].PublicKey == publicKey {
			s.Peers = append(s.Peers[:i], s.Peers[i+1:]...)
			return true
		}
	}
	return false
}

// String renders the peer as a [Peer] section.
func (s *peer) String() string {
	var b strings.Builder
	b.WriteString("[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", s.PublicKey)
	if s.PresharedKey != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", s.PresharedKey)
	}
	fmt.Fprintf(&b, "AllowedIPs = %s\n", s.AllowedIPs)
	for _, line := range s.Other {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// addLine sets a known key from a config line, or keeps it verbatim.
func (s *peer) addLine(line string) {
	k, v, ok := splitKV(line)
	switch {
	case ok && strings.EqualFold(k, "PublicKey"):
		s.PublicKey = v
	case ok && strings.EqualFold(k, "PresharedKey"):
		s.PresharedKey = v
	case ok && strings.EqualFold(k, "AllowedIPs"):
		// A second AllowedIPs line adds to the first, as in awg itself.
		if s.AllowedIPs != "" {
			v = s.AllowedIPs + ", " + v
		}
		s.AllowedIPs = v
	default:
		s.Other = append(s.Other, line)
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
