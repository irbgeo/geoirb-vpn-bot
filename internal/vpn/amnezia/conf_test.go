package amnezia

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// serverConfText mirrors the real awg0.conf layout (keys are fake).
const serverConfText = `[Interface]
PrivateKey = SERVERPRIV=
Address = 10.8.1.0/24
ListenPort = 443
Jc = 6
Jmin = 10
Jmax = 50
S1 = 12
H1 = 1
HeaderProtectionKey = hpk+/=
RekeyAfterTime = 100-120
RandomTrailers = on
DisableCookies = on
# I1 = <r 2><b 0x8580>
[Peer]
PublicKey = PUB1=
PresharedKey = PSK1=
AllowedIPs = 10.8.1.1/32

[Peer]
PublicKey = PUB2=
PresharedKey = PSK2=
AllowedIPs = 10.8.1.2/32
`

func TestParseServerConf(t *testing.T) {
	c, err := ParseServerConf(serverConfText)
	require.NoError(t, err)

	require.Equal(t, "443", c.Get("ListenPort"))
	require.Equal(t, "10.8.1.0/24", c.Get("address"), "keys are case-insensitive")
	require.Equal(t, "", c.Get("I1"), "commented keys are not active")
	require.Len(t, c.Peers, 2)
	require.Equal(
		t,
		peer{
			PublicKey:    "PUB2=",
			PresharedKey: "PSK2=",
			AllowedIPs:   "10.8.1.2/32",
		},
		c.Peers[1],
	)
}

func TestParseServerConfErrors(t *testing.T) {
	_, err := ParseServerConf("[Peer]\nPublicKey = X\n")
	require.Error(t, err, "no [Interface]")

	_, err = ParseServerConf("[Interface]\nPrivateKey = X\n[Peer]\nAllowedIPs = 10.8.1.1/32\n")
	require.Error(t, err, "peer without PublicKey")
}

func TestServerConfRoundTrip(t *testing.T) {
	c, err := ParseServerConf(serverConfText)
	require.NoError(t, err)

	again, err := ParseServerConf(c.String())
	require.NoError(t, err)
	require.Equal(t, c, again)
	require.Contains(t, c.String(), "# I1 = <r 2><b 0x8580>", "comments are kept")
}

func TestAddRemovePeer(t *testing.T) {
	c, err := ParseServerConf(serverConfText)
	require.NoError(t, err)

	c.AddPeer(
		peer{
			PublicKey:    "PUB3=",
			PresharedKey: "PSK3=",
			AllowedIPs:   "10.8.1.3/32",
		},
	)
	require.Len(t, c.Peers, 3)
	require.NotNil(t, c.FindPeer("PUB3="))

	require.True(t, c.RemovePeer("PUB1="))
	require.False(t, c.RemovePeer("PUB1="), "already removed")
	require.Nil(t, c.FindPeer("PUB1="))
	require.Len(t, c.Peers, 2)
}

func TestClientParams(t *testing.T) {
	c, err := ParseServerConf(serverConfText)
	require.NoError(t, err)

	require.Equal(t, []kv{
		{
			Key:   "Jc",
			Value: "6",
		},
		{
			Key:   "Jmin",
			Value: "10",
		},
		{
			Key:   "Jmax",
			Value: "50",
		},
		{
			Key:   "S1",
			Value: "12",
		},
		{
			Key:   "H1",
			Value: "1",
		},
		{
			Key:   "HeaderProtectionKey",
			Value: "hpk+/=",
		},
		{
			Key:   "RekeyAfterTime",
			Value: "100-120",
		},
		{
			Key:   "RandomTrailers",
			Value: "on",
		},
		{
			Key:   "DisableCookies",
			Value: "on",
		},
		{
			Key:   "I1",
			Value: "<r 2><b 0x8580>",
		},
	}, c.ClientParams())
}

func TestStripped(t *testing.T) {
	c, err := ParseServerConf(serverConfText)
	require.NoError(t, err)

	s := c.Stripped()
	require.NotContains(t, s, "Address")
	require.NotContains(t, s, "#")
	require.Contains(t, s, "PrivateKey = SERVERPRIV=")
	require.Contains(t, s, "ListenPort = 443")
	require.Contains(t, s, "PublicKey = PUB2=")

	_, err = ParseServerConf(s)
	require.NoError(t, err, "stripped output is still a valid config")
}

func TestRenderClientMTU(t *testing.T) {
	got := RenderClient(
		&clientConf{
			Address: "10.8.1.10/32",
			DNS:     "1.1.1.1",
			MTU:     1380,
		},
	)

	require.Contains(t, got, "DNS = 1.1.1.1\nMTU = 1380\nPrivateKey = ")
}

func TestRenderClient(t *testing.T) {
	c, err := ParseServerConf(serverConfText)
	require.NoError(t, err)

	got := RenderClient(
		&clientConf{
			Address:         "10.8.1.10/32",
			DNS:             "1.1.1.1, 1.0.0.1",
			PrivateKey:      "CLIENTPRIV=",
			Params:          c.ClientParams(),
			ServerPublicKey: "SERVERPUB=",
			PresharedKey:    "PSK10=",
			Endpoint:        "vpn.example.com:443",
		},
	)

	require.NotContains(t, got, "MTU", "no MTU line when it is not set")
	require.Equal(t, `[Interface]
Address = 10.8.1.10/32
DNS = 1.1.1.1, 1.0.0.1
PrivateKey = CLIENTPRIV=
Jc = 6
Jmin = 10
Jmax = 50
S1 = 12
H1 = 1
HeaderProtectionKey = hpk+/=
RekeyAfterTime = 100-120
RandomTrailers = on
DisableCookies = on
I1 = <r 2><b 0x8580>

[Peer]
PublicKey = SERVERPUB=
PresharedKey = PSK10=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = vpn.example.com:443
PersistentKeepalive = 25-35
`, got)
}

func TestParseServerConfKeepsAHeaderComment(t *testing.T) {
	text := "# managed by hand, ask geo\n\n# second line\n" + serverConfText

	c, err := ParseServerConf(text)
	require.NoError(t, err)
	require.Equal(t, "SERVERPRIV=", c.Get("PrivateKey"))
	require.True(t, strings.HasPrefix(c.String(), "# managed by hand, ask geo\n# second line\n[Interface]\n"), c.String())
	require.True(t, strings.HasPrefix(c.Stripped(), "[Interface]\n"), "syncconf gets no comments")

	_, err = ParseServerConf("stray = 1\n" + serverConfText)
	require.ErrorContains(t, err, "outside a section")
}
