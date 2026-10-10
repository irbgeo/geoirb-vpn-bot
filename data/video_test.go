package data

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func requireMP4TelegramAccepts(t *testing.T, v []byte) {
	t.Helper()
	require.Greater(t, len(v), 8)
	require.Equal(t, "ftyp", string(v[4:8]), "an MP4 file")
	require.Less(t, len(v), 50<<20, "bots may upload up to 50 MB")
}

func TestAndroidSplitTunnelIsAnMP4TelegramAccepts(t *testing.T) {
	requireMP4TelegramAccepts(t, AndroidSplitTunnel())
}

// The iPhone video is added later: until then there is none, and that is
// not an error.
func TestIPhoneSplitTunnelIsMissingOrAnMP4TelegramAccepts(t *testing.T) {
	v := IPhoneSplitTunnel()
	if v == nil {
		return
	}
	requireMP4TelegramAccepts(t, v)
}
