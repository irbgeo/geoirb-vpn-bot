package bot

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"
	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

func keyService() *fakeService {
	return &fakeService{
		created: &service.Peer{
			PublicKey: "PUB=",
			Name:      "tg:bob #2",
			ExpiresAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC),
		},
	}
}

// withVideo gives the router a video, as main does with the embedded file.
func withVideo(r *router) {
	r.splitVideo = newVideoFile([]byte("mp4"))
}

func TestCreateKeyStepThreeAsksTheDevice(t *testing.T) {
	r, s := newRouter(keyService())
	withVideo(r)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("key:issue")))
	require.NoError(t, r.Handle(ctx, press("key:noname")))

	require.Len(t, s.sent, 3, "the name question, step 2, step 3")
	require.Contains(t, s.sent[1].Text, "Шаг 2")
	require.Nil(t, s.sent[1].Keyboard, "the buttons come with step 3")
	require.Contains(t, s.sent[2].Text, "Какое у вас устройство?")
	kb := s.sent[2].Keyboard.InlineKeyboard
	require.Equal(t, cbSplitVideo, kb[0][0].CallbackData, "Android")
	require.Equal(t, cbSplitVideo, kb[0][1].CallbackData, "Windows: the same video")
	require.Equal(t, cbSplitNone, kb[1][0].CallbackData, "the app has no such setting elsewhere")
	require.True(t, hasMenuButton(s.sent[2].Keyboard))
	require.Empty(t, s.videos, "no video before the user picks a device")
}

func TestNoStepThreeWithoutAVideo(t *testing.T) {
	r, s := newRouter(keyService())
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("key:issue")))
	require.NoError(t, r.Handle(ctx, press("key:noname")))

	require.Len(t, s.sent, 2, "the name question, then step 2 as the last one")
	require.True(t, hasMenuButton(s.sent[1].Keyboard))

	require.NoError(t, r.Handle(ctx, press(cbSplitVideo)))
	require.Len(t, s.sent, 2)
	require.Empty(t, s.videos)
}

func TestAndroidAndWindowsGetTheVideo(t *testing.T) {
	r, s := newRouter(&fakeService{})
	withVideo(r)

	require.NoError(t, r.Handle(context.Background(), press(cbSplitVideo)))
	r.Wait() // the upload runs in the background

	require.Len(t, s.sent, 1)
	require.Contains(t, s.sent[0].Text, "Раздельное туннелирование")
	require.True(t, hasMenuButton(s.sent[0].Keyboard))
	require.Len(t, s.videos, 1)
	require.Equal(t, int64(42), s.videos[0].ChatID)
	require.Equal(t, "mp4", string(s.videos[0].Data), "the first send uploads the file")
	require.Equal(t, splitVideoName, s.videos[0].Name)
	require.NotEmpty(t, s.videos[0].Caption)
}

func TestVideoIsUploadedOnlyOnce(t *testing.T) {
	r, s := newRouter(&fakeService{})
	withVideo(r)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press(cbSplitVideo)))
	r.Wait()
	require.NoError(t, r.Handle(ctx, press(cbSplitVideo)))

	require.Len(t, s.videos, 2)
	require.Empty(t, s.videos[0].FileID)
	require.Equal(t, "VID1", s.videos[1].FileID, "then by the ID Telegram gave it")
	require.Empty(t, s.videos[1].Data, "no second upload")
}

func TestOtherDevicesGetANote(t *testing.T) {
	r, s := newRouter(&fakeService{})
	withVideo(r)

	require.NoError(t, r.Handle(context.Background(), press(cbSplitNone)))

	require.Len(t, s.sent, 1)
	require.Contains(t, s.sent[0].Text, "iPhone")
	require.True(t, hasMenuButton(s.sent[0].Keyboard))
	require.Empty(t, s.videos)
}

func TestMenuHasTheAppsButtonOnlyWithAVideo(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			role: service.RoleUser,
		},
	)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, startUpdate("/menu")))
	for _, row := range s.sent[0].Keyboard.InlineKeyboard {
		require.NotEqual(t, cbSplitAsk, row[0].CallbackData, "no video, no button")
	}

	withVideo(r)
	require.NoError(t, r.Handle(ctx, startUpdate("/menu")))
	require.Equal(t, cbSplitAsk, s.sent[1].Keyboard.InlineKeyboard[3][0].CallbackData, "after buy, before support")

	require.NoError(t, r.Handle(ctx, press(cbSplitAsk)))
	require.Contains(t, s.sent[2].Text, "Какое у вас устройство?")
	require.Equal(t, cbSplitVideo, s.sent[2].Keyboard.InlineKeyboard[0][0].CallbackData)
}

