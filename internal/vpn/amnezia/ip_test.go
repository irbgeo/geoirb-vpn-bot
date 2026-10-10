package amnezia

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFreeIPSkipsServerPeersAndReserved(t *testing.T) {
	c, err := ParseServerConf(serverConfText) // server .0, peers .1 and .2
	require.NoError(t, err)

	ip, err := c.FreeIP(nil)
	require.NoError(t, err)
	require.Equal(t, netip.MustParseAddr("10.8.1.3"), ip)

	ip, err = c.FreeIP(
		[]netip.Addr{
			netip.MustParseAddr("10.8.1.3"),
			netip.MustParseAddr("10.8.1.4"),
		},
	)
	require.NoError(t, err)
	require.Equal(t, netip.MustParseAddr("10.8.1.5"), ip, "IPs of disabled clients stay reserved")
}

func TestFreeIPReusesGaps(t *testing.T) {
	c, err := ParseServerConf(serverConfText)
	require.NoError(t, err)
	c.RemovePeer("PUB1=")

	ip, err := c.FreeIP(nil)
	require.NoError(t, err)
	require.Equal(t, netip.MustParseAddr("10.8.1.1"), ip)
}

func TestFreeIPFullSubnet(t *testing.T) {
	c, err := ParseServerConf("[Interface]\nAddress = 10.8.1.0/30\n[Peer]\nPublicKey = A\nAllowedIPs = 10.8.1.1/32\n[Peer]\nPublicKey = B\nAllowedIPs = 10.8.1.2/32\n")
	require.NoError(t, err)

	_, err = c.FreeIP(nil)
	require.ErrorContains(t, err, "no free IP", ".3 is broadcast, never given out")
}

func TestSubnetUsage(t *testing.T) {
	c, err := ParseServerConf(serverConfText)
	require.NoError(t, err)

	used, total, err := c.SubnetUsage([]netip.Addr{netip.MustParseAddr("10.8.1.9")})
	require.NoError(t, err)
	require.Equal(t, 3, used, "2 peers + 1 reserved")
	require.Equal(t, 254, total, "/24: .1–.254 (server sits on .0 here)")
}

// Hand-made peers do not always write "<ip>/32".
func TestFreeIPSeesEveryAllowedIPsForm(t *testing.T) {
	cases := []struct {
		name    string
		allowed string
		want    string
		used    int
	}{
		{
			name:    "bare address",
			allowed: "10.8.0.1",
			want:    "10.8.0.2",
			used:    1,
		},
		{
			name:    "wider prefix",
			allowed: "10.8.1.0/24",
			want:    "10.8.0.1",
			used:    256,
		},
		{
			name:    "wider prefix written from a host address",
			allowed: "10.8.0.1/30",
			want:    "10.8.0.4",
			used:    3, // .1–.3; .0 is the server
		},
		{
			name:    "list with IPv6 and junk",
			allowed: "fd00::2/128, junk, 10.8.0.1/32, 10.8.0.2",
			want:    "10.8.0.3",
			used:    2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ParseServerConf("[Interface]\nAddress = 10.8.0.0/22\n[Peer]\nPublicKey = A\nAllowedIPs = " + tc.allowed + "\n")
			require.NoError(t, err)

			ip, err := c.FreeIP(nil)
			require.NoError(t, err)
			require.Equal(t, netip.MustParseAddr(tc.want), ip)

			used, total, err := c.SubnetUsage(nil)
			require.NoError(t, err)
			require.Equal(t, tc.used, used)
			require.Equal(t, 1022, total)
		})
	}
}
