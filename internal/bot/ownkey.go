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
func (s *router) askOwnKeyAction(ctx context.Context, cq *tgbot.CallbackQuery) error {
	reissue := strings.HasPrefix(cq.Data, cbReissueAsk)
	pub := strings.TrimPrefix(strings.TrimPrefix(cq.Data, cbReissueAsk), cbDeleteAsk)
	keys, err := s.keys.Access(ctx, cq.SenderID())
	if err != nil {
		return err
	}
	msg := outMessage{
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
	return s.send.Send(ctx, msg)
}

// reissueKey gives the key new secrets and sends its new config.
func (s *router) reissueKey(ctx context.Context, cq *tgbot.CallbackQuery) error {
	userKey := service.UserKey{
		UserID:    cq.SenderID(),
		PublicKey: strings.TrimPrefix(cq.Data, cbReissue),
	}
	p, err := s.keys.ReissueKey(ctx, userKey)
	if err != nil {
		ownKeyFailedInput := ownKeyFailedInput{
			Query: cq,
			Err:   err,
		}
		return s.ownKeyFailed(ctx, ownKeyFailedInput)
	}
	conf, err := s.keys.ClientConfig(ctx, p.PublicKey)
	if err == nil {
		keyConfig := service.KeyConfig{
			Peer: p,
			Conf: conf,
		}
		configDelivery := configDelivery{
			ChatID: cq.ChatID(),
			Key:    &keyConfig,
		}
		err = s.sendConfig(ctx, configDelivery)
	}
	text := reissuedText
	if err != nil {
		text = keyDeliveryFailedText // the new key exists: "My access" has it
	}
	outMessage := outMessage{
		ChatID:   cq.ChatID(),
		Text:     text,
		Keyboard: myAccessKeyboard(),
	}
	sendErr := s.send.Send(ctx, outMessage)
	if sendErr != nil && err == nil {
		err = sendErr
	}
	return err
}

// deleteOwnKey deletes one of the user's keys for good.
func (s *router) deleteOwnKey(ctx context.Context, cq *tgbot.CallbackQuery) error {
	userKey := service.UserKey{
		UserID:    cq.SenderID(),
		PublicKey: strings.TrimPrefix(cq.Data, cbDelete),
	}
	err := s.keys.DeleteOwnKey(ctx, userKey)
	if err != nil {
		ownKeyFailedInput := ownKeyFailedInput{
			Query: cq,
			Err:   err,
		}
		return s.ownKeyFailed(ctx, ownKeyFailedInput)
	}
	outMessage := outMessage{
		ChatID:   cq.ChatID(),
		Text:     keyDeletedText,
		Keyboard: menuKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}

// ownKeyFailed explains a failed reissue or delete; a key that is gone (a
// second press, another user's key) is not an error.
func (s *router) ownKeyFailed(ctx context.Context, in ownKeyFailedInput) error {
	text, known := keyNotFoundText, errors.Is(in.Err, service.ErrNotFound)
	if !known {
		text = ownKeyFailedText
	}
	userError := userError{
		ChatID: in.Query.ChatID(),
		Err:    in.Err,
		Text:   text,
		Known:  known,
	}
	return s.replyError(ctx, userError)
}
