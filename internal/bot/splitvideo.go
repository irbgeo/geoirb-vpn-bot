package bot

import (
	"context"
)

// The last step of getting a key, also a menu button: apps that refuse to
// work while the tunnel is on (banks, Gosuslugi). AmneziaVPN can route
// chosen apps around the tunnel on Android and Windows only, and one video
// (data/SplitTunnel.mp4, built into the binary) shows how. Without a video
// there is neither the step nor the button.

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
// the video. The first send uploads the file; later ones reuse the ID
// Telegram gave it.
func (s *router) sendSplitVideo(ctx context.Context, chatID int64) error {
	if s.splitVideo.empty() {
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
		FileID:  s.splitVideo.fileID(),
		Caption: splitVideoCaption,
	}
	if outVideo.FileID == "" {
		outVideo.Name = splitVideoName
		outVideo.Data = s.splitVideo.data
	}
	id, err := s.send.SendVideo(ctx, &outVideo)
	if err != nil {
		return err
	}
	s.splitVideo.remember(id)
	return nil
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
