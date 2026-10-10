package bot

import (
	"context"
	"log"

	tgbot "github.com/irbgeo/go-tgbot"
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
// the video. Telegram has the file after the first upload and gives it an
// ID: with an ID the video is sent right here. Without one (the first
// request after a start, or Telegram no longer takes the ID) the upload
// goes to the background, so a slow one can't hold this request or be cut
// with it.
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
	id := s.splitVideo.fileID()
	if id != "" {
		outVideo := outVideo{
			ChatID:  chatID,
			FileID:  id,
			Caption: splitVideoCaption,
		}
		_, err = s.send.SendVideo(ctx, &outVideo)
		if err == nil || tgbot.IsForbidden(err) {
			return err // sent, or the user blocked the bot: the ID is fine
		}
		log.Printf("bot: split video by file ID: %v", err)
		s.splitVideo.forget(id)
	}
	s.jobs.spawn(func(life context.Context) {
		s.uploadSplitVideo(life, chatID)
	})
	return nil
}

// uploadSplitVideo sends the video to a chat when there was no file ID:
// one upload at a time. Whoever waited for a running upload sends by the
// ID it brought. A failure is told to the user, who can press again.
func (s *router) uploadSplitVideo(ctx context.Context, chatID int64) {
	s.splitVideo.uploading.Lock()
	defer s.splitVideo.uploading.Unlock()
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
	if err == nil {
		s.splitVideo.remember(id)
		return
	}
	log.Printf("bot: split video to %d: %v", chatID, err)
	outMessage := outMessage{
		ChatID:   chatID,
		Text:     splitVideoFailedText,
		Keyboard: menuKeyboard(),
	}
	err = s.send.Send(context.WithoutCancel(ctx), outMessage)
	if err != nil {
		log.Printf("bot: %v", err)
	}
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
