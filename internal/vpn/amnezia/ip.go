package amnezia

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// FreeIP returns the lowest client IP in the subnet that is not the
// network, broadcast or server address, not used by a peer and not in
// reserved (IPs kept for disabled clients, which have no peer right now).
func (s *serverConf) FreeIP(reserved []netip.Addr) (netip.Addr, error) {
	server, taken, err := s.addresses(reserved)
	if err != nil {
		return netip.Addr{}, err
	}
	prefix := server.Masked()
	for ip := prefix.Addr().Next(); prefix.Contains(ip.Next()); ip = ip.Next() {
		if !taken[ip] {
			return ip, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("amnezia: no free IP in %s", prefix)
}

// SubnetUsage counts client IPs in use (peers + reserved) and the total
// number of client IPs the subnet can hold.
func (s *serverConf) SubnetUsage(reserved []netip.Addr) (used, total int, err error) {
	server, taken, err := s.addresses(reserved)
	if err != nil {
		return 0, 0, err
	}
	prefix := server.Masked()
	for ip := prefix.Addr().Next(); prefix.Contains(ip.Next()); ip = ip.Next() {
		total++
		if taken[ip] {
			used++
		}
	}
	if server.Addr() != prefix.Addr() {
		total-- // the server holds a host address, not the network one
		used--
	}
	return used, total, nil
}

// addresses returns the interface address (server IP + prefix) and the set of IPs already taken.
func (s *serverConf) addresses(reserved []netip.Addr) (netip.Prefix, map[netip.Addr]bool, error) {
	server, err := netip.ParsePrefix(strings.TrimSpace(strings.Split(s.Get("Address"), ",")[0]))
	if err != nil {
		return netip.Prefix{}, nil, fmt.Errorf("amnezia: bad interface Address: %w", err)
	}
	if !server.Addr().Is4() {
		return netip.Prefix{}, nil, errors.New("amnezia: only an IPv4 interface Address is supported")
	}
	taken := map[netip.Addr]bool{
		server.Addr(): true,
	}
	subnet := server.Masked()
	for _, p := range s.Peers {
		for _, pr := range peerPrefixes(p.AllowedIPs) {
			if pr.IsSingleIP() {
				taken[pr.Addr()] = true
				continue
			}
			// A wider prefix (a hand-made peer) holds every address in it.
			for ip := subnet.Addr(); subnet.Contains(ip); ip = ip.Next() {
				if pr.Contains(ip) {
					taken[ip] = true
				}
			}
		}
	}
	for _, ip := range reserved {
		taken[ip] = true
	}
	return server, taken, nil
}

// peerPrefixes reads an AllowedIPs list ("10.8.1.2/32, fd00::2/128"). A bare
// address is its single-address prefix; what is not an address is skipped.
func peerPrefixes(allowed string) []netip.Prefix {
	var out []netip.Prefix
	for _, entry := range strings.Split(allowed, ",") {
		entry = strings.TrimSpace(entry)
		pr, err := netip.ParsePrefix(entry)
		if err != nil {
			addr, addrErr := netip.ParseAddr(entry)
			if addrErr != nil {
				continue
			}
			pr = netip.PrefixFrom(addr, addr.BitLen())
		}
		out = append(out, pr.Masked())
	}
	return out
}
