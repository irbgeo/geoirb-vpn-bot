package bot

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// subnetAlertPercent: alert admins when more of the subnet is taken.
const subnetAlertPercent = 80

// backupMaxAge: the backup runs daily; older than this means it failed.
const backupMaxAge = 26 * time.Hour

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

// Notifier sends what the bot says unasked: key notices to owners and
// alerts to admins (payments, subnet, backups, server load).
type Notifier struct {
	users       Users
	send        Sender
	load        ServerLoad
	backupStamp string // touched by every good backup (see backupAlert)
	// subnetAlert / backupAlert: an alert went out and the condition still
	// holds; it alerts again only after it cleared and came back.
	subnetAlerted latch
	backupAlerted latch
	online        onlineWatch
}

// NewNotifier creates a Notifier.
func NewNotifier(
	d *NotifierDeps,
) *Notifier {
	return &Notifier{
		users:       d.Users,
		send:        d.Sender,
		load:        d.Load,
		backupStamp: d.BackupStamp,
	}
}

// DeliverMaintenance tells owners that their key ended or ends soon (with
// an "extend" button) and the admins about keys without Telegram, a nearly
// full subnet, an old backup and clients suddenly dropping off.
func (s *Notifier) DeliverMaintenance(ctx context.Context, m *service.Maintenance) {
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
			s.sendKeyNotice(
				ctx,
				keyNotice{
					Peer:    p,
					Text:    g.Text(p),
					NoOffer: g.NoOffer,
				},
			)
		}
	}
	s.subnetAlert(ctx, m)
	s.backupAlert(ctx)
	s.onlineDropAlert(ctx, m)
}

// CheckServerLoad tells admins when a server limit (connection table,
// memory, disk, CPU) is passed or back to normal. It runs on its own
// timer, not with maintenance: a server out of memory can fail that one.
func (s *Notifier) CheckServerLoad(ctx context.Context) {
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
// blocked the bot) is logged and the rest still get it.
func (s *Notifier) NotifyAdmins(ctx context.Context, text string) {
	admins, err := s.users.Admins(ctx)
	if err != nil {
		log.Printf("bot: list admins: %v", err)
		return
	}
	if len(admins) == 0 {
		log.Printf("bot: no admins in the DB, alert only logged: %s", text)
		return
	}
	for _, a := range admins {
		err := s.send.Send(
			ctx,
			OutMessage{
				ChatID: a.ID,
				Text:   text,
			},
		)
		if err != nil {
			log.Printf("bot: notify admin %d: %v", a.ID, err)
		}
	}
}

// sendKeyNotice sends kn to the key's owner. A failed send (e.g. the user
// blocked the bot) and a key without an owner are logged.
func (s *Notifier) sendKeyNotice(ctx context.Context, kn keyNotice) {
	if kn.Peer.UserID == 0 {
		log.Printf("bot: key %s has no owner, notice not sent: %s", kn.Peer.IP, kn.Text)
		return
	}
	msg := OutMessage{
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
// and again only after it has dropped below and risen once more.
func (s *Notifier) subnetAlert(ctx context.Context, m *service.Maintenance) {
	if m.SubnetTotal == 0 {
		return // unknown this run: keep the alert state as it is
	}
	full := m.SubnetUsed*100 > m.SubnetTotal*subnetAlertPercent
	if s.subnetAlerted.rise(full) {
		s.NotifyAdmins(ctx, subnetAlertText(m))
	}
}

// backupAlert warns admins once when the last good backup is older than
// backupMaxAge (or never happened), and again only after a fresh one.
func (s *Notifier) backupAlert(ctx context.Context) {
	if s.backupStamp == "" {
		return
	}
	var last time.Time
	if st, err := os.Stat(s.backupStamp); err == nil {
		last = st.ModTime()
	}
	old := time.Since(last) > backupMaxAge
	if s.backupAlerted.rise(old) {
		s.NotifyAdmins(ctx, backupAlertText(last))
	}
}

// onlineDropAlert warns admins once when the clients online fall far below
// the peak of the last hour, and again only after they came back.
func (s *Notifier) onlineDropAlert(ctx context.Context, m *service.Maintenance) {
	if m.Online < 0 {
		return // unknown this run: keep the state as it is
	}
	drop, alert := s.online.record(m.Online)
	if alert {
		s.NotifyAdmins(ctx, onlineDropText(drop))
	}
}
