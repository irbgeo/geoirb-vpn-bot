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
// (data/SplitTunnel.mp4, built into the binary) shows how. Without that
// video there is neither the step nor the button. iPhone and iPad get
// another guide (a Shortcuts automation that switches the tunnel off while
// the app is open), with its own video when there is one
// (data/IPhoneAutomation.mp4). Both guides are a videoGuide and share the
// code below.

// askDevice asks which device the user has: each guide fits some of them.
func (s *router) askDevice(ctx context.Context, chatID int64) error {
	outMessage := outMessage{
		ChatID:   chatID,
		Text:     splitAskText,
		Keyboard: splitAskKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}

// sendSplitVideo answers "Android" and "Windows". Without the video there
// is no such step, so nothing is sent.
func (s *router) sendSplitVideo(ctx context.Context, chatID int64) error {
	if s.splitVideo.file.empty() {
		return nil
	}
	videoRequest := videoRequest{
		guide:  s.splitVideo,
		chatID: chatID,
	}
	return s.sendGuide(ctx, videoRequest)
}

// sendIPhoneGuide answers "iPhone, iPad": the text, and the video when the
// bot has one.
func (s *router) sendIPhoneGuide(ctx context.Context, chatID int64) error {
	videoRequest := videoRequest{
		guide:  s.iphoneVideo,
		chatID: chatID,
	}
	return s.sendGuide(ctx, videoRequest)
}

// sendGuide sends a guide: the steps in words, then its video if it has
// one. Telegram has the file after the first upload and gives it an ID:
// with an ID the video is sent right here. Without one (the first request
// after a start, or Telegram no longer takes the ID) the work goes to the
// background, so a slow upload can't hold this request or be cut with it:
// one request uploads, the ones that come meanwhile wait for its ID, and
// after a failed upload everyone gets the "try later" text for
// videoCoolDown.
func (s *router) sendGuide(ctx context.Context, r videoRequest) error {
	outMessage := outMessage{
		ChatID:   r.chatID,
		Text:     r.guide.text,
		Keyboard: menuKeyboard(),
	}
	err := s.send.Send(ctx, outMessage)
	if err != nil || r.guide.file.empty() {
		return err
	}
	r.fileID = r.guide.file.fileID()
	if r.fileID != "" {
		refused, err := s.videoByID(ctx, r)
		if !refused {
			return err
		}
		r.fileID = ""
	}
	switch r.guide.file.start() {
	case videoLater:
		s.videoFailed(ctx, r.chatID)
	case videoUpload:
		s.jobs.spawn(func(life context.Context) {
			s.uploadVideo(life, r)
		})
	case videoWait:
		s.jobs.spawn(func(life context.Context) {
			s.awaitVideo(life, r)
		})
	}
	return nil
}

// videoByID sends the video by the file ID in r. refused: Telegram does not
// take this ID (the caller uploads the file again, or says "later"). Any
// other failure is returned, and told to the user unless they blocked the
// bot.
func (s *router) videoByID(ctx context.Context, r videoRequest) (refused bool, err error) {
	outVideo := outVideo{
		ChatID:  r.chatID,
		FileID:  r.fileID,
		Caption: r.guide.caption,
	}
	_, err = s.send.SendVideo(ctx, &outVideo)
	switch {
	case err == nil, tgbot.IsForbidden(err):
		return false, err
	case refusedFileID(err):
		log.Printf("bot: video %s by file ID: %v", r.guide.name, err)
		r.guide.file.refuse(r.fileID)
		return true, nil
	}
	s.videoFailed(ctx, r.chatID)
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

// uploadVideo uploads the video to the chat whose request started the
// upload (videoFile.start) and ends it: the ID is kept for everyone else. A
// failure is told to the user, who can press again in a minute.
func (s *router) uploadVideo(ctx context.Context, r videoRequest) {
	outVideo := outVideo{
		ChatID:  r.chatID,
		Name:    r.guide.name,
		Data:    r.guide.file.data,
		Caption: r.guide.caption,
	}
	id, err := s.send.SendVideo(ctx, &outVideo)
	r.guide.file.finish(id)
	if err == nil {
		return
	}
	log.Printf("bot: video %s to %d: %v", r.guide.name, r.chatID, err)
	s.videoFailed(ctx, r.chatID)
}

// awaitVideo waits for the upload another request runs and sends the video
// by the ID it brought; a failed upload gets the "try later" text.
func (s *router) awaitVideo(ctx context.Context, r videoRequest) {
	r.fileID = r.guide.file.wait(ctx)
	if r.fileID == "" {
		s.videoFailed(ctx, r.chatID)
		return
	}
	refused, err := s.videoByID(ctx, r)
	if err != nil {
		log.Printf("bot: video %s to %d: %v", r.guide.name, r.chatID, err)
	}
	if refused {
		s.videoFailed(ctx, r.chatID)
	}
}

// videoFailed tells the user the video did not come and to press again
// later. Not on shutdown (ctx ended): the background work must end at once,
// without one more Telegram call per waiting user.
func (s *router) videoFailed(ctx context.Context, chatID int64) {
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

// splitNone answers Mac and Linux: the app has no such setting there. Old
// messages carry this button for iPhone and iPad too, so the text points
// them to their guide.
func (s *router) splitNone(ctx context.Context, chatID int64) error {
	outMessage := outMessage{
		ChatID:   chatID,
		Text:     splitNoneText,
		Keyboard: menuKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}
