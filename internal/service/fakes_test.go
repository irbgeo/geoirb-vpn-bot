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
	m map[int64]User
}

func (f *fakeUsers) Get(_ context.Context, id int64) (*User, error) {
	u, ok := f.m[id]
	if !ok {
		return nil, nil
	}
	return &u, nil
}

// Register mirrors the store's upsert: a new user gets u as given; an
// existing one only gets the new username (role and the rest are kept).
func (f *fakeUsers) Register(_ context.Context, u *User) (*User, error) {
	got, ok := f.m[u.ID]
	if !ok {
		got = *u
	}
	got.Username = u.Username
	f.m[u.ID] = got
	return &got, nil
}

func (f *fakeUsers) AddKeys(_ context.Context, d KeysDelta) error {
	u, ok := f.m[d.UserID]
	if !ok {
		return nil // like the store: no row, nothing to count
	}
	u.KeysCount += d.Delta
	f.m[d.UserID] = u
	return nil
}

func (f *fakeUsers) SetKeyCounts(_ context.Context, counts map[int64]int) error {
	for id, u := range f.m {
		u.KeysCount = counts[id]
		f.m[id] = u
	}
	return nil
}

func (f *fakeUsers) SetTrialUsed(_ context.Context, id int64) error {
	u := f.m[id]
	u.TrialUsed = true
	f.m[id] = u
	return nil
}

func (f *fakeUsers) ByRole(_ context.Context, r Role) ([]*User, error) {
	var out []*User
	for _, u := range f.m {
		if u.Role == r {
			out = append(out, &u)
		}
	}
	return out, nil
}

func (f *fakeUsers) List(_ context.Context, p Page) ([]*User, int64, error) {
	all := make([]*User, 0, len(f.m))
	for _, u := range f.m {
		all = append(all, &u)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	end := min(p.Skip+p.Limit, int64(len(all)))
	return all[min(p.Skip, end):end], int64(len(all)), nil
}

type fakeFeedback struct {
	saved []Feedback
}

func (f *fakeFeedback) Add(_ context.Context, fb *Feedback) error {
	f.saved = append(f.saved, *fb)
	return nil
}

// List returns the saved feedback newest first (the last added first).
func (f *fakeFeedback) List(_ context.Context, p Page) ([]*Feedback, int64, error) {
	all := make([]*Feedback, 0, len(f.saved))
	for i := len(f.saved) - 1; i >= 0; i-- {
		all = append(all, &f.saved[i])
	}
	end := min(p.Skip+p.Limit, int64(len(all)))
	return all[min(p.Skip, end):end], int64(len(all)), nil
}

type fakePeers struct {
	m       map[string]Peer
	saveErr error
	getErr  error
}

func (f *fakePeers) Get(_ context.Context, key string) (*Peer, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	p, ok := f.m[key]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func (f *fakePeers) Save(_ context.Context, p *Peer) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.m[p.PublicKey] = *p
	return nil
}

func (f *fakePeers) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err // like a real DB call on a cancelled context
	}
	delete(f.m, key)
	return nil
}

func (f *fakePeers) ByUser(_ context.Context, userID int64) ([]*Peer, error) {
	var out []*Peer
	for _, p := range f.m {
		if p.UserID == userID {
			out = append(out, &p)
		}
	}
	return out, nil
}

func (f *fakePeers) ServerIPs(ctx context.Context, serverID string) ([]string, error) {
	ps, _ := f.ByServer(ctx, serverID)
	ips := make([]string, 0, len(ps))
	for _, p := range ps {
		ips = append(ips, p.IP)
	}
	return ips, nil
}

