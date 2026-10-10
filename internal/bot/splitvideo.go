package bot

import (
	"context"

	"github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// The last step of getting a key, also a menu button: apps that refuse to
// work while the tunnel is on (banks, Gosuslugi). AmneziaVPN can route
// chosen apps around the tunnel on Android and Windows only, and one video
// shows how. Both the step and the button exist only while
// SPLIT_VIDEO_FILE_ID is set.

// askDevice asks which device the user has: the video fits two of them.
func (s *router) askDevice(ctx context.Context, chatID int64) error {
	outMessage := outMessage{
		ChatID:   chatID,
		Text:     splitAskText,
		Keyboard: splitAskKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}

// sendSplitVideo answers "Android" and "Windows": the steps in words, then
// the video. A button pressed after the video was switched off does nothing.
func (s *router) sendSplitVideo(ctx context.Context, chatID int64) error {
	if s.splitVideo == "" {
		return nil
	}
	outMessage := outMessage{
		ChatID:   chatID,
		Text:     splitHowToText,
		Keyboard: menuKeyboard(),
	}
	err := s.send.Send(ctx, outMessage)
	if err != nil {
		return err
	}
	outVideo := outVideo{
		ChatID:  chatID,
		FileID:  s.splitVideo,
		Caption: splitVideoCaption,
	}
	return s.send.SendVideo(ctx, outVideo)
}

// splitNone answers every other device: the app has no such setting there.
func (s *router) splitNone(ctx context.Context, chatID int64) error {
	outMessage := outMessage{
		ChatID:   chatID,
		Text:     splitNoneText,
		Keyboard: menuKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}

// adminVideo tells an admin the Telegram ID of a video they sent, to put
// into SPLIT_VIDEO_FILE_ID. It reports false for everyone else, so their
// message is handled as before.
func (s *router) adminVideo(ctx context.Context, m *tgbot.Message) (bool, error) {
	u, err := s.users.User(ctx, m.From.ID)
	if err != nil || u.Role != service.RoleAdmin {
		return false, nil //nolint:nilerr // not an admin: not a video for the bot
	}
	outMessage := outMessage{
		ChatID: m.Chat.ID,
		Text:   videoIDText(m.Video.FileID),
	}
	return true, s.send.Send(ctx, outMessage)
}
