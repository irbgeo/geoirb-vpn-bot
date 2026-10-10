package bot

import (
	"context"
	"log"
	"time"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
	"github.com/irbgeo/geoirb-vpn-bot/internal/tunnel"
)

// subnetAlertPercent: alert admins when more of the subnet is taken.
const subnetAlertPercent = 80

// backupMaxAge: the backup runs daily; older than this means it failed.
const backupMaxAge = 26 * time.Hour

// ruNetsMaxAge: the RU networks list updates weekly; older means it failed.
const ruNetsMaxAge = 8 * 24 * time.Hour

// Online drop: alert admins when the clients online fall to a quarter of
// the most seen in the last hour (if that was 5 or more). A server IP
// blocked in Russia looks like this: new connections fail, open ones live
// on, so clients drop off one by one over about an hour.
// ponytail: a quiet night can look the same and alert; add hours or a
// probe from Russia if it gets noisy.
const (
	onlineDropWindow  = time.Hour
	onlineDropMinPeak = 5
	onlineDropRatio   = 4
)

// notifier sends what the bot says unasked: key notices to owners and
// alerts to admins (payments, subnet, backups, server load, tunnel).
type notifier struct {
	users Users
	send  Sender
	load  ServerLoad
	// subnetAlerted: an alert went out and the subnet is still full; it
	// alerts again only after it cleared and came back.
	subnetAlerted latch
	backup        *stampWatch
	ruNets        *stampWatch
	online        onlineWatch
}

// NewNotifier creates a notifier. From cfg it takes BackupStamp and
// RUNetsStamp: the files touched by each good backup / RU networks update
// ("" = no check). load nil = no server load alerts.
func NewNotifier(
	users Users,
	sender Sender,
	cfg *config.Config,
	load ServerLoad,
) *notifier {
	backup := newStampWatch(
		cfg.BackupStamp,
		backupMaxAge,
	)
	ruNets := newStampWatch(
		cfg.RUNetsStamp,
		ruNetsMaxAge,
	)
	return &notifier{
		users:  users,
		send:   sender,
		load:   load,
		backup: backup,
		ruNets: ruNets,
	}
}

// DeliverMaintenance tells owners that their key ended or ends soon (with
// an "extend" button) and the admins about keys without Telegram, a nearly
// full subnet, an old backup or RU networks list and clients suddenly
// dropping off.
func (s *notifier) DeliverMaintenance(ctx context.Context, m *service.Maintenance) {
	for _, g := range []noticeGroup{
		{
			Peers:   m.MadeForever,
			Text:    madeForeverText,
			NoOffer: true,
		},
		{
			Peers: m.Expired,
			Text:  expiredText,
		},
		{
			Peers: m.Remind3d,
			Text:  remind3dText,
		},
		{
			Peers: m.Remind1d,
			Text:  remind1dText,
		},
	} {
		for _, p := range g.Peers {
			keyNotice := keyNotice{
				Peer:    p,
				Text:    g.Text(p),
				NoOffer: g.NoOffer,
			}
			s.sendKeyNotice(ctx, keyNotice)
		}
	}
	s.subnetAlert(ctx, m)
	s.stampAlerts(ctx)
	s.onlineDropAlert(ctx, m)
}

// WatchServerLoad runs CheckServerLoad every minute until ctx is done.
func (s *notifier) WatchServerLoad(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.CheckServerLoad(ctx)
		}
	}
}

// WatchTunnel checks the exit tunnel now, before it returns (so the bot
// route is set for the state found before the bot's first calls; while the
// state is still Unknown, the watcher's start window, a route left by a
// killed bot is not touched — a clean stop removes it), then every minute in the background until ctx
// is done, and tells admins when it goes down or comes back. Up at the
// first known state is quiet (only the bot route is set); down is told
// once.
func (s *notifier) WatchTunnel(ctx context.Context, w tunnelChecker) {
	first := true
	unsent := "" // the newest alert that reached no admin yet: tried again every check
	check := func() {
		st, changed, err := w.Check(ctx)
		if err != nil {
			log.Printf("bot: tunnel check: %v", err)
		}
		if changed && (st == tunnel.Down || !first) {
			unsent = tunnelText(st)
		}
		if unsent != "" && s.NotifyAdmins(ctx, unsent) {
			unsent = ""
		}
		if err == nil && st != tunnel.Unknown {
			first = false
		}
	}
	check()
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				check()
			}
		}
	}()
}

