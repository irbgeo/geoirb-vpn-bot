package bot

import (
	"context"
	"errors"
	"strings"

	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// askOwnKeyAction confirms "reissue" or "delete" for one of the user's
// keys, saying what it means for that key.
func (r *Router) askOwnKeyAction(ctx context.Context, cq *tgbot.CallbackQuery) error {
	reissue := strings.HasPrefix(cq.Data, cbReissueAsk)
	pub := strings.TrimPrefix(strings.TrimPrefix(cq.Data, cbReissueAsk), cbDeleteAsk)
	keys, err := r.keys.Access(ctx, cq.SenderID())
	if err != nil {
		return err
	}
	msg := OutMessage{
		ChatID: cq.ChatID(),
		Text:   keyNotFoundText,
	}
	for _, k := range keys {
		if k.Peer.PublicKey != pub {
			continue
		}
		if reissue {
			msg.Text, msg.Keyboard = reissueAskText(k.Peer), confirmKeyboard(cbReissue+pub)
		} else {
			msg.Text, msg.Keyboard = deleteAskText(k.Peer), confirmKeyboard(cbDelete+pub)
		}
	}
	return r.send.Send(ctx, msg)
}

// reissueKey gives the key new secrets and sends its new config.
func (r *Router) reissueKey(ctx context.Context, cq *tgbot.CallbackQuery) error {
	p, err := r.keys.ReissueKey(
		ctx,
		service.UserKey{
			UserID:    cq.SenderID(),
			PublicKey: strings.TrimPrefix(cq.Data, cbReissue),
		},
	)
	if err != nil {
		return r.ownKeyFailed(ctx, cq, err)
	}
	conf, err := r.keys.ClientConfig(ctx, p.PublicKey)
	if err == nil {
		err = r.sendConfig(
			ctx,
			configDelivery{
				ChatID: cq.ChatID(),
				Key: &service.KeyConfig{
					Peer: p,
					Conf: conf,
				},
			},
		)
	}
	text := reissuedText
	if err != nil {
		text = keyDeliveryFailedText // the new key exists: "My access" has it
	}
	if sendErr := r.send.Send(
		ctx,
		OutMessage{
			ChatID:   cq.ChatID(),
			Text:     text,
			Keyboard: myAccessKeyboard(),
		},
	); sendErr != nil && err == nil {
		err = sendErr
	}
	return err
}

// deleteOwnKey deletes one of the user's keys for good.
func (r *Router) deleteOwnKey(ctx context.Context, cq *tgbot.CallbackQuery) error {
	err := r.keys.DeleteOwnKey(
		ctx,
		service.UserKey{
			UserID:    cq.SenderID(),
			PublicKey: strings.TrimPrefix(cq.Data, cbDelete),
		},
	)
	if err != nil {
		return r.ownKeyFailed(ctx, cq, err)
	}
	return r.send.Send(
		ctx,
		OutMessage{
			ChatID:   cq.ChatID(),
			Text:     keyDeletedText,
			Keyboard: menuKeyboard(),
		},
	)
}

// ownKeyFailed explains a failed reissue or delete; a key that is gone (a
// second press, another user's key) is not an error.
func (r *Router) ownKeyFailed(ctx context.Context, cq *tgbot.CallbackQuery, err error) error {
	text, known := keyNotFoundText, errors.Is(err, service.ErrNotFound)
	if !known {
		text = ownKeyFailedText
	}
	return r.replyError(
		ctx,
		userError{
			ChatID: cq.ChatID(),
			Err:    err,
			Text:   text,
			Known:  known,
		},
	)
}
