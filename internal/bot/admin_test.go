package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
				},
			},
		},
	}
}

// buttons flattens a keyboard into callback data.
func buttons(m EditMessage) []string {
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
	require.Len(t, b, 11, "10 users + next")
	require.Equal(t, "a:user:1", b[0])
	require.Equal(t, "a:users:1", b[10])

	require.NoError(t, r.Handle(context.Background(), press("a:users:1")))
	second := buttons(s.edits[1])
	require.Equal(t, []string{"a:user:11", "a:user:12", "a:users:0"}, second, "2 users + back")
}

func TestAdminNegativePageShowsFirst(t *testing.T) {
	r, s := newRouter(adminService())

	require.NoError(t, r.Handle(context.Background(), press("a:users:-1")))
	require.Contains(t, s.edits[0].Text, "1/2")
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
	require.Equal(t, "vpn_u7.conf", s.files[0].Name)
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

	var refunds []string
	for _, b := range buttons(card) {
		if len(b) > 6 && b[:6] == "a:ref:" {
			refunds = append(refunds, b)
		}
	}
	require.Len(t, refunds, 1, "only the not-refunded payment")
	require.LessOrEqual(t, len(refunds[0]), 64, "Telegram callback_data limit")
}

func TestAdminRefund(t *testing.T) {
	svc := withPayments(adminService())
	r, s := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press("a:user:7")))
	var ask string
	for _, b := range buttons(s.edits[0]) {
		if len(b) > 6 && b[:6] == "a:ref:" {
			ask = b
		}
	}

	require.NoError(t, r.Handle(ctx, press(ask)))
	require.Empty(t, s.refunds, "asks first")
	confirm := buttons(s.edits[1])[0]
	require.Contains(t, s.edits[1].Text, "Вернуть 150 ⭐")

	require.NoError(t, r.Handle(ctx, press(confirm)))
	charge := "stxLongTelegramChargeID-0123456789-abcdefghijklmnopqrstuvwxyz"
	require.Equal(
		t,
		[]RefundInput{
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
	}
	r, s := newRouter(svc)
	r.pause = 0
	s.fail[8] = true
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("a:bc")))
	require.Contains(t, s.sent[0].Text, "Напишите текст")

	require.NoError(t, r.Handle(ctx, startUpdate("Сервер переедет в субботу")))
	preview := s.sent[1]
	require.Contains(t, preview.Text, "2 пользователям")
	require.Contains(t, preview.Text, "Сервер переедет в субботу")
	require.Equal(t, "a:bcok", preview.Keyboard.InlineKeyboard[0][0].CallbackData)
	require.Equal(t, "a:cancel", preview.Keyboard.InlineKeyboard[0][1].CallbackData)

	require.NoError(t, r.Handle(ctx, press("a:bcok")))
	r.Wait()
	require.Contains(t, s.sent[2].Text, "началась", "the admin is told at once")
	require.Equal(t, int64(7), s.sent[3].ChatID)
	require.Equal(t, "Сервер переедет в субботу", s.sent[3].Text)
	report := s.sent[len(s.sent)-1]
	require.Equal(t, int64(42), report.ChatID)
	require.Contains(t, report.Text, "доставлено 1")
	require.Contains(t, report.Text, "не доставлено 1")

	sent := len(s.sent)
	require.NoError(t, r.Handle(ctx, press("a:bcok")))
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
	require.NoError(t, r.Handle(ctx, press("a:bcok")))
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
	require.NoError(t, r.Handle(ctx, press("a:bcok")))
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
	require.Contains(t, report.Text, "доставлено 1")
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

	require.NoError(t, r.Handle(ctx, press("a:bcok")))
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
	var ask string
	for _, b := range buttons(s.edits[0]) {
		if len(b) > 6 && b[:6] == "a:ref:" {
			ask = b
		}
	}
	confirm := "a:refok:" + ask[len("a:ref:"):]
	charge := "stxLongTelegramChargeID-0123456789-abcdefghijklmnopqrstuvwxyz"
	r.refunding[charge] = true // another press is refunding it right now

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
	require.NoError(t, r.Handle(ctx, press("a:bcok")))
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
	require.Equal(t, "a:cfgsok", ask.Keyboard.InlineKeyboard[0][0].CallbackData)
	require.Equal(t, "a:cancel", ask.Keyboard.InlineKeyboard[0][1].CallbackData)

	require.NoError(t, r.Handle(ctx, press("a:cfgsok")))
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

	r.configsRunning = true
	require.NoError(t, r.Handle(ctx, press("a:cfgsok")))
	r.Wait()
	require.Empty(t, s.files, "nothing sent while another run is going")
	require.Contains(t, s.sent[0].Text, "уже идёт")
}

func TestAdminMaintenanceNoticesUseTheBroadcastPreview(t *testing.T) {
	for _, tc := range []struct {
		button string
		want   string
	}{
		{
			button: "a:mnt",
			want:   "технические работы",
		},
		{
			button: "a:mntend",
			want:   "работы закончены",
		},
	} {
		svc := adminService()
		svc.recipients = []int64{
			7,
		}
		r, s := newRouter(svc)
		r.pause = 0
		ctx := context.Background()

		require.NoError(t, r.Handle(ctx, press(tc.button)))
		preview := s.sent[0]
		require.Contains(t, preview.Text, "1 пользователям")
		require.Contains(t, strings.ToLower(preview.Text), tc.want)
		require.Equal(t, "a:bcok", preview.Keyboard.InlineKeyboard[0][0].CallbackData, "the usual confirm")

		require.NoError(t, r.Handle(ctx, press("a:bcok")))
		r.Wait()
		require.Equal(t, int64(7), s.sent[2].ChatID)
		require.Contains(t, strings.ToLower(s.sent[2].Text), tc.want)
	}
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
