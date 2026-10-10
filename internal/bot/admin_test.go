package bot

import (
	"context"
	"errors"
	"fmt"
	tgbot "github.com/irbgeo/go-tgbot"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

func adminService() *fakeService {
	users := make([]*service.User, 0, 12)
	for id := int64(1); id <= 12; id++ {
		users = append(
			users,
			&service.User{
				ID:       id,
				Username: fmt.Sprintf("u%d", id),
				Role:     service.RoleUser,
			},
		)
	}
	return &fakeService{
		role:  service.RoleAdmin,
		users: users,
		access: []service.KeyInfo{
			{
				Peer: &service.Peer{
					PublicKey: "PUB1=",
					UserID:    7,
					Name:      "tg:u7",
					IP:        "10.8.1.10",
					Enabled:   true,
					ExpiresAt: time.Now().Add(24 * time.Hour),
				},
			},
		},
	}
}

// buttons flattens a keyboard into callback data.
func buttons(m editMessage) []string {
	var out []string
	for _, row := range m.Keyboard.InlineKeyboard {
		for _, b := range row {
			out = append(out, b.CallbackData)
		}
	}
	return out
}

func TestAdminSeesAdminMenu(t *testing.T) {
	r, s := newRouter(adminService())

	require.NoError(t, r.Handle(context.Background(), startUpdate("/start")))
	var data []string
	for _, row := range s.sent[0].Keyboard.InlineKeyboard {
		data = append(data, row[0].CallbackData)
	}
	require.Subset(
		t,
		data,
		[]string{
			"a:users:0",
			"a:stats",
			"a:bc",
			"a:cfgs",
			"a:mnt",
		},
	)
}

func TestPlainUserHasNoAdminMenu(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			role: service.RoleUser,
		},
	)

	require.NoError(t, r.Handle(context.Background(), startUpdate("/start")))
	for _, row := range s.sent[0].Keyboard.InlineKeyboard {
		require.NotContains(t, row[0].CallbackData, "a:")
	}
}

func TestNonAdminAdminButtonsIgnored(t *testing.T) {
	svc := adminService()
	svc.role = service.RoleUnlimited
	r, s := newRouter(svc)

	for _, data := range []string{
		"a:users:0",
		"a:user:7",
		"a:dis:PUB1=",
		"a:delok:PUB1=",
	} {
		require.NoError(t, r.Handle(context.Background(), press(data)))
	}
	require.Empty(t, svc.calls, "a forged button does nothing")
	require.Empty(t, s.edits)
	require.Empty(t, s.sent)
}

func TestAdminUsersPages(t *testing.T) {
	r, s := newRouter(adminService())

	require.NoError(t, r.Handle(context.Background(), press("a:users:0")))
	first := s.edits[0]
	require.Contains(t, first.Text, "всего 12")
	require.Contains(t, first.Text, "1/2")
	b := buttons(first)
	require.Len(t, b, 12, "10 users + next + menu")
	require.Equal(t, "a:user:1", b[0])
	require.Equal(t, "a:users:1", b[10])
	require.Equal(t, cbMenu, b[11])

	require.NoError(t, r.Handle(context.Background(), press("a:users:1")))
	second := buttons(s.edits[1])
	require.Equal(t, []string{"a:user:11", "a:user:12", "a:users:0", cbMenu}, second, "2 users + back + menu")
}

func TestAdminNegativePageShowsFirst(t *testing.T) {
	r, s := newRouter(adminService())

	require.NoError(t, r.Handle(context.Background(), press("a:users:-1")))
	require.Contains(t, s.edits[0].Text, "1/2")
}

func TestAdminUserCardNotesUnavailableStats(t *testing.T) {
	svc := adminService()
	svc.access[0].StatsUnavailable = true
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press("a:user:7")))
	require.Contains(t, s.edits[0].Text, "Данные о подключениях временно недоступны")
}

