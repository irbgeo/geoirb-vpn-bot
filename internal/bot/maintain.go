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

// DeliverMaintenance tells owners that their key ended or ends soon (with
// an "extend" button) and the admins about keys without Telegram and a
// nearly full subnet.
func (r *Router) DeliverMaintenance(ctx context.Context, m *service.Maintenance) {
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
			r.sendKeyNotice(
				ctx,
				keyNotice{
					Peer:    p,
					Text:    g.Text(p),
					NoOffer: g.NoOffer,
				},
			)
		}
	}
	r.subnetAlert(ctx, m)
	r.backupAlert(ctx)
}

// sendKeyNotice sends n to the key's owner. A failed send (e.g. the user
// blocked the bot) and a key without an owner are logged.
func (r *Router) sendKeyNotice(ctx context.Context, n keyNotice) {
	if n.Peer.UserID == 0 {
		log.Printf("bot: key %s has no owner, notice not sent: %s", n.Peer.IP, n.Text)
		return
	}
	msg := OutMessage{
		ChatID: n.Peer.UserID,
		Text:   n.Text,
	}
	if !n.NoOffer {
		msg.Keyboard = extendKeyboard(n.Peer)
	}
	err := r.send.Send(ctx, msg)
	if err != nil {
		log.Printf("bot: key notice to %d: %v", n.Peer.UserID, err)
	}
}

// subnetAlert warns admins once when the subnet passes subnetAlertPercent,
// and again only after it has dropped below and risen once more.
func (r *Router) subnetAlert(ctx context.Context, m *service.Maintenance) {
	if m.SubnetTotal == 0 {
		return // unknown this run: keep the alert state as it is
	}
	full := m.SubnetUsed*100 > m.SubnetTotal*subnetAlertPercent
	r.mu.Lock()
	send := full && !r.subnetAlerted
	r.subnetAlerted = full
	r.mu.Unlock()
	if send {
		r.NotifyAdmins(ctx, subnetAlertText(m))
	}
}

// backupAlert warns admins once when the last good backup is older than
// backupMaxAge (or never happened), and again only after a fresh one.
func (r *Router) backupAlert(ctx context.Context) {
	if r.backupStamp == "" {
		return
	}
	var last time.Time
	if st, err := os.Stat(r.backupStamp); err == nil {
		last = st.ModTime()
	}
	old := time.Since(last) > backupMaxAge
	r.mu.Lock()
	send := old && !r.backupAlerted
	r.backupAlerted = old
	r.mu.Unlock()
	if send {
		r.NotifyAdmins(ctx, backupAlertText(last))
	}
}
