package bot

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"time"
)

// The router's own state lives in these small types, each with its own
// lock, so one feature can't hold up another and each is easy to read.

// dialogs remembers, per chat, what the bot waits for: a broadcast text, a
// key name, a "send" press. An entry older than pendingTTL counts as gone.
type dialogs struct {
	mu sync.Mutex
	m  map[int64]pendingInput
}

// takeResult says what dialogs.take found.
type takeResult int

const (
	takeNone    takeResult = iota // nothing of that kind waits
	takeExpired                   // it waited too long and is dropped
	takeOK
)

func newDialogs() *dialogs {
	return &dialogs{
		m: map[int64]pendingInput{},
	}
}

// set records what the chat's next input is; At is stamped when zero.
func (s *dialogs) set(p pendingInput) {
	if p.At.IsZero() {
		p.At = time.Now()
	}
	s.mu.Lock()
	s.m[p.ChatID] = p
	s.mu.Unlock()
}

// drop forgets what the bot waited for from this chat.
func (s *dialogs) drop(chatID int64) {
	s.mu.Lock()
	delete(s.m, chatID)
	s.mu.Unlock()
}

// peek returns the chat's live entry without removing it.
func (s *dialogs) peek(chatID int64) (pendingInput, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[chatID]
	if !ok || time.Since(p.At) > pendingTTL {
		return pendingInput{}, false
	}
	return p, true
}

// take removes and returns the chat's entry if it is of kind k. An
// expired one is removed too, and reported as takeExpired.
func (s *dialogs) take(in dialogTake) (pendingInput, takeResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[in.ChatID]
	if !ok || p.Kind != in.Kind {
		return pendingInput{}, takeNone
	}
	delete(s.m, in.ChatID)
	if time.Since(p.At) > pendingTTL {
		return pendingInput{}, takeExpired
	}
	return p, takeOK
}

// jobs runs the background mass sends (broadcasts, config notices), one at
// a time: two together would go over Telegram's ~30 messages a second.
// They run on life, not on an update's ctx: go-tgbot's Dispatcher cancels
// that one as soon as the handler returns. spawn runs other background
// work (the video upload) on life too, outside that one slot.
type jobs struct {
	mu   sync.Mutex
	busy bool
	wg   sync.WaitGroup
	life context.Context
	stop context.CancelFunc
}

func newJobs() *jobs {
	life, stop := context.WithCancel(context.Background())
	return &jobs{
		life: life,
		stop: stop,
	}
}

// reserve takes the one slot; false when a mass send is running.
func (s *jobs) reserve() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return false
	}
	s.busy = true
	return true
}

// run starts fn in the reserved slot and frees it when fn returns.
func (s *jobs) run(fn func(ctx context.Context)) {
	s.wg.Go(func() {
		defer s.release()
		fn(s.life)
	})
}

// spawn runs fn in the background without taking the slot; close and wait
// cover it like a mass send.
func (s *jobs) spawn(fn func(ctx context.Context)) {
	s.wg.Go(func() {
		fn(s.life)
	})
}

// release gives the slot back without running anything.
func (s *jobs) release() {
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

// close stops running jobs (they report what went out) and waits for them.
func (s *jobs) close() {
	s.stop()
	s.wg.Wait()
}

// wait waits for running jobs without stopping them.
func (s *jobs) wait() {
	s.wg.Wait()
}

// maintFlag is the maintenance state: a file that exists while it is on,
// so it survives a restart; with no path, memory only.
type maintFlag struct {
	path string
	mu   sync.Mutex
	mem  bool
}

func newMaintFlag(path string) *maintFlag {
	return &maintFlag{
		path: path,
	}
}

func (s *maintFlag) on() bool {
	if s.path == "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.mem
	}
	_, err := os.Stat(s.path)
	return err == nil
}