// pressFrom is a button press in another user's chat.
func pressFrom(chatID int64, data string) tgbot.Update {
	u := press(data)
	u.CallbackQuery.From.ID = chatID
	u.CallbackQuery.Message.Chat.ID = chatID
	return u
}

// The upload is slow: it must not hold the user's request, must outlive the
// request's context, and two presses must not upload the file twice.
func TestVideoUploadRunsOnceInTheBackground(t *testing.T) {
	r, s := newRouter(&fakeService{})
	withVideo(r)
	s.videoHold = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	require.NoError(t, r.Handle(ctx, press(cbSplitVideo)), "returns while the upload hangs")
	require.NoError(t, r.Handle(ctx, pressFrom(43, cbSplitVideo)))
	cancel() // go-tgbot cancels the handler context when Handle returns
	require.Eventually(
		t,
		func() bool { return len(s.sentVideos()) == 1 },
		time.Second,
		time.Millisecond,
		"the upload started on the router's own context",
	)

	close(s.videoHold)
	r.Wait()
	videos := s.sentVideos()
	require.Len(t, videos, 2)
	require.NotEmpty(t, videos[0].Data)
	require.Equal(t, "VID1", videos[1].FileID, "the second user gets it by the ID of the first upload")
	require.Empty(t, videos[1].Data)
	require.ElementsMatch(t, []int64{42, 43}, []int64{videos[0].ChatID, videos[1].ChatID})
}

// failedTexts counts the "video did not come" messages sent so far.
func (s *fakeSender) failedTexts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.sent {
		if m.Text == splitVideoFailedText {
			n++
		}
	}
	return n
}

func TestFailedVideoUploadTellsTheUserAndIsTriedAgainAfterACoolDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, s := newRouter(&fakeService{})
		withVideo(r)
		s.videoErr = func(*outVideo) error {
			return errors.New("timeout")
		}
		ctx := context.Background()

		require.NoError(t, r.Handle(ctx, press(cbSplitVideo)))
		r.Wait()
		require.Equal(t, splitVideoFailedText, s.sent[len(s.sent)-1].Text)
		require.Empty(t, r.splitVideo.fileID())

		s.videoErr = nil
		require.NoError(t, r.Handle(ctx, press(cbSplitVideo)))
		r.Wait()
		require.Len(t, s.videos, 1, "no upload right after a failed one")
		require.Equal(t, 2, s.failedTexts(), "the text comes at once")

		time.Sleep(videoCoolDown)
		require.NoError(t, r.Handle(ctx, press(cbSplitVideo)))
		r.Wait()
		require.Len(t, s.videos, 2)
		require.NotEmpty(t, s.videos[1].Data, "uploaded again")
		require.Equal(t, "VID1", r.splitVideo.fileID())
	})
}

// Presses that come while an upload runs wait for its result: when it
// fails they get the text, and none of them uploads the file again.
func TestPressesDuringAFailingUploadDoNotUploadAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, s := newRouter(&fakeService{})
		withVideo(r)
		s.videoHold = make(chan struct{})
		s.videoErr = func(*outVideo) error {
			return errors.New("timeout")
		}
		ctx := context.Background()

		for chat := int64(42); chat < 47; chat++ {
			require.NoError(t, r.Handle(ctx, pressFrom(chat, cbSplitVideo)))
		}
		synctest.Wait()
		require.Len(t, s.sentVideos(), 1, "one upload, the others wait")
		require.Zero(t, s.failedTexts())

		close(s.videoHold)
		r.Wait()
		require.Len(t, s.videos, 1, "nobody uploaded after the failure")
		require.Equal(t, 5, s.failedTexts(), "everyone is told")
	})
}

