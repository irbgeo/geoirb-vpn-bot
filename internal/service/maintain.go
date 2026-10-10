package service

import (
	"context"
	"errors"
	"log"
	"time"
)

// Reminder windows before a key's end.
const (
	remind3d = 72 * time.Hour
	remind1d = 24 * time.Hour
)

// Maintain is the periodic job: it disables keys whose term ended, picks
// keys that need a "3 days" / "1 day" reminder, and measures the subnet.
// Each reminder is marked sent before it is returned: a failed send loses
// it rather than repeating it every minute. Under a day left, only the
// 1-day reminder goes out.
// A failed subnet reading is logged and left at zero: the key notices
// matter more, and their marks are already saved.
// ponytail: scans every key of the server each run — the subnet bounds it (a /22 is ~1000 keys).
func (s *service) Maintain(ctx context.Context) (*Maintenance, error) {
	m, err := s.maintainKeys(ctx)
	if err != nil {
		return nil, err
	}
	m.SubnetUsed, m.SubnetTotal, err = s.subnetUsage(ctx)
	if err != nil {
		log.Printf("service: subnet usage: %v", err)
		m.SubnetUsed, m.SubnetTotal = 0, 0
	}
	m.Online, err = s.onlineCount(ctx)
	if err != nil {
		log.Printf("service: online count: %v", err)
		m.Online = -1
	}
	m.MadeForever = publicAll(m.MadeForever)
	m.Expired = publicAll(m.Expired)
	m.Remind3d = publicAll(m.Remind3d)
	m.Remind1d = publicAll(m.Remind1d)
	return m, nil
}

// maintainKeys expires keys and picks reminders, under s.mu.
func (s *service) maintainKeys(ctx context.Context) (*Maintenance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ps, err := s.peers.ByServer(ctx, s.cfg.ServerID)
	if err != nil {
		return nil, err
	}
	forever, err := s.foreverOwners(ctx)
	if err != nil {
		return nil, err
	}
	m := &Maintenance{}
	for _, p := range ps {
		maintainStep := maintainStep{
			Peer:   p,
			Report: m,
		}
		var stop bool
		if forever[p.UserID] && !p.ExpiresAt.IsZero() {
			stop = s.maintainForever(ctx, maintainStep)
		} else {
			stop = s.maintainTerm(ctx, maintainStep)
		}
		if stop {
			break // the server is likely down: the next run retries
		}
	}
	return m, nil
}

// foreverOwners are the users whose keys never expire: unlimited and
// admin. The role is set by hand in the DB, so a key issued with an end
// date (e.g. a trial) is fixed here, within a minute of the change.
func (s *service) foreverOwners(ctx context.Context) (map[int64]bool, error) {
	ids := map[int64]bool{}
	for _, r := range []Role{
		RoleUnlimited,
		RoleAdmin,
	} {
		us, err := s.users.ByRole(ctx, r)
		if err != nil {
			return nil, err
		}
		for _, u := range us {
			ids[u.ID] = true
		}
	}
	return ids, nil
}

// maintainForever handles a timed key of an owner whose keys never
// expire: the end date goes and the key comes back on, unless an admin
// blocked it. It reports true when the run must stop: the error was not
// about this one key.
func (s *service) maintainForever(ctx context.Context, in maintainStep) bool {
	p := in.Peer
	if p.Blocked { // the admin block stays: only the end date goes
		dropEnd(p)
		s.savePeer(ctx, p)
		return false
	}
	err := s.makeForever(ctx, p)
	if err == nil {
		in.Report.MadeForever = append(in.Report.MadeForever, p)
		return false
	}
	// A skip kind: only this key can't go back on, the rest go on.
	kind := skipKind(err)
	logKey := logKey{
		PublicKey: p.PublicKey,
		Kind:      kind,
	}
	if kind == "" || s.skipLogged.first(logKey) {
		log.Printf("service: make %s forever: %v", p.IP, err)
	}
	return kind == ""
}

// dropEnd makes a key never expire and clears its reminders.
func dropEnd(p *Peer) {
	p.ExpiresAt = time.Time{}
	p.Reminded3d = false
	p.Reminded1d = false
}

// savePeer saves a reminder mark; false (logged) if it failed, so the
// reminder is not sent and is tried again next run.
func (s *service) savePeer(ctx context.Context, p *Peer) bool {
	err := s.peers.Save(ctx, p)
	if err != nil {
		log.Printf("service: save reminder for %s: %v", p.IP, err)
		return false
	}
	return true
}

// makeForever drops a key's end date and reminders, and puts it back on
// the server (same keys, same IP) if it was disabled. The caller holds s.mu.
func (s *service) makeForever(ctx context.Context, p *Peer) error {
	dropEnd(p)
	return s.enableAndSave(ctx, p)
}

// skipKind names an error that skips only one key in maintainKeys ("" for
// any other error). It keys the once-only log, so a changing message does
// not log again.
func skipKind(err error) string {
	switch {
	case errors.Is(err, ErrIPTaken):
		return "ip-taken"
	case errors.Is(err, ErrUnreadable):
		return "unreadable"
	}
	return ""
}

// maintainTerm handles a key with an end date: it is disabled when the
// term ended, or gets the reminder that is due. It reports true when the
// run must stop.
func (s *service) maintainTerm(ctx context.Context, in maintainStep) bool {
	p := in.Peer
	if !p.Enabled || p.ExpiresAt.IsZero() {
		return false
	}
	left := time.Until(p.ExpiresAt)
	switch {
	case left <= 0:
		err := s.disablePeer(ctx, p)
		if err != nil {
			// The server is likely down: stop instead of waiting a command
			// timeout per key.
			log.Printf("service: expire %s: %v", p.IP, err)
			return true
		}
		in.Report.Expired = append(in.Report.Expired, p)
	case left <= remind1d && !p.Reminded1d:
		p.Reminded1d, p.Reminded3d = true, true
		if s.savePeer(ctx, p) {
			in.Report.Remind1d = append(in.Report.Remind1d, p)
		}
	case left <= remind3d && !p.Reminded3d:
		p.Reminded3d = true
		if s.savePeer(ctx, p) {
			in.Report.Remind3d = append(in.Report.Remind3d, p)
		}
	}
	return false
}

// subnetUsage counts taken client IPs: peers on the server plus the IPs
// reserved for the bot's keys (disabled ones have no peer).
func (s *service) subnetUsage(ctx context.Context) (used, total int, err error) {
	reserved, err := s.reservedIPs(ctx)
	if err != nil {
		return 0, 0, err
	}
	return s.vpn.SubnetUsage(ctx, reserved)
}
