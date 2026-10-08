package amnezia

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const tablePath = "/opt/amnezia/awg/clientsTable"

const clientsTable = `[
    {
        "clientId": "PUB1=",
        "userData": {
            "allowed_ips": "10.8.1.1/32",
            "clientName": "Admin <iOS>",
            "creationDate": "Thu Sep 10 03:40:35 2026",
            "dataReceived": "8.33 GiB"
        }
    }
]`

var persistPath = regexp.MustCompile(`f="([^"]+)"`)

// fileContainer fakes a container with files that `cat` reads and the
// persist script writes.
func fileContainer(files map[string]string) *fakeRunner {
	return &fakeRunner{handler: func(in execInput) (string, error) {
		if in.Args[0] == "cat" {
			return files[in.Args[1]], nil
		}
		m := persistPath.FindStringSubmatch(strings.Join(in.Args, " "))
		if m != nil {
			files[m[1]] = in.Stdin
		}
		return "", nil
	}}
}

func tableEntries(t *testing.T, text string) []map[string]any {
	t.Helper()
	var out []map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	return out
}

func TestSetClientAddsEntry(t *testing.T) {
	files := map[string]string{
		tablePath: clientsTable,
	}
	s := &Server{run: fileContainer(files)}

	err := s.SetClient(
		context.Background(),
		clientEntry{
			PublicKey:  "PUB2=",
			Name:       "tg:alice",
			AllowedIPs: "10.8.1.10/32",
			CreatedAt:  time.Date(2026, 9, 5, 7, 8, 9, 0, time.UTC),
		},
	)
	require.NoError(t, err)

	got := tableEntries(t, files[tablePath])
	require.Len(t, got, 2)
	require.Equal(
		t,
		map[string]any{
			"clientId": "PUB2=",
			"userData": map[string]any{
				"allowed_ips":  "10.8.1.10/32",
				"clientName":   "tg:alice",
				"creationDate": "Sat Sep 5 07:08:09 2026",
			},
		},
		got[1],
	)
	require.Equal(t, "8.33 GiB", got[0]["userData"].(map[string]any)["dataReceived"], "app fields kept")
	require.Contains(t, files[tablePath], `"Admin <iOS>"`, "no HTML escaping")
	require.Contains(t, files[tablePath], "\n    {", "4-space indent like the app")
}

func TestSetClientRenamesExisting(t *testing.T) {
	files := map[string]string{
		tablePath: clientsTable,
	}
	s := &Server{run: fileContainer(files)}

	err := s.SetClient(
		context.Background(),
		clientEntry{
			PublicKey:  "PUB1=",
			Name:       "tg:bob",
			AllowedIPs: "10.8.1.1/32",
			CreatedAt:  time.Now(),
		},
	)
	require.NoError(t, err)

	got := tableEntries(t, files[tablePath])
	require.Len(t, got, 1)
	ud := got[0]["userData"].(map[string]any)
	require.Equal(t, "tg:bob", ud["clientName"])
	require.Equal(t, "Thu Sep 10 03:40:35 2026", ud["creationDate"], "creation date kept")
}

func TestRemoveClient(t *testing.T) {
	files := map[string]string{
		tablePath: clientsTable,
	}
	s := &Server{run: fileContainer(files)}

	require.NoError(t, s.RemoveClient(context.Background(), "PUB1="))
	require.Empty(t, tableEntries(t, files[tablePath]))
}

func TestRemoveUnknownClientWritesNothing(t *testing.T) {
	files := map[string]string{
		tablePath: clientsTable,
	}
	r := fileContainer(files)
	s := &Server{run: r}

	require.NoError(t, s.RemoveClient(context.Background(), "NOPE="))
	require.Equal(t, []string{"cat " + tablePath}, r.cmds())
}

func TestBadClientsTable(t *testing.T) {
	files := map[string]string{
		tablePath: "not json",
	}
	s := &Server{run: fileContainer(files)}

	err := s.RemoveClient(context.Background(), "PUB1=")
	require.ErrorContains(t, err, "clientsTable")
}
