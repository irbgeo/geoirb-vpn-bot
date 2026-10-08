package bypass

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListsAreBuiltInAmneziaImportFormat(t *testing.T) {
	files, err := New().Files(context.Background())
	require.NoError(t, err)

	var names []string
	for _, f := range files {
		names = append(names, f.Name)
		var entries []struct {
			Hostname string `json:"hostname"`
		}
		require.NoError(t, json.Unmarshal(f.Data, &entries), f.Name)
		require.NotEmpty(t, entries, f.Name)
		for _, e := range entries {
			require.NotEmpty(t, e.Hostname, "every entry has a hostname or a network: %s", f.Name)
		}
	}
	require.Equal(
		t,
		[]string{
			ComputerList,
			PhoneList,
		},
		names,
	)
}

func TestListsAreCopies(t *testing.T) {
	a, err := New().Files(context.Background())
	require.NoError(t, err)
	a[0].Data[0] = 'X'

	b, err := New().Files(context.Background())
	require.NoError(t, err)
	require.Equal(t, byte('['), b[0].Data[0], "a caller can't change the lists for the next one")
}

// Entries that must never be in a list: our client DNS servers (a bypass
// would send users' DNS queries past the VPN) and ranges so wide they take
// many non-Russian sites past the VPN too.
var forbidden = []string{
	"1.1.1.1",
	"1.0.0.1",
	"194.0.0.0/8",
	"193.0.0.0/9",
	"77.88.0.0/13",
	"217.0.0.0/13",
}

func TestListsHaveNoDNSServersOrOverlyWideRanges(t *testing.T) {
	files, err := New().Files(context.Background())
	require.NoError(t, err)
	for _, f := range files {
		var entries []struct {
			Hostname string   `json:"hostname"`
			IP       string   `json:"ip"`
			IPs      []string `json:"ips"`
		}
		require.NoError(t, json.Unmarshal(f.Data, &entries))
		for _, e := range entries {
			for _, v := range append([]string{e.Hostname, e.IP}, e.IPs...) {
				require.NotContains(t, forbidden, v, "%s: entry %q", f.Name, e.Hostname)
			}
		}
	}
}
