package bot

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

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
// request after a start, or Telegram no longer takes the ID) the work goes
// to the background, so a slow upload can't hold this request or be cut
// with it: one request uploads, the ones that come meanwhile wait for its
// ID, and after a failed upload everyone gets the "try later" text for
// videoCoolDown.
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
		refused, err := s.videoByID(ctx, &outVideo)
		if !refused {
			return err
		}
	}
	switch s.splitVideo.start() {
	case videoLater:
		s.splitVideoFailed(ctx, chatID)
	case videoUpload:
		s.jobs.spawn(func(life context.Context) {
			s.uploadSplitVideo(life, chatID)
		})
	case videoWait:
		s.jobs.spawn(func(life context.Context) {
			s.awaitSplitVideo(life, chatID)
		})
	}
	return nil
}

// videoByID sends the video by a file ID. refused: Telegram does not take
// this ID (the caller uploads the file again, or says "later"). Any other
// failure is returned, and told to the user unless they blocked the bot.
func (s *router) videoByID(ctx context.Context, v *outVideo) (refused bool, err error) {
	_, err = s.send.SendVideo(ctx, v)
	switch {
	case err == nil, tgbot.IsForbidden(err):
		return false, err
	case refusedFileID(err):
		log.Printf("bot: split video by file ID: %v", err)
		s.splitVideo.refuse(v.FileID)
		return true, nil
	}
	s.splitVideoFailed(ctx, v.ChatID)
	return false, err
}

// refusedFileID: Telegram answered a send by file ID with a 400 about the
// file ("wrong file identifier", "type of file mismatch", …): the ID is
// stale, or names a file of another kind (Telegram files a soundless mp4
// as an animation, see telegramSender.SendVideo). Not "chat not found".
func refusedFileID(err error) bool {
	var apiErr *tgbot.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusBadRequest {
		return false
	}
	return strings.Contains(strings.ToLower(apiErr.Description), "file")
}

// uploadSplitVideo uploads the video to the chat whose request started the
// upload (videoFile.start) and ends it: the ID is kept for everyone else. A
// failure is told to the user, who can press again in a minute.
func (s *router) uploadSplitVideo(ctx context.Context, chatID int64) {
	outVideo := outVideo{
		ChatID:  chatID,
		Name:    splitVideoName,
		Data:    s.splitVideo.data,
		Caption: splitVideoCaption,
	}
	id, err := s.send.SendVideo(ctx, &outVideo)
	s.splitVideo.finish(id)
	if err == nil {
		return
	}
	log.Printf("bot: split video to %d: %v", chatID, err)
	s.splitVideoFailed(ctx, chatID)
}

// awaitSplitVideo waits for the upload another request runs and sends the
// video by the ID it brought; a failed upload gets the "try later" text.
func (s *router) awaitSplitVideo(ctx context.Context, chatID int64) {
	id := s.splitVideo.wait(ctx)
	if id == "" {
		s.splitVideoFailed(ctx, chatID)
		return
	}
	outVideo := outVideo{
		ChatID:  chatID,
		FileID:  id,
		Caption: splitVideoCaption,
	}
	refused, err := s.videoByID(ctx, &outVideo)
	if err != nil {
		log.Printf("bot: split video to %d: %v", chatID, err)
	}
	if refused {
		s.splitVideoFailed(ctx, chatID)
	}
}

// splitVideoFailed tells the user the video did not come and to press again
// later. Not on shutdown (ctx ended): the background work must end at once,
// without one more Telegram call per waiting user.
func (s *router) splitVideoFailed(ctx context.Context, chatID int64) {
	if ctx.Err() != nil {
		return
	}
	outMessage := outMessage{
		ChatID:   chatID,
		Text:     splitVideoFailedText,
		Keyboard: menuKeyboard(),
	}
	err := s.send.Send(ctx, outMessage)
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