func TestAdminUserCard(t *testing.T) {
	r, s := newRouter(adminService())

	require.NoError(t, r.Handle(context.Background(), press("a:user:7")))
	card := s.edits[0]
	require.Contains(t, card.Text, "@bob")
	require.Contains(t, card.Text, "Ключей: 1")
	require.Contains(t, card.Text, "10.8.1.10")
	require.Equal(
		t,
		[]string{
			"a:dis:PUB1=",
			"a:ext:PUB1=",
			"a:cfg:PUB1=",
			"a:del:PUB1=",
			"a:iss:7",
			"a:users:0",
			cbMenu,
		},
		buttons(card),
	)
}

func TestAdminKeyActionsRefreshCard(t *testing.T) {
	svc := adminService()
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("a:dis:PUB1=")))
	require.NoError(t, r.Handle(ctx, press("a:en:PUB1=")))
	require.NoError(t, r.Handle(ctx, press("a:ext:PUB1=")))

	require.Equal(
		t,
		[]string{
			"disable PUB1=",
			"enable PUB1=",
			"extend PUB1= 30",
		},
		svc.calls,
	)
	require.Len(t, s.edits, 3, "card redrawn after each action")
	require.Contains(t, s.edits[2].Text, "10.8.1.10")
}

func TestAdminDeleteNeedsConfirmation(t *testing.T) {
	svc := adminService()
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("a:del:PUB1=")))
	require.Empty(t, svc.calls, "not deleted yet")
	require.Contains(t, s.edits[0].Text, "Удалить")
	require.Equal(t, []string{"a:delok:PUB1=", "a:user:7"}, buttons(s.edits[0]))

	require.NoError(t, r.Handle(ctx, press("a:delok:PUB1=")))
	require.Equal(t, []string{"delete PUB1="}, svc.calls)
}

func TestAdminConfigSendsFiles(t *testing.T) {
	r, s := newRouter(adminService())

	require.NoError(t, r.Handle(context.Background(), press("a:cfg:PUB1=")))
	require.Len(t, s.files, 2, "config and QR")
	require.Equal(t, "key_u7.conf", s.files[0].Name)
}

func TestAdminIssueAsksForTerm(t *testing.T) {
	r, s := newRouter(adminService())

	require.NoError(t, r.Handle(context.Background(), press("a:iss:7")))
	require.Equal(
		t,
		[]string{
			"a:issd:7:7",
			"a:issd:7:30",
			"a:issd:7:90",
			"a:issd:7:365",
			"a:issd:7:0",
			"a:user:7",
		},
		buttons(s.edits[0]),
	)
}

func TestAdminIssueForTelegramUserSendsKeyToThem(t *testing.T) {
	svc := adminService()
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press("a:issd:7:30")))

	require.Equal(
		t,
		[]service.IssueInput{
			{
				UserID: 7,
				Days:   30,
			},
		},
		svc.issued,
	)
	require.Len(t, s.files, 2, "config and QR; the lists are the next step")
	for _, f := range s.files {
		require.Equal(t, int64(7), f.ChatID, "delivered to the user, not the admin")
	}
	require.Len(t, s.edits, 1, "admin sees the updated card")
}

func withPayments(svc *fakeService) *fakeService {
	svc.payments = []*service.Payment{
		{
			ChargeID:  "stxLongTelegramChargeID-0123456789-abcdefghijklmnopqrstuvwxyz",
			UserID:    7,
			Stars:     150,
			Days:      30,
			Applied:   true,
			CreatedAt: time.Date(2026, 9, 27, 12, 30, 0, 0, time.UTC),
		},
		{
			ChargeID:   "old-charge",
			UserID:     7,
			Stars:      400,
			Days:       90,
			CreatedAt:  time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
			RefundedAt: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
		},
	}
	return svc
}

func TestAdminCardShowsPaymentsAndRefundButton(t *testing.T) {
	r, s := newRouter(withPayments(adminService()))

	require.NoError(t, r.Handle(context.Background(), press("a:user:7")))
	card := s.edits[0]
	require.Contains(t, card.Text, "27.09.2026 15:30 по Москве — 150 ⭐, 1 месяц")
	require.Contains(t, card.Text, "400 ⭐, 3 месяца, ↩️ возвращено")

	refunds := 0
	for _, b := range buttons(card) {
		if strings.HasPrefix(b, cbAdminRef) {
			refunds++
		}
	}
	require.Equal(t, 1, refunds, "only the not-refunded payment")
	require.LessOrEqual(t, len(refundButton(card)), 64, "Telegram callback_data limit")
}

