package bot

import (
	"context"
	"testing"
	"time"

	"github.com/irbgeo/go-tgbot"
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

func videoUpdate(fileID string) tgbot.Update {
	u := startUpdate("")
	u.Message.Video = &tgbot.Video{
		FileID: fileID,
	}
	return u
}

func TestCreateKeyStepThreeAsksTheDevice(t *testing.T) {
	r, s := newRouter(keyService())
	r.splitVideo = "VID1"
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

	require.NoError(t, r.Handle(ctx, press(cbSplitVideo)), "a button from before the video was removed")
	require.Len(t, s.sent, 2)
	require.Empty(t, s.videos)
}

func TestAndroidAndWindowsGetTheVideo(t *testing.T) {
	r, s := newRouter(&fakeService{})
	r.splitVideo = "VID1"

	require.NoError(t, r.Handle(context.Background(), press(cbSplitVideo)))

	require.Len(t, s.sent, 1)
	require.Contains(t, s.sent[0].Text, "Раздельное туннелирование")
	require.True(t, hasMenuButton(s.sent[0].Keyboard))
	require.Len(t, s.videos, 1)
	require.Equal(t, int64(42), s.videos[0].ChatID)
	require.Equal(t, "VID1", s.videos[0].FileID, "sent by its Telegram ID, not uploaded again")
	require.NotEmpty(t, s.videos[0].Caption)
}

func TestOtherDevicesGetANote(t *testing.T) {
	r, s := newRouter(&fakeService{})
	r.splitVideo = "VID1"

	require.NoError(t, r.Handle(context.Background(), press(cbSplitNone)))

	require.Len(t, s.sent, 1)
	require.Contains(t, s.sent[0].Text, "iPhone")
	require.True(t, hasMenuButton(s.sent[0].Keyboard))
	require.Empty(t, s.videos)
}

func TestAdminVideoGetsItsID(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			role: service.RoleAdmin,
		},
	)

	require.NoError(t, r.Handle(context.Background(), videoUpdate("VID9")))

	require.Len(t, s.sent, 1)
	require.Contains(t, s.sent[0].Text, "VID9")
	require.Contains(t, s.sent[0].Text, "SPLIT_VIDEO_FILE_ID")
}

func TestUserVideoIsNotAnswered(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			role: service.RoleUser,
		},
	)

	require.NoError(t, r.Handle(context.Background(), videoUpdate("VID9")))

	require.Empty(t, s.sent, "the ID is for admins only")
}

// A user who is asked for a key name and sends a video is asked again, as
// with any other message without text.
func TestUserVideoStillAnswersThePendingQuestion(t *testing.T) {
	svc := &fakeService{
		role: service.RoleUser,
	}
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("key:issue")))
	require.NoError(t, r.Handle(ctx, videoUpdate("VID9")))

	require.Empty(t, svc.createdWith)
	require.Equal(t, "key:noname", s.sent[len(s.sent)-1].Keyboard.InlineKeyboard[0][0].CallbackData)
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

	r.splitVideo = "VID1"
	require.NoError(t, r.Handle(ctx, startUpdate("/menu")))
	require.Equal(t, cbSplitAsk, s.sent[1].Keyboard.InlineKeyboard[3][0].CallbackData, "after buy, before support")

	require.NoError(t, r.Handle(ctx, press(cbSplitAsk)))
	require.Contains(t, s.sent[2].Text, "Какое у вас устройство?")
	require.Equal(t, cbSplitVideo, s.sent[2].Keyboard.InlineKeyboard[0][0].CallbackData)
}
