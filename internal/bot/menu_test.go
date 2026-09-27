package bot

import (
	"context"
	"errors"
	"testing"

	tgbot "github.com/irbgeo/go-tgbot"
	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// hasMenuButton reports whether the keyboard has the "back to menu" button.
func hasMenuButton(kb *tgbot.InlineKeyboardMarkup) bool {
	if kb == nil {
		return false
	}
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == cbMenu {
				return true
			}
		}
	}
	return false
}

func TestMenuButtonTurnsTheSameMessageIntoTheMenu(t *testing.T) {
	r, s := newRouter(adminService())
	u := press(cbMenu)
	u.CallbackQuery.Message.MessageID = 77

	require.NoError(t, r.Handle(context.Background(), u))
	require.Empty(t, s.sent, "no new message")
	require.Len(t, s.edits, 1)
	e := s.edits[0]
	require.Equal(t, int64(42), e.ChatID)
	require.Equal(t, int64(77), e.MessageID)
	require.Contains(t, e.Text, "Привет")
	require.Equal(t, cbCreateKey, e.Keyboard.InlineKeyboard[0][0].CallbackData)
	require.Contains(t, buttons(e), "a:users:0", "the admin gets the admin menu")
}

func TestMenuButtonEndsAWaitingQuestion(t *testing.T) {
	svc := &fakeService{
		created: &service.Peer{
			PublicKey: "PUB=",
		},
	}
	r, _ := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("key:issue")))
	require.NoError(t, r.Handle(ctx, press(cbMenu)))

	require.NoError(t, r.Handle(ctx, startUpdate("iPhone")))
	require.Empty(t, svc.createdWith)
}

func TestMenuFallsBackToANewMessageWhenEditFails(t *testing.T) {
	r, s := newRouter(adminService())
	s.editErr = errBoomBot

	require.NoError(t, r.Handle(context.Background(), press(cbMenu)))
	require.Len(t, s.sent, 1)
	require.Contains(t, s.sent[0].Text, "Привет")
}

func TestScreensHaveTheMenuButton(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		press string
		edit  bool
	}{
		{"users list", "a:users:0", true},
		{"user card", "a:user:7", true},
		{"stats", "a:stats", false},
		{"my access", cbMyAccess, false},
		{"support", cbSupport, false},
		{"terms", cbTerms, false},
		{"tariffs", cbBuy, false},
		{"step 1: apps", cbCreateKey, false},
		{"step 3: lists", cbBypass, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := adminService()
			svc.stats = &service.Stats{}
			r, s := newRouter(svc)
			require.NoError(t, r.Handle(ctx, press(tc.press)))
			if tc.edit {
				require.True(t, hasMenuButton(s.edits[len(s.edits)-1].Keyboard))
				return
			}
			require.True(t, hasMenuButton(s.sent[0].Keyboard))
		})
	}
}

func TestNoKeysScreenHasTheMenuButton(t *testing.T) {
	r, s := newRouter(&fakeService{})
	require.NoError(t, r.Handle(context.Background(), press(cbMyAccess)))
	require.True(t, hasMenuButton(s.sent[0].Keyboard))
}

var errBoomBot = errors.New("boom")