func TestAdminRefund(t *testing.T) {
	svc := withPayments(adminService())
	r, s := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:user:7")))
	ask := refundButton(s.edits[0])

	require.NoError(t, r.Handle(ctx, press(ask)))
	require.Empty(t, s.refunds, "asks first")
	confirm := buttons(s.edits[1])[0]
	require.Contains(t, s.edits[1].Text, "Вернуть 150 ⭐")

	require.NoError(t, r.Handle(ctx, press(confirm)))
	charge := "stxLongTelegramChargeID-0123456789-abcdefghijklmnopqrstuvwxyz"
	require.Equal(
		t,
		[]refundInput{
			{
				UserID:   7,
				ChargeID: charge,
			},
		},
		s.refunds,
	)
	require.Equal(t, []string{charge}, svc.refunded)
	require.Equal(t, int64(7), s.sent[0].ChatID, "the user is told")
	require.Contains(t, s.sent[0].Text, "150 ⭐")
	require.Contains(t, s.edits[2].Text, "↩️ возвращено", "card redrawn")

	require.NoError(t, r.Handle(ctx, press(confirm)))
	require.Len(t, s.refunds, 1, "a second press refunds nothing")
}

func TestAdminStats(t *testing.T) {
	svc := adminService()
	svc.stats = &service.Stats{
		Users:       12,
		Active:      9,
		Disabled:    2,
		Expiring7d:  3,
		Online:      4,
		SubnetUsed:  20,
		SubnetTotal: 254,
		Revenue30d:  1650,
		Payments30d: 5,
		TopTraffic: []service.KeyInfo{
			{
				Peer: &service.Peer{
					Name: "tg:bob",
					IP:   "10.8.1.10",
				},
				Received: 3 << 30,
				Sent:     1 << 30,
			},
		},
	}
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press("a:stats")))
	require.Len(t, s.sent, 1, "a new message: the menu stays")
	text := s.sent[0].Text
	for _, want := range []string{
		"Пользователей: 12",
		"активных 9",
		"отключённых 2",
		"истекают за 7 дней: 3",
		"В сети сейчас: 4",
		"20 из 254",
		"1650 ⭐ (5 оплат)",
		"tg:bob (10.8.1.10) — 4.0 ГБ",
	} {
		require.Contains(t, text, want)
	}
}

func TestAdminBroadcast(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
		8,
		9,
	}
	r, s := newRouter(svc)
	r.pause = 0
	s.fail[8] = true
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("a:bc")))
	require.Contains(t, s.sent[0].Text, "Напишите текст")

	require.NoError(t, r.Handle(ctx, startUpdate("Сервер переедет в субботу")))
	preview := s.sent[1]
	require.Contains(t, preview.Text, "3 пользователям")
	require.Contains(t, preview.Text, "Сервер переедет в субботу")
	require.Contains(t, preview.Keyboard.InlineKeyboard[0][0].CallbackData, "a:bcok:")
	require.Equal(t, "a:cancel", preview.Keyboard.InlineKeyboard[0][1].CallbackData)

	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	require.Contains(t, s.sent[2].Text, "началась", "the admin is told at once")
	require.Equal(t, int64(7), s.sent[3].ChatID)
	require.Equal(t, "Сервер переедет в субботу", s.sent[3].Text)
	report := s.sent[len(s.sent)-1]
	require.Equal(t, int64(42), report.ChatID)
	require.Contains(t, report.Text, "доставлено 2", "counts must not be symmetric")
	require.Contains(t, report.Text, "не доставлено 1")

	sent := len(s.sent)
	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	require.Len(t, s.sent, sent, "a second press sends nothing")
}

func TestAdminBroadcastCancel(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("a:bc")))
	require.NoError(t, r.Handle(ctx, startUpdate("oops")))
	require.NoError(t, r.Handle(ctx, press("a:cancel")))
	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()

	for _, m := range s.sent {
		require.NotEqual(t, int64(7), m.ChatID, "nothing sent to users")
	}
}

