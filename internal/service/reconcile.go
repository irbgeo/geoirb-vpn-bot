package service

import "context"

// Reconcile compares the DB with the server config: enabled keys must be
// on the server, disabled ones must not. It only reports; it never
// deletes or adds anything.
func (s *Service) Reconcile(ctx context.Context) (*ReconcileReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ours, err := s.peers.ByServer(ctx, s.cfg.ServerID)
	if err != nil {
		return nil, err
	}
	c, err := s.vpn.ReadConf(ctx)
	if err != nil {
		return nil, err
	}

	known := make(map[string]bool, len(ours))
	r := &ReconcileReport{}
	for _, p := range ours {
		known[p.PublicKey] = true
		onServer := c.FindPeer(p.PublicKey) != nil
		switch {
		case p.Enabled && !onServer:
			r.MissingOnServer = append(r.MissingOnServer, p)
		case !p.Enabled && onServer:
			r.DisabledButOnServer = append(r.DisabledButOnServer, p)
		}
	}
	for _, p := range c.Peers {
		if !known[p.PublicKey] {
			r.Manual++
		}
	}
	return r, nil
}
