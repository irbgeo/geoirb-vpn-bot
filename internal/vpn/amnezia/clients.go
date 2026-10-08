package amnezia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// creationDateLayout matches the app's "Sat Sep 26 10:19:20 2026".
const creationDateLayout = "Mon Jan 2 15:04:05 2006"

// SetClient adds a client to clientsTable, or renames it if present
// (creation date and app-written stats are kept).
func (s *server) SetClient(ctx context.Context, e clientEntry) error {
	return s.updateTable(ctx, func(list []tableEntry) ([]tableEntry, bool) {
		for i := range list {
			if list[i].ClientID == e.PublicKey {
				list[i].UserData["clientName"] = e.Name
				list[i].UserData["allowed_ips"] = e.AllowedIPs
				return list, true
			}
		}
		tableEntry := tableEntry{
			ClientID: e.PublicKey,
			UserData: map[string]any{
				"clientName":   e.Name,
				"allowed_ips":  e.AllowedIPs,
				"creationDate": e.CreatedAt.Format(creationDateLayout),
			},
		}
		return append(list, tableEntry), true
	})
}

// RemoveClient deletes a client from clientsTable; unknown keys are a no-op.
func (s *server) RemoveClient(ctx context.Context, publicKey string) error {
	return s.updateTable(ctx, func(list []tableEntry) ([]tableEntry, bool) {
		for i := range list {
			if list[i].ClientID == publicKey {
				return append(list[:i], list[i+1:]...), true
			}
		}
		return list, false
	})
}

// updateTable reads clientsTable, applies fn and saves the result when fn
// reports a change.
func (s *server) updateTable(ctx context.Context, fn func([]tableEntry) ([]tableEntry, bool)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := path.Join(confDir, "clientsTable")
	text, err := s.run.Exec(ctx, cmd("cat", p))
	if err != nil {
		return err
	}
	var list []tableEntry
	if strings.TrimSpace(text) != "" {
		err = json.Unmarshal([]byte(text), &list)
		if err != nil {
			return fmt.Errorf("amnezia: bad clientsTable: %w", err)
		}
	}
	for i := range list {
		if list[i].UserData == nil {
			list[i].UserData = map[string]any{}
		}
	}

	list, changed := fn(list)
	if !changed {
		return nil
	}
	out, err := marshalTable(list)
	if err != nil {
		return err
	}
	persistInput := persistInput{
		Path:    p,
		Content: out,
		Expect:  sha256Hex(text),
	}
	return s.persist(ctx, persistInput)
}

// marshalTable writes JSON the way the app does: 4-space indent, no HTML
// escaping of <, >, &.
func marshalTable(list []tableEntry) (string, error) {
	if list == nil {
		list = []tableEntry{}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "    ")
	err := enc.Encode(list)
	if err != nil {
		return "", fmt.Errorf("amnezia: encode clientsTable: %w", err)
	}
	return b.String(), nil
}