func TestAdminBroadcastStopsOnShutdownAndReports(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
		8,
		9,
	}
	r, s := newRouter(svc)
	r.pause = time.Hour // would hang without a ctx-aware pause
	ctx, cancel := context.WithCancel(context.Background())

	require.NoError(t, r.Handle(ctx, press("a:bc")))
	require.NoError(t, r.Handle(ctx, startUpdate("hello")))
	require.NoError(t, r.Handle(ctx, pressSend(s)))
	time.Sleep(50 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		r.Close() // shutdown: stops background jobs and waits for them
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast did not stop on shutdown")
	}
	report := s.sent[len(s.sent)-1]
	require.Equal(t, int64(42), report.ChatID)
	require.Contains(t, report.Text, "остановлена", "not \"done\": it was cut short")
	require.Contains(t, report.Text, "доставлено 1")
	require.Contains(t, report.Text, "не отправлено 2", "the users it never tried")
	require.Contains(
		t,
		configsReportText(
			broadcastResult{
				Sent:    1,
				Skipped: 2,
			},
		),
		"не отправлено 2",
	)
}

func TestAdminPendingKeepsWaitingOnNonText(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("a:bc")))
	require.NoError(t, r.Handle(ctx, startUpdate(""))) // a photo: no text
	require.Contains(t, s.sent[len(s.sent)-1].Text, "Нужен текст")

	require.NoError(t, r.Handle(ctx, startUpdate("first")))
	require.NoError(t, r.Handle(ctx, startUpdate("second")))
	require.Contains(t, s.sent[len(s.sent)-1].Text, "second", "new text replaces the preview")

	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	var toUser []string
	for _, m := range s.sent {
		if m.ChatID == 7 {
			toUser = append(toUser, m.Text)
		}
	}
	require.Equal(t, []string{"second"}, toUser)
}

func TestAdminRefundInProgressIsNotStartedTwice(t *testing.T) {
	svc := withPayments(adminService())
	r, s := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:user:7")))
	ask := refundButton(s.edits[0])
	confirm := "a:refok:" + ask[len("a:ref:"):]
	charge := "stxLongTelegramChargeID-0123456789-abcdefghijklmnopqrstuvwxyz"
	require.True(t, r.refunds.start(charge)) // another press is refunding it right now

	require.NoError(t, r.Handle(ctx, press(confirm)))
	require.Empty(t, s.refunds, "no second refund call")
	require.Empty(t, s.sent, "no false error for the admin")
}

func TestAdminBroadcastOutlivesTheHandlerContext(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
		8,
		9,
	}
	r, s := newRouter(svc)
	r.pause = 10 * time.Millisecond
	require.NoError(t, r.Handle(context.Background(), press("a:bc")))
	require.NoError(t, r.Handle(context.Background(), startUpdate("hello")))

	// go-tgbot's Dispatcher cancels the handler's ctx as soon as Handle returns.
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, r.Handle(ctx, pressSend(s)))
	cancel()
	r.Wait()

	delivered := 0
	for _, m := range s.sent {
		if m.ChatID >= 7 && m.ChatID <= 9 {
			delivered++
		}
	}
	require.Equal(t, 3, delivered, "every recipient gets it")
	require.Contains(t, s.sent[len(s.sent)-1].Text, "доставлено 3")
}

