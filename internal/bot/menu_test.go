package bot

import (
	"context"
	"errors"
	"testing"

	tgbot "github.com/irbgeo/go-tgbot"
	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

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
		{
			name:  "users list",
			press: "a:users:0",
			edit:  true,
		},
		{
			name:  "user card",
			press: "a:user:7",
			edit:  true,
		},
		{
			name:  "stats",
			press: "a:stats",
			edit:  false,
		},
		{
			name:  "my access",
			press: cbMyAccess,
			edit:  false,
		},
		{
			name:  "support",
			press: cbSupport,
			edit:  false,
		},
		{
			name:  "terms",
			press: cbTerms,
			edit:  false,
		},
		{
			name:  "tariffs",
			press: cbBuy,
			edit:  false,
		},
		{
			name:  "step 1: apps",
			press: cbCreateKey,
			edit:  false,
		},
		{
			name:  "step 3: lists",
			press: cbBypass,
			edit:  false,
		},
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

var errBoomBot = errors.New("boom")
