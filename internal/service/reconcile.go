package service

import (
	"context"
	"log"
)

// Reconcile compares the DB with the server config: enabled keys must be
// on the server, disabled ones must not. It only reports; it never
// deletes or adds keys. It also resets users' KeysCount from the DB keys.
func (s *Service) Reconcile(ctx context.Context) (*ReconcileReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ours, err := s.peers.ByServer(ctx, s.cfg.ServerID)
	if err != nil {
		return nil, err
	}
	onServer, err := s.vpn.PeerKeys(ctx)
	if err != nil {
		return nil, err
	}
	live := make(map[string]bool, len(onServer))
	for _, k := range onServer {
		live[k] = true
	}

	known := make(map[string]bool, len(ours))
	counts := map[int64]int{}
	r := &ReconcileReport{}
	for _, p := range ours {
		if p.UserID != 0 {
			counts[p.UserID]++
		}
		known[p.PublicKey] = true
		switch {
		case p.Enabled && !live[p.PublicKey]:
			r.MissingOnServer = append(r.MissingOnServer, p)
		case !p.Enabled && live[p.PublicKey]:
			r.DisabledButOnServer = append(r.DisabledButOnServer, p)
		}
	}
	for _, k := range onServer {
		if !known[k] {
			r.Manual++
		}
	}
	// ponytail: counts from this server only; with several servers, sum
	// them across servers before setting.
	err = s.users.SetKeyCounts(ctx, counts)
	if err != nil {
		log.Printf("service: sync keys counts: %v", err)
	}
	r.MissingOnServer = publicAll(r.MissingOnServer)
	r.DisabledButOnServer = publicAll(r.DisabledButOnServer)
	return r, nil
}