func TestAdminUpdateConfigs(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
		8,
	}
	svc.access = append(
		svc.access,
		service.KeyInfo{
			Peer: &service.Peer{
				PublicKey: "NOPRIV=",
				Enabled:   true,
			},
		},
		service.KeyInfo{
			Peer: &service.Peer{
				PublicKey: "OFF=",
			},
		},
	)
	r, s := newRouter(svc)
	r.pause = 0
	s.fail[8] = true
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("a:cfgs")))
	ask := s.sent[0]
	require.Contains(t, ask.Text, "2 пользователям")
	require.Contains(t, ask.Text, "ENDPOINT_HOST")
	require.Contains(t, ask.Text, configsNoticeText, "the admin sees exactly what users get")
	require.Contains(t, ask.Keyboard.InlineKeyboard[0][0].CallbackData, "a:cfgsok:")
	require.Equal(t, "a:cancel", ask.Keyboard.InlineKeyboard[0][1].CallbackData)

	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	require.Contains(t, s.sent[1].Text, "Рассылаю", "the admin is told at once")
	notice := s.sent[2]
	require.Equal(t, int64(7), notice.ChatID)
	require.Contains(t, notice.Text, "обновите ключ")
	require.Contains(t, notice.Text, "📋 Мой доступ")
	require.Contains(t, notice.Text, "📄 Конфиг")
	require.Equal(t, cbMyAccess, notice.Keyboard.InlineKeyboard[0][0].CallbackData, "opens My access")
	require.Empty(t, s.files, "no configs are sent: the user gets them from My access")
	require.Len(t, s.sent, 4, "ask, started, one notice, report")
	report := s.sent[len(s.sent)-1]
	require.Equal(t, int64(42), report.ChatID)
	require.Contains(t, report.Text, "получили 1")
	require.Contains(t, report.Text, "не доставлено 1")
}

func TestAdminUpdateConfigsOnlyOnceAtATime(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	r.pause = 0
	ctx := context.Background()

	require.True(t, r.jobs.reserve()) // another mass send is running
	require.NoError(t, r.Handle(ctx, press("a:cfgs")))
	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	require.Empty(t, s.sentTo(7), "nothing sent while another run is going")
	require.Contains(t, s.sent[len(s.sent)-1].Text, "уже идёт")

	r.jobs.release()
	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	require.Len(t, s.sentTo(7), 1, "the same button works once the slot is free")
}

func TestAdminMaintenanceIsOneToggleButton(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	r.pause = 0
	ctx := context.Background()
	menuButton := func() tgbot.InlineKeyboardButton {
		require.NoError(t, r.Handle(ctx, startUpdate("/menu")))
		for _, row := range s.sent[len(s.sent)-1].Keyboard.InlineKeyboard {
			for _, b := range row {
				if b.CallbackData == "a:mnt" {
					return b
				}
			}
		}
		t.Fatal("no maintenance button in the menu")
		return tgbot.InlineKeyboardButton{}
	}

	require.Equal(t, "🛠 Техработы", menuButton().Text)

	// start: the usual preview, then send
	require.NoError(t, r.Handle(ctx, press("a:mnt")))
	preview := s.sent[len(s.sent)-1]
	require.Contains(t, preview.Text, "1 пользователям")
	require.Contains(t, strings.ToLower(preview.Text), "технические работы")
	require.Contains(t, preview.Keyboard.InlineKeyboard[0][0].CallbackData, "a:bcok:")
	require.False(t, r.maint.on(), "nothing changes before send (/menu here would drop the preview)")

	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	require.Contains(t, strings.ToLower(s.sentTo(7)[0].Text), "технические работы")
	require.Equal(t, "✅ Закончить техработы", menuButton().Text)

	// end: same button, the "over" text
	require.NoError(t, r.Handle(ctx, press("a:mnt")))
	require.Contains(t, strings.ToLower(s.sent[len(s.sent)-1].Text), "работы закончены")
	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	require.Contains(t, strings.ToLower(s.sentTo(7)[1].Text), "работы закончены")
	require.Equal(t, "🛠 Техработы", menuButton().Text)
}

func TestMaintenanceCancelKeepsTheState(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("a:mnt")))
	require.NoError(t, r.Handle(ctx, press("a:cancel")))
	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	require.False(t, r.maint.on())
	require.Empty(t, s.sentTo(7))
}

func TestMaintenanceStateSurvivesARestart(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "maintenance")
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	cfg := &config.Config{
		MaintenanceFlag: flag,
	}
	r, s := newRouterWith(svc, cfg)
	r.pause = 0
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:mnt")))
	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	require.FileExists(t, flag)

	restarted, _ := newRouterWith(svc, cfg)
	require.True(t, restarted.maint.on(), "a new process reads the flag file")
}

// sentTo returns the messages sent to one chat.
func (s *fakeSender) sentTo(chatID int64) []outMessage {
	var out []outMessage
	for _, m := range s.sent {
		if m.ChatID == chatID {
			out = append(out, m)
		}
	}
	return out
}

