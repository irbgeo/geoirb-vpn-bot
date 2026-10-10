package service

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"
)

// --- repositories: maps, copy on get/save so tests can't alias state ---

type fakeUsers struct {
	m        map[int64]User
	trialErr error // SetTrialUsed fails
}

func (s *fakeUsers) Get(_ context.Context, id int64) (*User, error) {
	u, ok := s.m[id]
	if !ok {
		return nil, nil
	}
	return &u, nil
}

// Register mirrors the store's upsert: a new user gets u as given; an
// existing one only gets the new username (role and the rest are kept).
func (s *fakeUsers) Register(_ context.Context, u *User) (*User, error) {
	got, ok := s.m[u.ID]
	if !ok {
		got = *u
	}
	got.Username = u.Username
	s.m[u.ID] = got
	return &got, nil
}

func (s *fakeUsers) AddKeys(_ context.Context, d KeysDelta) error {
	u, ok := s.m[d.UserID]
	if !ok {
		return nil // like the store: no row, nothing to count
	}
	u.KeysCount += d.Delta
	s.m[d.UserID] = u
	return nil
}

func (s *fakeUsers) SetKeyCounts(_ context.Context, counts map[int64]int) error {
	for id, u := range s.m {
		u.KeysCount = counts[id]
		s.m[id] = u
	}
	return nil
}

func (s *fakeUsers) SetTrialUsed(_ context.Context, id int64) error {
	if s.trialErr != nil {
		return s.trialErr
	}
	u := s.m[id]
	u.TrialUsed = true
	s.m[id] = u
	return nil
}

func (s *fakeUsers) ByRole(_ context.Context, r Role) ([]*User, error) {
	var out []*User
	for _, u := range s.m {
		if u.Role == r {
			out = append(out, &u)
		}
	}
	return out, nil
}