// CheckServerLoad tells admins when a server limit (connection table,
// memory, disk, CPU) is passed or back to normal. It runs on its own
// timer, not with maintenance: a server out of memory can fail that one.
func (s *notifier) CheckServerLoad(ctx context.Context) {
	if s.load == nil {
		return
	}
	alerts, err := s.load.Check()
	if err != nil {
		log.Printf("bot: server load: %v", err)
	}
	for _, a := range alerts {
		s.NotifyAdmins(ctx, loadAlertText(a))
	}
}

// NotifyAdmins sends text to every admin. A failed send (e.g. an admin who
// blocked the bot) is logged and the rest still get it. It reports whether
// the text got out: at least one admin has it, or there are no admins and
// the log line is all there can be. False means "try again later".
func (s *notifier) NotifyAdmins(ctx context.Context, text string) bool {
	admins, err := s.users.Admins(ctx)
	if err != nil {
		log.Printf("bot: list admins: %v", err)
		return false
	}
	if len(admins) == 0 {
		log.Printf("bot: no admins in the DB, alert only logged: %q", text) // %q: user text stays on one line
		return true
	}
	delivered := false
	for _, a := range admins {
		outMessage := outMessage{
			ChatID: a.ID,
			Text:   text,
		}
		err := s.send.Send(ctx, outMessage)
		if err != nil {
			log.Printf("bot: notify admin %d: %v", a.ID, err)
			continue
		}
		delivered = true
	}
	return delivered
}

// sendKeyNotice sends kn to the key's owner. A failed send (e.g. the user
// blocked the bot) and a key without an owner are logged.
func (s *notifier) sendKeyNotice(ctx context.Context, kn keyNotice) {
	if kn.Peer.UserID == 0 {
		log.Printf("bot: key %s has no owner, notice not sent: %s", kn.Peer.IP, kn.Text)
		return
	}
	msg := outMessage{
		ChatID: kn.Peer.UserID,
		Text:   kn.Text,
	}
	if !kn.NoOffer {
		msg.Keyboard = extendKeyboard(kn.Peer)
	}
	err := s.send.Send(ctx, msg)
	if err != nil {
		log.Printf("bot: key notice to %d: %v", kn.Peer.UserID, err)
	}
}

// subnetAlert warns admins once when the subnet passes subnetAlertPercent,
// and again only after it has dropped below and risen once more. An alert
// that reached no admin does not count as sent: the next run tries again
// (the same in stampAlerts and onlineDropAlert).
func (s *notifier) subnetAlert(ctx context.Context, m *service.Maintenance) {
	if m.SubnetTotal == 0 {
		return // unknown this run: keep the alert state as it is
	}
	full := m.SubnetUsed*100 > m.SubnetTotal*subnetAlertPercent
	if s.subnetAlerted.rise(full) && !s.NotifyAdmins(ctx, subnetAlertText(m)) {
		s.subnetAlerted.drop()
	}
}

// stampAlerts warns admins once when the last good backup or RU networks
// update is too old (or never happened), and again only after a fresh one.
func (s *notifier) stampAlerts(ctx context.Context) {
	last, alert := s.backup.check()
	if alert && !s.NotifyAdmins(ctx, backupAlertText(last)) {
		s.backup.alerted.drop()
	}
	last, alert = s.ruNets.check()
	if alert && !s.NotifyAdmins(ctx, ruNetsAlertText(last)) {
		s.ruNets.alerted.drop()
	}
}

// onlineDropAlert warns admins once when the clients online fall far below
// the peak of the last hour, and again only after they came back.
func (s *notifier) onlineDropAlert(ctx context.Context, m *service.Maintenance) {
	if m.Online < 0 {
		return // unknown this run: keep the state as it is
	}
	drop, alert := s.online.record(m.Online)
	if alert && !s.NotifyAdmins(ctx, onlineDropText(drop)) {
		s.online.unsent()
	}
}