func (s *maintFlag) set(on bool) error {
	if s.path == "" {
		s.mu.Lock()
		s.mem = on
		s.mu.Unlock()
		return nil
	}
	if on {
		return os.WriteFile(s.path, nil, 0o644)
	}
	err := os.Remove(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// latch turns a condition that holds for a while into one alert: rise is
// true only when the condition was false last time and is true now.
type latch struct {
	mu sync.Mutex
	up bool
}

func (s *latch) rise(now bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	was := s.up
	s.up = now
	return now && !was
}

// drop forgets a rise whose alert reached no one, so the next check that
// finds the condition still true alerts again.
func (s *latch) drop() {
	s.mu.Lock()
	s.up = false
	s.mu.Unlock()
}

// stampWatch watches a file touched by a job after every good run: it says
// once when the file gets older than maxAge (or is missing), and again
// only after a fresh touch. An empty path = no check.
type stampWatch struct {
	path    string
	maxAge  time.Duration
	alerted latch
}

// check returns the last good run (zero = none) and whether to alert now.
func (s *stampWatch) check() (last time.Time, alert bool) {
	if s.path == "" {
		return time.Time{}, false
	}
	st, err := os.Stat(s.path)
	if err == nil {
		last = st.ModTime()
	}
	return last, s.alerted.rise(time.Since(last) > s.maxAge)
}

// onlineWatch remembers the clients online over the last onlineDropWindow
// and says once when the count falls far below the peak.
type onlineWatch struct {
	mu      sync.Mutex
	samples []onlineSample
	low     bool
}

// record adds the count of now and returns the drop it makes against the
// peak before it; alert is true only when the drop just began.
func (s *onlineWatch) record(online int) (drop onlineDrop, alert bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.samples = slices.DeleteFunc(s.samples, func(x onlineSample) bool {
		return now.Sub(x.At) >= onlineDropWindow
	})
	drop = onlineDrop{
		Online: online,
	}
	for _, x := range s.samples {
		drop.Peak = max(drop.Peak, x.Online)
	}
	sample := onlineSample{
		At:     now,
		Online: online,
	}
	s.samples = append(s.samples, sample)
	low := drop.Peak >= onlineDropMinPeak && online*onlineDropRatio <= drop.Peak
	alert = low && !s.low
	s.low = low
	return drop, alert
}

// unsent forgets a drop whose alert reached no one: the next record that
// still sees it alerts again.
func (s *onlineWatch) unsent() {
	s.mu.Lock()
	s.low = false
	s.mu.Unlock()
}

// inFlight is a set of running operations by ID (refunds by charge ID),
// so a double press doesn't start the same one twice.
type inFlight struct {
	mu sync.Mutex
	m  map[string]bool
}

func newInFlight() *inFlight {
	return &inFlight{
		m: map[string]bool{},
	}
}

// start marks id running; false if it already is.
func (s *inFlight) start(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m[id] {
		return false
	}
	s.m[id] = true
	return true
}

func (s *inFlight) end(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
}

// rateLimitKeys: with this many keys remembered, allow first forgets the
// ones idle for a whole window, so a flood of accounts can't grow the map
// without end.
const rateLimitKeys = 1000

// rateLimit lets one key (a user, a pressed button) act at most max times
// per window; allow counts the try when it says yes.
// shortcut: in memory only, a restart forgets the counts; count in the DB
// if someone floods across restarts.
type rateLimit[K comparable] struct {
	max    int
	window time.Duration
	mu     sync.Mutex
	seen   map[K][]time.Time
}

func newRateLimit[K comparable](
	limit int,
	window time.Duration,
) *rateLimit[K] {
	return &rateLimit[K]{
		max:    limit,
		window: window,
		seen:   map[K][]time.Time{},
	}
}

func (s *rateLimit[K]) allow(key K) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if len(s.seen) >= rateLimitKeys {
		for k, at := range s.seen {
			if now.Sub(at[len(at)-1]) >= s.window {
				delete(s.seen, k)
			}
		}
	}
	live := slices.DeleteFunc(s.seen[key], func(t time.Time) bool {
		return now.Sub(t) >= s.window
	})
	if len(live) >= s.max {
		s.seen[key] = live
		return false
	}
	s.seen[key] = append(live, now)
	return true
}

// videoFile is a video shipped in the binary. The first send uploads it;
// the file ID Telegram returns is reused afterwards. In memory only: one
// upload after every restart. uploading is held for a whole upload, so
// users who ask meanwhile wait for its ID instead of uploading too.
type videoFile struct {
	data      []byte // never changed after newVideoFile
	uploading sync.Mutex
	mu        sync.Mutex
	id        string
}

func newVideoFile(
	data []byte,
) *videoFile {
	return &videoFile{
		data: data,
	}
}

func (s *videoFile) empty() bool {
	return len(s.data) == 0
}

// fileID is Telegram's ID of the uploaded video; "" before the first upload.
func (s *videoFile) fileID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// forget drops id when it is still the remembered one: Telegram refused it.
func (s *videoFile) forget(id string) {
	s.mu.Lock()
	if s.id == id {
		s.id = ""
	}
	s.mu.Unlock()
}

// remember keeps the ID of a sent video; an empty one changes nothing.
func (s *videoFile) remember(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	s.id = id
	s.mu.Unlock()
}