func (s *fakeUsers) List(_ context.Context, p Page) ([]*User, int64, error) {
	all := make([]*User, 0, len(s.m))
	for _, u := range s.m {
		all = append(all, &u)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	end := min(p.Skip+p.Limit, int64(len(all)))
	return all[min(p.Skip, end):end], int64(len(all)), nil
}

type fakeFeedback struct {
	saved []Feedback
}

func (s *fakeFeedback) Add(_ context.Context, fb *Feedback) error {
	s.saved = append(s.saved, *fb)
	return nil
}

// List returns the saved feedback newest first (the last added first).
func (s *fakeFeedback) List(_ context.Context, p Page) ([]*Feedback, int64, error) {
	all := make([]*Feedback, 0, len(s.saved))
	for i := len(s.saved) - 1; i >= 0; i-- {
		all = append(all, &s.saved[i])
	}
	end := min(p.Skip+p.Limit, int64(len(all)))
	return all[min(p.Skip, end):end], int64(len(all)), nil
}

type fakePeers struct {
	m       map[string]Peer
	saveErr error
	// saveLost: Save stores the row, then returns this (the reply was lost).
	saveLost error
	getErr   error
	// replaceErr: the next Replace removes the old row, then fails (the
	// worst case); the one after works.
	replaceErr error
}

func (s *fakePeers) Get(_ context.Context, key string) (*Peer, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	p, ok := s.m[key]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func (s *fakePeers) Save(_ context.Context, p *Peer) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.m[p.PublicKey] = *p
	return s.saveLost
}

func (s *fakePeers) Delete(ctx context.Context, key string) error {
	err := ctx.Err()
	if err != nil {
		return err // like a real DB call on a cancelled context
	}
	delete(s.m, key)
	return nil
}

func (s *fakePeers) Replace(_ context.Context, in PeerSwap) error {
	delete(s.m, in.Old.PublicKey)
	err := s.replaceErr
	s.replaceErr = nil
	if err != nil {
		return err
	}
	s.m[in.New.PublicKey] = *in.New
	return nil
}

func (s *fakePeers) ByUser(_ context.Context, userID int64) ([]*Peer, error) {
	var out []*Peer
	for _, p := range s.m {
		if p.UserID == userID {
			out = append(out, &p)
		}
	}
	return out, nil
}

func (s *fakePeers) ServerIPs(ctx context.Context, serverID string) ([]string, error) {
	ps, _ := s.ByServer(ctx, serverID)
	ips := make([]string, 0, len(ps))
	for _, p := range ps {
		ips = append(ips, p.IP)
	}
	return ips, nil
}

func (s *fakePeers) ByServer(_ context.Context, serverID string) ([]*Peer, error) {
	var out []*Peer
	for _, p := range s.m {
		if p.ServerID == serverID {
			out = append(out, &p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out, nil
}

type fakePayments struct {
	m         map[string]Payment
	saveErr   error
	saveFails int // fail this many Saves, then work
	// addTaken: Add finds the charge already recorded (someone else wrote
	// it after Get saw nothing).
	addTaken bool
}

func (s *fakePayments) Add(_ context.Context, p *Payment) (bool, error) {
	_, ok := s.m[p.ChargeID]
	if ok || s.addTaken {
		return false, nil
	}
	s.m[p.ChargeID] = *p
	return true, nil
}

func (s *fakePayments) Get(_ context.Context, id string) (*Payment, error) {
	p, ok := s.m[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

// MarkApplied / MarkRefunded change only their own fields, like the store.
func (s *fakePayments) MarkApplied(_ context.Context, m PaymentMark) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	if s.saveFails > 0 {
		s.saveFails--
		return errBoom
	}
	p, ok := s.m[m.ChargeID]
	if !ok {
		return errors.New("no payment")
	}
	p.Applied = true
	p.PeerKey = m.PeerKey
	s.m[m.ChargeID] = p
	return nil
}

func (s *fakePayments) MarkRefunded(_ context.Context, m PaymentMark) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	p, ok := s.m[m.ChargeID]
	if !ok {
		return nil
	}
	p.RefundedAt = m.At
	s.m[m.ChargeID] = p
	return nil
}

func (s *fakePayments) ByUser(_ context.Context, userID int64) ([]*Payment, error) {
	var out []*Payment
	for _, p := range s.m {
		if p.UserID == userID {
			out = append(out, &p)
		}
	}
	return out, nil
}

func (s *fakePayments) Since(_ context.Context, t time.Time) ([]*Payment, error) {
	var out []*Payment
	for _, p := range s.m {
		if !p.CreatedAt.Before(t) {
			out = append(out, &p)
		}
	}
	return out, nil
}

// --- VPN server: config text in memory ---

// fakeVPN is the VPN port in memory. Like the real one, a change either
// happens or fails with nothing changed. The server starts with one peer
// made in the Amnezia app: MANUAL1= on 10.8.1.1.
type fakeVPN struct {
	peers    map[string]VPNPeer // on the server, by public key
	stats    []PeerStat
	keys     int
	changes  int    // calls that change the server
	err      error  // every change fails
	onChange func() // runs inside a change, before err
	readErr  error  // PeerKeys and SubnetUsage fail
	statsErr error  // Stats fails
}

func newFakeVPN() *fakeVPN {
	return &fakeVPN{
		peers: map[string]VPNPeer{
			"MANUAL1=": {
				PublicKey: "MANUAL1=",
				IP:        "10.8.1.1",
			},
		},
	}
}

func (s *fakeVPN) GenKeys(context.Context) (VPNKeys, error) {
	s.keys++
	return VPNKeys{
		Private: fmt.Sprintf("PRIV%d=", s.keys),
		Public:  fmt.Sprintf("PUB%d=", s.keys),
		PSK:     fmt.Sprintf("PSK%d=", s.keys),
	}, nil
}

func (s *fakeVPN) AddPeer(_ context.Context, in *AddPeerInput) error {
	s.changes++
	p := *in.Peer
	p.IP = s.freeIP(in.Reserved)
	err := in.Save(p.IP)
	if err != nil {
		return err
	}
	err = s.fail()
	if err != nil {
		return err
	}
	s.put(&p)
	return nil
}

func (s *fakeVPN) PutPeer(_ context.Context, p *VPNPeer) error {
	s.changes++
	_, ok := s.peers[p.PublicKey]
	if ok {
		return nil
	}
	for _, other := range s.peers {
		if other.IP == p.IP {
			return fmt.Errorf("%w: %s", ErrIPTaken, p.IP)
		}
	}
	err := s.fail()
	if err != nil {
		return err
	}
	s.put(p)
	return nil
}

func (s *fakeVPN) RemovePeer(_ context.Context, p *VPNPeer) error {
	s.changes++
	err := s.fail()
	if err != nil {
		return err
	}
	delete(s.peers, p.PublicKey)
	return nil
}

func (s *fakeVPN) ReplacePeer(_ context.Context, in *ReplacePeerInput) error {
	s.changes++
	err := s.fail()
	if err != nil {
		return err
	}
	delete(s.peers, in.Old.PublicKey)
	s.put(in.New)
	return nil
}

func (s *fakeVPN) PeerKeys(context.Context) ([]string, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	out := make([]string, 0, len(s.peers))
	for k := range s.peers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (s *fakeVPN) SubnetUsage(_ context.Context, reserved []netip.Addr) (used, total int, err error) {
	if s.readErr != nil {
		return 0, 0, s.readErr
	}
	return len(s.taken(reserved)), 254, nil
}

func (s *fakeVPN) Stats(context.Context) ([]PeerStat, error) {
	return s.stats, s.statsErr
}

// ClientConfig renders a stand-in config from the spec.
func (s *fakeVPN) ClientConfig(_ context.Context, c *ClientSpec) (string, error) {
	return fmt.Sprintf(
		"Address = %s/32\nDNS = %s\nMTU = %d\nPrivateKey = %s\nPresharedKey = %s\nEndpoint = %s\n",
		c.IP,
		c.DNS,
		c.MTU,
		c.PrivateKey,
		c.PSK,
		c.EndpointHost,
	), nil
}

// fail runs onChange and returns err: the change is not made.
func (s *fakeVPN) fail() error {
	if s.onChange != nil {
		s.onChange()
	}
	return s.err
}

func (s *fakeVPN) put(p *VPNPeer) {
	s.peers[p.PublicKey] = *p
}

// freeIP is the lowest 10.8.1.x not on the server and not reserved.
func (s *fakeVPN) freeIP(reserved []netip.Addr) string {
	taken := s.taken(reserved)
	for i := 1; ; i++ {
		ip := fmt.Sprintf("10.8.1.%d", i)
		if !taken[ip] {
			return ip
		}
	}
}

// taken is the set of IPs on the server or reserved.
func (s *fakeVPN) taken(reserved []netip.Addr) map[string]bool {
	out := map[string]bool{}
	for _, p := range s.peers {
		out[p.IP] = true
	}
	for _, ip := range reserved {
		out[ip.String()] = true
	}
	return out
}

// hasPeer reports whether the server holds this public key.
func (s *fakeVPN) hasPeer(key string) bool {
	_, ok := s.peers[key]
	return ok
}

var errBoom = errors.New("boom")
