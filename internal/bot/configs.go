package bot

import (
	"context"
	"errors"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// adminConfigsAsk shows how many users get a fresh config and waits for
// "send" or "cancel".
func (r *Router) adminConfigsAsk(ctx context.Context, a adminAction) error {
	ids, err := r.svc.BroadcastRecipients(ctx)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	return r.send.Send(
		ctx,
		OutMessage{
			ChatID:   a.ChatID,
			Text:     configsAskText(len(ids)),
			Keyboard: configsKeyboard(),
		},
	)
}

// adminConfigs sends every user with an enabled key a notice and a fresh
// config for each such key, in the background like a broadcast. Only one
// run at a time, so a double press doesn't send everything twice.
func (r *Router) adminConfigs(ctx context.Context, a adminAction) error {
	r.mu.Lock()
	busy := r.configsRunning
	r.configsRunning = true
	r.mu.Unlock()
	if busy {
		return r.send.Send(
			ctx,
			OutMessage{
				ChatID: a.ChatID,
				Text:   configsBusyText,
			},
		)
	}
	ids, err := r.svc.BroadcastRecipients(ctx)
	if err != nil {
		r.endConfigs()
		return r.reportError(ctx, a.failed(err))
	}
	err = r.send.Send(
		ctx,
		OutMessage{
			ChatID: a.ChatID,
			Text:   configsStartedText,
		},
	)
	r.jobs.Go(func() {
		defer r.endConfigs()
		r.runBroadcast(
			r.life,
			broadcastJob{
				AdminChat:  a.ChatID,
				Recipients: ids,
				Deliver:    r.sendFreshConfigs,
				Report:     configsReportText,
			},
		)
	})
	return err
}

func (r *Router) endConfigs() {
	r.mu.Lock()
	r.configsRunning = false
	r.mu.Unlock()
}

// sendFreshConfigs sends one user the notice, then a config for each of
// their enabled keys. Keys the bot has no private key for (made on a
// device) are skipped: only that device can build their config.
func (r *Router) sendFreshConfigs(ctx context.Context, userID int64) error {
	keys, err := r.svc.Access(ctx, userID)
	if err != nil {
		return err
	}
	err = r.send.Send(
		ctx,
		OutMessage{
			ChatID: userID,
			Text:   configsNoticeText,
		},
	)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if !k.Peer.Enabled {
			continue
		}
		if err := r.sendFreshConfig(
			ctx,
			service.UserKey{
				UserID:    userID,
				PublicKey: k.Peer.PublicKey,
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *Router) sendFreshConfig(ctx context.Context, k service.UserKey) error {
	kc, err := r.svc.UserConfig(ctx, k)
	if errors.Is(err, service.ErrNoPrivateKey) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.sendConfig(
		ctx,
		configDelivery{
			ChatID: k.UserID,
			Key:    kc,
		},
	)
}