func TestCardKeyButtonsNameTheKey(t *testing.T) {
	kb := userCardKeyboard(
		cardView{
			UserID: 7,
			Keys: []service.KeyInfo{
				{
					Peer: &service.Peer{
						PublicKey: "A=",
						Name:      "mac",
						IP:        "10.8.1.2",
						Enabled:   true,
					},
				},
				{
					Peer: &service.Peer{
						PublicKey: "B=",
						Name:      "Admin [iOS 26.6.1]",
						IP:        "10.8.1.1",
					},
				},
			},
		},
	)
	byData := map[string]string{}
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			byData[b.CallbackData] = b.Text
		}
	}
	require.Contains(t, byData["a:dis:A="], "mac")
	require.Contains(t, byData["a:cfg:A="], "mac")
	require.Contains(t, byData["a:del:A="], "mac")
	require.Contains(t, byData["a:en:B="], "Admin [iOS 26.6.1]")
	require.Contains(t, byData["a:del:B="], "Admin [iOS 26.6.1]")
}

func TestConfigCaptionNamesTheKey(t *testing.T) {
	require.Contains(
		t,
		keyCaption(
			&service.Peer{
				Name: "mac",
			},
		),
		"mac",
	)
}

func TestAdminRefundTellsWhenTheRecordFailed(t *testing.T) {
	svc := withPayments(adminService())
	svc.refundMarkErr = errors.New("mongo down")
	r, s := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:user:7")))
	ask := refundButton(s.edits[0])
	require.NoError(t, r.Handle(ctx, press(ask)))
	require.NoError(t, r.Handle(ctx, press(buttons(s.edits[1])[0])))

	require.Len(t, s.refunds, 1)
	var told bool
	for _, m := range s.sentTo(42) {
		told = told || strings.Contains(m.Text, "не записан")
	}
	require.True(t, told, "the admin knows the Stars went back but the record did not change")
}

func TestUnfinishedPaymentNamesTheKeyToCheck(t *testing.T) {
	text := unfinishedPaymentsText(
		[]*service.Payment{
			{
				UserID:  7,
				Stars:   150,
				Days:    30,
				PeerKey: "ABCDEFGHIJ=",
			},
		},
	)
	require.Contains(t, text, "ABCDEFGH")
	require.Contains(t, text, "могли уже добавиться")
}

func TestAdminCardForeverKeyHasNoExtend(t *testing.T) {
	svc := adminService()
	forever := svc.access[0]
	forever.Peer.ExpiresAt = time.Time{}
	timed := &service.Peer{
		PublicKey: "PUB2=",
		UserID:    7,
		Name:      "tg:u7 #2",
		IP:        "10.8.1.11",
		Enabled:   true,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	svc.access = append(svc.access, service.KeyInfo{Peer: timed})
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press("a:user:7")))
	got := buttons(s.edits[0])
	require.NotContains(t, got, "a:ext:"+forever.Peer.PublicKey)
	require.Contains(t, got, "a:ext:PUB2=")
}

// refundButton is the "return the Stars" button of a user card ("" = none).
func refundButton(card editMessage) string {
	for _, b := range buttons(card) {
		if strings.HasPrefix(b, cbAdminRef) {
			return b
		}
	}
	return ""
}

func TestAdminRefundThatTelegramRefusesChangesNothing(t *testing.T) {
	svc := withPayments(adminService())
	r, s := newRouter(svc)
	s.refundErr = errors.New("telegram down")
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:user:7")))
	require.NoError(t, r.Handle(ctx, press(refundButton(s.edits[0]))))

	err := r.Handle(ctx, press(buttons(s.edits[1])[0]))

	require.ErrorContains(t, err, "telegram down", "for the log")
	require.Len(t, s.refunds, 1, "it was tried")
	require.Empty(t, svc.refunded, "not recorded as returned")
	require.Empty(t, s.sentTo(7), "the user is told nothing")
	admin := s.sentTo(42)
	require.Len(t, admin, 1)
	require.Contains(t, admin[0].Text, "Не получилось")
	require.Len(t, s.edits, 2, "the card is not redrawn as if it worked")
}

