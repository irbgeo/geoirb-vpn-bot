package data

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitTunnelIsAnMP4TelegramAccepts(t *testing.T) {
	v := SplitTunnel()

	require.Greater(t, len(v), 8)
	require.Equal(t, "ftyp", string(v[4:8]), "an MP4 file")
	require.Less(t, len(v), 50<<20, "bots may upload up to 50 MB")
}