func (f *fakePeers) ByServer(_ context.Context, serverID string) ([]*Peer, error) {
	var out []*Peer
	for _, p := range f.m {
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
}

func (f *fakePayments) Add(_ context.Context, p *Payment) (bool, error) {
	if _, ok := f.m[p.ChargeID]; ok {
		return false, nil
	}
	f.m[p.ChargeID] = *p
	return true, nil
}

func (f *fakePayments) Get(_ context.Context, id string) (*Payment, error) {
	p, ok := f.m[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

// MarkApplied / MarkRefunded change only their own fields, like the store.
func (f *fakePayments) MarkApplied(_ context.Context, m PaymentMark) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	if f.saveFails > 0 {
		f.saveFails--
		return errBoom
	}
	p, ok := f.m[m.ChargeID]
	if !ok {
		return errors.New("no payment")
	}
	p.Applied = true
	p.PeerKey = m.PeerKey
	f.m[m.ChargeID] = p
	return nil
}

func (f *fakePayments) MarkRefunded(_ context.Context, m PaymentMark) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	p, ok := f.m[m.ChargeID]
	if !ok {
		return nil
	}
	p.RefundedAt = m.At
	f.m[m.ChargeID] = p
	return nil
}

func (f *fakePayments) ByUser(_ context.Context, userID int64) ([]*Payment, error) {
	var out []*Payment
	for _, p := range f.m {
		if p.UserID == userID {
			out = append(out, &p)
		}
	}
	return out, nil
}

func (f *fakePayments) Since(_ context.Context, t time.Time) ([]*Payment, error) {
	var out []*Payment
	for _, p := range f.m {
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
	table    map[string]string  // public key -> name in the Amnezia app
	stats    []PeerStat
	keys     int
	changes  int    // calls that change the server
	err      error  // every change fails
	onChange func() // runs inside a change, before err
	readErr  error  // PeerKeys and SubnetUsage fail
	tableErr error  // the app list can't be written (never an error)
}

func newFakeVPN() *fakeVPN {
	return &fakeVPN{
		peers: map[string]VPNPeer{
			"MANUAL1=": {
				PublicKey: "MANUAL1=",
				IP:        "10.8.1.1",
			},
		},
		table: map[string]string{},
	}
}

func (f *fakeVPN) GenKeys(context.Context) (VPNKeys, error) {
	f.keys++
	return VPNKeys{
		Private: fmt.Sprintf("PRIV%d=", f.keys),
		Public:  fmt.Sprintf("PUB%d=", f.keys),
		PSK:     fmt.Sprintf("PSK%d=", f.keys),
	}, nil
}

func (f *fakeVPN) AddPeer(_ context.Context, in *AddPeerInput) error {
	f.changes++
	p := *in.Peer
	p.IP = f.freeIP(in.Reserved)
	if err := in.Save(p.IP); err != nil {
		return err
	}
	if err := f.fail(); err != nil {
		return err
	}
	f.put(&p)
	return nil
}

func (f *fakeVPN) PutPeer(_ context.Context, p *VPNPeer) error {
	f.changes++
	if _, ok := f.peers[p.PublicKey]; ok {
		return nil
	}
	for _, other := range f.peers {
		if other.IP == p.IP {
			return fmt.Errorf("%w: %s", ErrIPTaken, p.IP)
		}
	}
	if err := f.fail(); err != nil {
		return err
	}
	f.put(p)
	return nil
}

func (f *fakeVPN) RemovePeer(_ context.Context, p *VPNPeer) error {
	f.changes++
	if err := f.fail(); err != nil {
		return err
	}
	delete(f.peers, p.PublicKey)
	delete(f.table, p.PublicKey)
	return nil
}

func (f *fakeVPN) ReplacePeer(_ context.Context, in *ReplacePeerInput) error {
	f.changes++
	if err := f.fail(); err != nil {
		return err
	}
	delete(f.peers, in.Old.PublicKey)
	delete(f.table, in.Old.PublicKey)
	f.put(in.New)
	return nil
}

func (f *fakeVPN) PeerKeys(context.Context) ([]string, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	out := make([]string, 0, len(f.peers))
	for k := range f.peers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (f *fakeVPN) SubnetUsage(_ context.Context, reserved []netip.Addr) (used, total int, err error) {
	if f.readErr != nil {
		return 0, 0, f.readErr
	}
	return len(f.taken(reserved)), 254, nil
}

func (f *fakeVPN) Stats(context.Context) ([]PeerStat, error) {
	return f.stats, nil
}

// ClientConfig renders a stand-in config from the spec.
func (f *fakeVPN) ClientConfig(_ context.Context, c *ClientSpec) (string, error) {
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
func (f *fakeVPN) fail() error {
	if f.onChange != nil {
		f.onChange()
	}
	return f.err
}

func (f *fakeVPN) put(p *VPNPeer) {
	f.peers[p.PublicKey] = *p
	if f.tableErr == nil {
		f.table[p.PublicKey] = p.Name
	}
}

// freeIP is the lowest 10.8.1.x not on the server and not reserved.
func (f *fakeVPN) freeIP(reserved []netip.Addr) string {
	taken := f.taken(reserved)
	for i := 1; ; i++ {
		if ip := fmt.Sprintf("10.8.1.%d", i); !taken[ip] {
			return ip
		}
	}
}

// taken is the set of IPs on the server or reserved.
func (f *fakeVPN) taken(reserved []netip.Addr) map[string]bool {
	out := map[string]bool{}
	for _, p := range f.peers {
		out[p.IP] = true
	}
	for _, ip := range reserved {
		out[ip.String()] = true
	}
	return out
}

// hasPeer reports whether the server holds this public key.
func (f *fakeVPN) hasPeer(key string) bool {
	_, ok := f.peers[key]
	return ok
}

var errBoom = errors.New("boom")