// The Stars went back earlier (the user was told then) but the record
// failed, so the button stayed. Pressing it again records the refund and
// does not tell the user a second time.
func TestAdminRefundOfAnAlreadyReturnedChargeDoesNotTellTheUserAgain(t *testing.T) {
	svc := withPayments(adminService())
	r, s := newRouter(svc)
	s.refundAlready = true
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:user:7")))
	require.NoError(t, r.Handle(ctx, press(refundButton(s.edits[0]))))

	require.NoError(t, r.Handle(ctx, press(buttons(s.edits[1])[0])))

	require.Len(t, svc.refunded, 1, "now it is recorded")
	require.Empty(t, s.sentTo(7), "the user heard about it the first time")
	require.Contains(t, s.edits[2].Text, "↩️ возвращено")
}

// confirmButtons are the "send" buttons of every preview sent so far, oldest
// first: each carries its own preview's token.
func confirmButtons(s *fakeSender) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, m := range s.sent {
		if m.Keyboard == nil || len(m.Keyboard.InlineKeyboard) == 0 {
			continue
		}
		data := m.Keyboard.InlineKeyboard[0][0].CallbackData
		if strings.HasPrefix(data, cbAdminBcOK+":") || strings.HasPrefix(data, cbAdminCfgOK+":") {
			out = append(out, data)
		}
	}
	return out
}

// pressSend presses "send" under the newest preview.
func pressSend(s *fakeSender) tgbot.Update {
	all := confirmButtons(s)
	return press(all[len(all)-1])
}

func TestSendUnderAnOlderPreviewSendsNothing(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	r.pause = 0
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:bc")))
	require.NoError(t, r.Handle(ctx, startUpdate("first draft")))
	require.NoError(t, r.Handle(ctx, startUpdate("final text")))
	old := confirmButtons(s)[0]

	require.NoError(t, r.Handle(ctx, press(old)))
	r.Wait()
	require.Empty(t, s.sentTo(7), "the old button must not send the newer text")
	require.Equal(t, oldPreviewText, s.sent[len(s.sent)-1].Text)

	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	require.Equal(t, "final text", s.sentTo(7)[0].Text, "the newest preview is still there")
}

func TestConfigsSendButtonWorksOnceAndOnlyUnderItsOwnQuestion(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	r.pause = 0
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press(cbAdminCfgOK+":forged")))
	r.Wait()
	require.Empty(t, s.sentTo(7), "no question was asked")

	require.NoError(t, r.Handle(ctx, press("a:cfgs")))
	require.NoError(t, r.Handle(ctx, startUpdate("just a text")))
	require.Len(t, confirmButtons(s), 1, "a text at the configs question is not a broadcast")
	require.NoError(t, r.Handle(ctx, press("a:cfgs")))
	require.NoError(t, r.Handle(ctx, press(confirmButtons(s)[0])))
	r.Wait()
	require.Empty(t, s.sentTo(7), "the button of the older question")

	require.NoError(t, r.Handle(ctx, pressSend(s)))
	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	require.Len(t, s.sentTo(7), 1, "sent once; the second press finds nothing")

	require.NoError(t, r.Handle(ctx, press("a:cfgs")))
	require.NoError(t, r.Handle(ctx, press("a:cancel")))
	require.NoError(t, r.Handle(ctx, pressSend(s)))
	r.Wait()
	require.Len(t, s.sentTo(7), 1, "cancelled")
}

// Updates of one chat are handled in order, so a double press is two full
// runs: without a guard "+30 days" adds 60 and "issue" makes two keys.
func TestAdminDoublePressExtendsAndIssuesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := adminService()
		r, s := newRouter(svc)
		ctx := context.Background()

		require.NoError(t, r.Handle(ctx, press("a:ext:PUB1=")))
		require.NoError(t, r.Handle(ctx, press("a:ext:PUB1=")))
		require.Equal(t, []string{"extend PUB1= 30"}, svc.calls)
		require.Equal(t, repeatedPressText, s.sent[len(s.sent)-1].Text)

		require.NoError(t, r.Handle(ctx, press("a:issd:7:30")))
		require.NoError(t, r.Handle(ctx, press("a:issd:7:30")))
		require.Len(t, svc.issued, 1)
		require.NoError(t, r.Handle(ctx, press("a:issd:7:90")))
		require.Len(t, svc.issued, 2, "another term is another action")

		time.Sleep(repeatPressGap)
		require.NoError(t, r.Handle(ctx, press("a:ext:PUB1=")))
		require.Len(t, svc.calls, 2, "on purpose, a little later: fine")
	})
}

