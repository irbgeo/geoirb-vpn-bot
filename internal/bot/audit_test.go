package bot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// Tests for the fixes from the 2026-09-27 audit.

func TestPaidButDeliveryFailedStillTellsTheUser(t *testing.T) {
	svc := adminService()
	svc.admins = []*service.User{
		{
			ID: 1,
		},
	}
	svc.payRes = &service.PayResult{
		Peer: &service.Peer{
			PublicKey: "PUB1=",
			Name:      "tg:bob",
		},
		NewKey: true,
		Days:   30,
	}
	svc.configErr = errors.New("awg timeout")
	r, s := newRouter(svc)

	require.Error(t, r.Handle(context.Background(), paid()))
	require.Equal(t, int64(42), s.sent[0].ChatID)
	require.Contains(t, s.sent[0].Text, "Оплата получена")
	require.Contains(t, s.sent[0].Text, "Мой доступ")
	require.Contains(t, s.sent[1].Text, "awg timeout", "admins see why")
}

func TestAdminConfigOfImportedKeyExplains(t *testing.T) {
	svc := adminService()
	svc.configErr = service.ErrNoPrivateKey
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press("a:cfg:PUB1=")))
	require.Empty(t, s.files)
	require.Contains(t, s.sent[0].Text, "только на устройстве")
}

func TestExpiredKeyHasNoEnableButton(t *testing.T) {
	svc := adminService()
	p := svc.access[0].Peer
	p.Enabled = false
	p.ExpiresAt = time.Now().Add(-time.Hour)
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press("a:user:7")))
	b := buttons(s.edits[0])
	require.NotContains(t, b, "a:en:PUB1=", "enabling it would be undone within a minute")
	require.Contains(t, b, "a:ext:PUB1=")
}

func TestAdminErrorsAreExplained(t *testing.T) {
	svc := adminService()
	svc.enableErr = service.ErrExpired
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press("a:en:PUB1=")), "expected: not logged as an error")
	require.Contains(t, s.sent[0].Text, "30 дней", "tells the admin what to do")
	require.NotContains(t, s.sent[0].Text, "service:", "no raw internal error")
}

func TestPendingBroadcastExpiresAndCanBeCancelled(t *testing.T) {
	svc := adminService()
	svc.recipients = []int64{
		7,
	}
	r, s := newRouter(svc)
	ctx := context.Background()
	previews := func() int {
		n := 0
		for _, m := range s.sent {
			if m.Keyboard != nil && len(m.Keyboard.InlineKeyboard) > 0 && strings.HasPrefix(m.Keyboard.InlineKeyboard[0][0].CallbackData, "a:bcok:") {
				n++
			}
		}
		return n
	}

	r.dialogs.set(
		pendingInput{
			ChatID: 42,
			Kind:   pendingBroadcast,
			At:     time.Now().Add(-time.Hour),
		},
	)
	require.NoError(t, r.Handle(ctx, startUpdate("hello days later")))
	require.Zero(t, previews(), "an old prompt doesn't turn a later text into a broadcast")

	require.NoError(t, r.Handle(ctx, press("a:bc")))
	require.Contains(t, buttons(editMessage{Keyboard: s.sent[len(s.sent)-1].Keyboard}), "a:cancel", "the prompt can be cancelled")
	require.NoError(t, r.Handle(ctx, press("a:cancel")))
	require.NoError(t, r.Handle(ctx, startUpdate("text")))
	require.Zero(t, previews(), "cancelled")

	require.NoError(t, r.Handle(ctx, press("a:bc")))
	require.NoError(t, r.Handle(ctx, startUpdate("/start")))
	require.NoError(t, r.Handle(ctx, startUpdate("text")))
	require.Zero(t, previews(), "/start drops the prompt too")
}

func TestInvoiceFailureSaysInvoice(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			invoiceErr: errors.New("db down"),
		},
	)

	require.Error(t, r.Handle(context.Background(), press("buy:30")))
	require.Contains(t, s.sent[0].Text, "счёт")
}

func TestKeyTextsPointToTheMenu(t *testing.T) {
	require.Contains(t, hasKeyText, "Мой доступ")
	require.NotContains(t, hasKeyText, "скоро")
	require.Contains(t, trialUsedText, "Купить")
	require.NotContains(t, trialUsedText, "скоро")
}
