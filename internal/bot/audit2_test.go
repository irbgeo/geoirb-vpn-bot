package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"
	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

func inGroup(u tgbot.Update) tgbot.Update {
	if u.Message != nil {
		u.Message.Chat.Type = "group"
	}
	if u.CallbackQuery != nil {
		u.CallbackQuery.Message.Chat.Type = "supergroup"
	}
	return u
}

func TestGroupChatsAreIgnored(t *testing.T) {
	svc := adminService()
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, inGroup(startUpdate("/menu"))))
	require.NoError(t, r.Handle(ctx, inGroup(press("my"))))
	require.NoError(t, r.Handle(ctx, inGroup(press("a:user:7"))))
	require.Empty(t, s.sent, "nothing is posted to a group")
	require.Empty(t, s.edits)
	require.Empty(t, s.files)
	require.Empty(t, svc.registered)
}

func TestKeyNamePromptBelongsToTheUserWhoAsked(t *testing.T) {
	svc := &fakeService{
		created: &service.Peer{
			PublicKey: "PUB=",
		},
	}
	r, _ := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("key:issue")))

	other := startUpdate("iPhone")
	other.Message.From.ID = 99
	require.NoError(t, r.Handle(ctx, other))
	require.Empty(t, svc.createdWith, "someone else's text is not the name")
}

func TestExpiredPreviewIsNotSent(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	r.pause = 0
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:mnt")))
	p, ok := r.dialogs.peek(42)
	require.True(t, ok)
	p.At = time.Now().Add(-pendingTTL - time.Minute)
	r.dialogs.set(p)

	require.NoError(t, r.Handle(ctx, press("a:bcok")))
	r.Wait()
	require.Empty(t, sentTo(s, 7))
	require.False(t, r.maint.on())
	require.Contains(t, s.sent[len(s.sent)-1].Text, "устарел")
}

func TestMaintenancePreviewAlreadyDoneIsNotSentAgain(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	r.pause = 0
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:mnt"))) // preview "started"
	require.NoError(t, r.maint.set(true))             // someone else turned it on meanwhile

	require.NoError(t, r.Handle(ctx, press("a:bcok")))
	r.Wait()
	require.Empty(t, sentTo(s, 7), "users are not told again")
	require.Contains(t, s.sent[len(s.sent)-1].Text, "уже")
}

func TestBroadcastKeptWhenRecipientsFail(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	r.pause = 0
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:bc")))
	require.NoError(t, r.Handle(ctx, startUpdate("hello")))

	svc.recipientsErr = errors.New("mongo down")
	require.Error(t, r.Handle(ctx, press("a:bcok")))
	svc.recipientsErr = nil
	require.NoError(t, r.Handle(ctx, press("a:bcok")))
	r.Wait()
	require.Equal(t, "hello", sentTo(s, 7)[0].Text, "a second press sends it")
}

func TestPreviewErrorIsShownToTheAdmin(t *testing.T) {
	svc := adminService()
	svc.recipientsErr = errors.New("mongo down")
	r, s := newRouter(svc)

	err := r.Handle(context.Background(), press("a:mnt"))
	require.ErrorContains(t, err, "mongo down")
	require.NotEmpty(t, sentTo(s, 42), "the admin sees that it failed")
}

func TestCustomTextAtAMaintenancePreviewStillFlips(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	r.pause = 0
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:mnt")))
	require.NoError(t, r.Handle(ctx, startUpdate("Работы до 20:00")))
	require.NoError(t, r.Handle(ctx, press("a:bcok")))
	r.Wait()

	require.Equal(t, "Работы до 20:00", sentTo(s, 7)[0].Text)
	require.True(t, r.maint.on(), "own wording, same switch")
}

func TestFailedMaintenanceFlipStopsTheBroadcast(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	r.maint = newMaintFlag("/nonexistent-dir/maintenance")
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:mnt")))

	require.Error(t, r.Handle(ctx, press("a:bcok")))
	r.Wait()
	require.Empty(t, sentTo(s, 7), "users are not told about maintenance that is not recorded")
}

func TestOnlyOneMassSendAtATime(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	ctx := context.Background()
	require.True(t, r.jobs.reserve()) // "Обновить конфиги" is running
	require.NoError(t, r.Handle(ctx, press("a:bc")))
	require.NoError(t, r.Handle(ctx, startUpdate("hello")))

	require.NoError(t, r.Handle(ctx, press("a:bcok")))
	require.Empty(t, sentTo(s, 7))
	require.Contains(t, s.sent[len(s.sent)-1].Text, "уже идёт")

	r.jobs.release()
	r.pause = 0
	require.NoError(t, r.Handle(ctx, press("a:bcok")))
	r.Wait()
	require.Equal(t, "hello", sentTo(s, 7)[0].Text, "the preview waited for the free slot")
}

func TestKeyCreatedButNotDeliveredTellsTheUser(t *testing.T) {
	svc := &fakeService{
		created: &service.Peer{
			PublicKey: "PUB=",
		},
		configErr: errors.New("docker down"),
	}
	r, s := newRouter(svc)

	err := r.Handle(context.Background(), press("key:noname"))
	require.Error(t, err)
	last := s.sent[len(s.sent)-1]
	require.Contains(t, last.Text, "Мой доступ")
	require.Equal(t, cbMyAccess, last.Keyboard.InlineKeyboard[0][0].CallbackData)
}

func TestAdminKeyActionUnknownNameIsAnError(t *testing.T) {
	r, _ := newRouter(adminService())
	err := r.adminKeyAction(
		context.Background(),
		adminAction{
			ChatID: 42,
			Name:   "nope",
			Arg:    "PUB=",
		},
	)
	require.Error(t, err)
}

func TestCardShowsOnlyRecentPayments(t *testing.T) {
	var ps []*service.Payment
	for i := range 25 {
		ps = append(
			ps,
			&service.Payment{
				ChargeID:  fmt.Sprintf("c%d", i),
				Stars:     150,
				Days:      30,
				CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			},
		)
	}
	text := paymentsText(ps)
	require.Equal(t, cardPaymentsLimit, strings.Count(text, "• "))
	require.Contains(t, text, "ещё 15")
	kb := userCardKeyboard(
		cardView{
			UserID:   7,
			Payments: ps,
		},
	)
	refunds := 0
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, cbAdminRef) {
				refunds++
			}
		}
	}
	require.Equal(t, cardPaymentsLimit, refunds)
}

func TestKeyLimitTextsFollowTheConstant(t *testing.T) {
	n := fmt.Sprint(service.MaxUnlimitedKeys)
	require.Contains(t, keyLimitText, n)
	require.Contains(
		t,
		greeting(
			&service.User{
				Role: service.RoleUnlimited,
			},
		),
		n,
	)
}
