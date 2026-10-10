package bot

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

func TestMyAccessHasReissueAndDeletePerKey(t *testing.T) {
	r, s := newRouter(ownKeyService())
	require.NoError(t, r.Handle(context.Background(), press(cbMyAccess)))
	kb := s.sent[0].Keyboard
	require.True(t, containsButton(
		buttonQuery{
			Keyboard: kb,
			Data:     cbReissueAsk + "PUB=",
		},
	))
	require.True(t, containsButton(
		buttonQuery{
			Keyboard: kb,
			Data:     cbDeleteAsk + "PUB=",
		},
	))
}

func TestReissueAsksThenSendsTheNewConfig(t *testing.T) {
	svc := ownKeyService()
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press(cbReissueAsk+"PUB=")))
	ask := s.sent[0]
	require.Contains(t, ask.Text, "iPhone")
	require.Contains(t, ask.Text, "перестанет работать")
	require.True(t, containsButton(
		buttonQuery{
			Keyboard: ask.Keyboard,
			Data:     cbReissue + "PUB=",
		},
	))
	require.True(t, hasMenuButton(ask.Keyboard), "cancel")
	require.Empty(t, svc.reissuedFor, "nothing before the confirm")

	require.NoError(t, r.Handle(ctx, press(cbReissue+"PUB=")))
	require.Equal(
		t,
		[]service.UserKey{
			{
				UserID:    42,
				PublicKey: "PUB=",
			},
		},
		svc.reissuedFor,
	)
	require.Len(t, s.files, 2, "new .conf and QR")
	require.Contains(t, s.sent[len(s.sent)-1].Text, "Старый ключ больше не работает")
}

func TestDeleteAsksWithTheLostDaysThenDeletes(t *testing.T) {
	svc := ownKeyService()
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press(cbDeleteAsk+"PUB=")))
	ask := s.sent[0]
	require.Contains(t, ask.Text, "iPhone")
	require.Contains(t, ask.Text, "20.10.2026", "the paid days that will be lost")
	require.True(t, containsButton(
		buttonQuery{
			Keyboard: ask.Keyboard,
			Data:     cbDelete + "PUB=",
		},
	))
	require.Empty(t, svc.deletedOwn)

	require.NoError(t, r.Handle(ctx, press(cbDelete+"PUB=")))
	require.Equal(
		t,
		[]service.UserKey{
			{
				UserID:    42,
				PublicKey: "PUB=",
			},
		},
		svc.deletedOwn,
	)
	require.Contains(t, s.sent[len(s.sent)-1].Text, "удалён")
}

func TestOwnKeyActionOnAGoneKey(t *testing.T) {
	svc := ownKeyService()
	svc.ownKeyErr = service.ErrNotFound
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press(cbReissue+"PUB=")))
	require.NoError(t, r.Handle(ctx, press(cbDelete+"PUB=")))
	for _, m := range s.sent {
		require.True(t, strings.Contains(m.Text, "не найден"), m.Text)
	}
	require.Empty(t, s.files)
}

func TestReissueUnreadableKeyExplains(t *testing.T) {
	svc := ownKeyService()
	svc.ownKeyErr = service.ErrUnreadable
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press(cbReissue+"PUB=")))
	require.Equal(t, keyUnreadableText, s.sent[len(s.sent)-1].Text)
}

func TestDeleteBlockedKeyExplains(t *testing.T) {
	svc := ownKeyService()
	svc.ownKeyErr = service.ErrBlocked
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press(cbDelete+"PUB=")))
	require.Equal(t, blockedKeyDeleteText, s.sent[len(s.sent)-1].Text)
}

func TestAskForAKeyNotInMyAccess(t *testing.T) {
	r, s := newRouter(ownKeyService())
	require.NoError(t, r.Handle(context.Background(), press(cbDeleteAsk+"OTHER=")))
	require.Contains(t, s.sent[0].Text, "не найден")
}

func ownKeyService() *fakeService {
	return &fakeService{
		role: service.RoleUser,
		access: []service.KeyInfo{
			{
				Peer: &service.Peer{
					PublicKey: "PUB=",
					Name:      "iPhone",
					IP:        "10.8.1.10",
					Enabled:   true,
					ExpiresAt: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC),
				},
			},
		},
		reissued: &service.Peer{
			PublicKey:  "NEW=",
			Name:       "iPhone",
			IP:         "10.8.1.10",
			PrivateKey: "NEWPRIV=",
		},
	}
}

func TestMyAccessWhenVPNStatsFailShowsKeysAndNote(t *testing.T) {
	svc := ownKeyService()
	svc.access[0].StatsUnavailable = true
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press(cbMyAccess)))
	require.Contains(t, s.sent[0].Text, "iPhone")
	require.Contains(t, s.sent[0].Text, "Данные о подключениях временно недоступны")

	require.NoError(t, r.Handle(ctx, press(cbReissueAsk+"PUB=")))
	require.Contains(t, s.sent[1].Text, "iPhone")
	require.NoError(t, r.Handle(ctx, press(cbDeleteAsk+"PUB=")))
	require.Contains(t, s.sent[2].Text, "iPhone")
}

func TestSkipWithoutOpenNameDialogIssuesNothing(t *testing.T) {
	svc := &fakeService{
		created: &service.Peer{
			PublicKey: "PUB=",
		},
	}
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press(cbKeyNoName)))

	require.Empty(t, svc.createdWith)
	require.Empty(t, s.files)
	require.Len(t, s.sent, 1)
	require.Contains(t, s.sent[0].Text, "Эта кнопка устарела")
	require.NotNil(t, s.sent[0].Keyboard)
}

// Reissue, delete and "config again" make the server work (a reissue
// rewrites its config under the service lock): one user can't loop them.
func TestOwnKeyActionsAreLimitedPerUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := adminService() // has the key PUB1=
		r, s := newRouter(svc)
		ctx := context.Background()
		for range keyActionsPerMinute {
			require.NoError(t, r.Handle(ctx, press(cbConfig+"PUB1=")))
		}
		files := len(s.files)

		for _, data := range []string{
			cbConfig + "PUB1=",
			cbReissue + "PUB1=",
			cbDelete + "PUB1=",
		} {
			require.NoError(t, r.Handle(ctx, press(data)))
			require.Equal(t, tooOftenText, s.sent[len(s.sent)-1].Text, data)
		}
		require.Len(t, s.files, files, "no config sent")
		require.Empty(t, svc.reissuedFor)
		require.Empty(t, svc.deletedOwn)

		require.NoError(t, r.Handle(ctx, press(cbReissueAsk+"PUB1=")))
		require.NotEqual(t, tooOftenText, s.sent[len(s.sent)-1].Text, "asking costs nothing and is not limited")
		require.NoError(t, r.Handle(ctx, pressFrom(43, cbConfig+"PUB1=")))
		require.Greater(t, len(s.files), files, "another user is not held back")

		time.Sleep(time.Minute)
		require.NoError(t, r.Handle(ctx, press(cbDelete+"PUB1=")))
		require.Len(t, svc.deletedOwn, 1, "a minute later it works again")
	})
}