func TestAdminTextReturnsADatabaseError(t *testing.T) {
	svc := adminService()
	r, _ := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:bc")))

	svc.userErr = errors.New("mongo down")
	require.ErrorContains(t, r.Handle(ctx, startUpdate("hello")), "mongo down", "not dropped without a word")

	svc.userErr = service.ErrNotFound
	require.NoError(t, r.Handle(ctx, startUpdate("hello")), "an unknown user is just not an admin")
}

func TestAdminUsersErrorIsExplained(t *testing.T) {
	svc := adminService()
	svc.usersErr = errors.New("mongo down")
	r, s := newRouter(svc)

	err := r.Handle(context.Background(), press("a:users:0"))
	require.ErrorContains(t, err, "mongo down", "for the log")
	require.Len(t, s.sentTo(42), 1, "the admin is told, like after every other admin button")
	require.Contains(t, s.sentTo(42)[0].Text, "Не получилось")
	require.NotContains(t, s.sentTo(42)[0].Text, "mongo")
}

func TestMaintenanceFlagFileTurnsOff(t *testing.T) {
	flag := newMaintFlag(filepath.Join(t.TempDir(), "maintenance"))

	require.NoError(t, flag.set(true))
	require.True(t, flag.on())
	require.NoError(t, flag.set(false))
	require.False(t, flag.on())
	require.NoError(t, flag.set(false), "already off: nothing to remove is fine")

	gone := newMaintFlag(filepath.Join(t.TempDir(), "no-such-dir", "maintenance"))
	require.Error(t, gone.set(true))
}

// After an action on a key the card of the key's OWNER (7) is redrawn, not
// the card of the admin who pressed (42).
func TestAdminKeyActionsRedrawTheOwnersCard(t *testing.T) {
	for _, data := range []string{
		"a:dis:PUB1=",
		"a:en:PUB1=",
		"a:ext:PUB1=",
		"a:delok:PUB1=",
	} {
		svc := adminService()
		r, s := newRouter(svc)

		require.NoError(t, r.Handle(context.Background(), press(data)))

		require.Len(t, s.edits, 1, data)
		require.Equal(t, []int64{42, 7}, svc.askedUsers, "%s: the admin's role, then the owner", data)
		require.Equal(t, []int64{7}, svc.askedAccess, data)
		require.Equal(t, []int64{7}, svc.askedPayments, data)
	}
}

// The user blocked the bot: the key issued by hand goes to the admin, who
// passes it on.
func TestAdminIssueFallsBackToTheAdminWhenTheUserIsUnreachable(t *testing.T) {
	svc := adminService()
	r, s := newRouter(svc)
	s.fail[7] = true

	require.NoError(t, r.Handle(context.Background(), press("a:issd:7:30")))

	require.Len(t, svc.issued, 1)
	require.Empty(t, s.sentTo(7))
	admin := s.sentTo(42)
	require.Len(t, admin, 1)
	require.Equal(t, importText, admin[0].Text, "the admin gets the key with the import steps")
	require.Equal(t, int64(42), s.files[len(s.files)-1].ChatID, "and its config and QR code")
	require.Len(t, s.edits, 1, "then the user card")
}

func TestAdminIssueBadButton(t *testing.T) {
	for _, data := range []string{
		"a:issd:7",
		"a:issd:x:30",
		"a:issd:0:30",
		"a:issd:7:month",
	} {
		svc := adminService()
		r, _ := newRouter(svc)

		require.Error(t, r.Handle(context.Background(), press(data)), data)
		require.Empty(t, svc.issued, data)
	}
}