// On shutdown the upload is cut and everyone who waited for it stops at
// once, without one more Telegram call each.
func TestShutdownEndsVideoWaitersWithoutMessages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, s := newRouter(&fakeService{})
		withVideo(r)
		s.videoHold = make(chan struct{})
		ctx := context.Background()
		for chat := int64(100); chat < 150; chat++ {
			require.NoError(t, r.Handle(ctx, pressFrom(chat, cbSplitVideo)))
		}
		synctest.Wait()
		began := time.Now()

		r.Close()

		require.Zero(t, time.Since(began), "nobody waited for anything")
		require.Len(t, s.videos, 1, "only the upload that was cut")
		require.Zero(t, s.failedTexts())
		require.Len(t, s.sent, 50, "only the 50 how-to texts")
	})
}

func refusedID() error {
	return &tgbot.APIError{
		Code:        400,
		Description: "Bad Request: wrong file identifier/HTTP URL specified",
	}
}

func TestVideoIDTelegramNoLongerTakesIsUploadedAgain(t *testing.T) {
	r, s := newRouter(&fakeService{})
	withVideo(r)
	r.splitVideo.id = "OLD"
	s.videoErr = func(v *outVideo) error {
		if v.FileID == "OLD" {
			return refusedID()
		}
		return nil
	}

	require.NoError(t, r.Handle(context.Background(), press(cbSplitVideo)))
	r.Wait()
	require.Len(t, s.videos, 2)
	require.Equal(t, "OLD", s.videos[0].FileID)
	require.NotEmpty(t, s.videos[1].Data, "the bad ID is dropped and the file uploaded once more")
	require.Equal(t, "VID1", r.splitVideo.fileID())
}

// Telegram may file the soundless mp4 as an animation and then refuse its
// ID in sendVideo: one re-upload, then the text for the cool-down, not an
// upload on every press.
func TestVideoIDRefusedAgainAfterAReuploadStopsUploading(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, s := newRouter(&fakeService{})
		withVideo(r)
		r.splitVideo.id = "OLD"
		s.videoErr = func(v *outVideo) error {
			if v.FileID != "" {
				return refusedID()
			}
			return nil
		}
		ctx := context.Background()

		require.NoError(t, r.Handle(ctx, press(cbSplitVideo)))
		r.Wait()
		require.Len(t, s.videos, 2, "refused, so uploaded again")
		require.Zero(t, s.failedTexts(), "the upload itself brought the video")

		require.NoError(t, r.Handle(ctx, press(cbSplitVideo)))
		require.NoError(t, r.Handle(ctx, press(cbSplitVideo)))
		r.Wait()
		require.Len(t, s.videos, 3, "the new ID is refused too: no more uploads for now")
		require.Equal(t, "VID1", s.videos[2].FileID)
		require.Equal(t, 2, s.failedTexts())

		time.Sleep(videoCoolDown)
		require.NoError(t, r.Handle(ctx, press(cbSplitVideo)))
		r.Wait()
		require.Len(t, s.videos, 4)
		require.NotEmpty(t, s.videos[3].Data, "after the cool-down one upload again")
	})
}

// A send by ID that fails for another reason (a timeout) says nothing about
// the ID: it is kept and the file is not uploaded.
func TestVideoByIDTimeoutKeepsTheID(t *testing.T) {
	r, s := newRouter(&fakeService{})
	withVideo(r)
	r.splitVideo.id = "VID1"
	s.videoErr = func(*outVideo) error {
		return errors.New("timeout")
	}

	require.ErrorContains(t, r.Handle(context.Background(), press(cbSplitVideo)), "timeout")
	r.Wait()
	require.Len(t, s.videos, 1, "no upload")
	require.Equal(t, "VID1", r.splitVideo.fileID())
	require.Equal(t, 1, s.failedTexts())
}

func TestVideoToAUserWhoBlockedTheBotKeepsTheID(t *testing.T) {
	r, s := newRouter(&fakeService{})
	withVideo(r)
	r.splitVideo.id = "VID1"
	s.videoErr = func(*outVideo) error {
		return &tgbot.APIError{
			Code:        403,
			Description: "Forbidden: bot was blocked by the user",
		}
	}

	require.Error(t, r.Handle(context.Background(), press(cbSplitVideo)))
	r.Wait()
	require.Len(t, s.videos, 1, "no upload for a chat that is gone")
	require.Equal(t, "VID1", r.splitVideo.fileID())
}
