package bot

import (
	"context"
	"testing"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"
	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

func TestMyAccessOffersOneMoreKeyToPlainUsers(t *testing.T) {
	timed := &service.Peer{
		PublicKey: "PUB1=",
		Name:      "tg:bob",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	r, s := newRouter(
		&fakeService{
			role: service.RoleUser,
			access: []service.KeyInfo{
				{Peer: timed},
			},
		},
	)

	require.NoError(t, r.Handle(context.Background(), press("my")))
	require.Contains(t, callbacks(s.sent[0].Keyboard.InlineKeyboard), "buyk:new")
}

func TestMyAccessHasNoNewKeyForUnlimited(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			role: service.RoleUnlimited,
			access: []service.KeyInfo{
				{Peer: &service.Peer{PublicKey: "PUB1="}},
			},
		},
	)

	require.NoError(t, r.Handle(context.Background(), press("my")))
	require.NotContains(t, callbacks(s.sent[0].Keyboard.InlineKeyboard), "buyk:new")
}

func TestNewKeyTariffsSendANewKeyInvoice(t *testing.T) {
	svc := &fakeService{}
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press("buyk:new")))
	require.Equal(t, newKeyBuyText, s.sent[0].Text)
	require.Equal(t, "buy:30:new", s.sent[0].Keyboard.InlineKeyboard[0][0].CallbackData)

	require.NoError(t, r.Handle(context.Background(), press("buy:30:new")))
	require.Equal(t, service.BuyNewKey, svc.invoiceIn.PublicKey)
	require.Contains(t, s.invoices[0].Description, "новый ключ")
	require.LessOrEqual(t, len([]rune(s.invoices[0].Description)), 255, "Telegram description limit")
}

func TestUserKeyLimitExplained(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			invoiceErr: service.ErrKeyLimit,
		},
	)

	require.NoError(t, r.Handle(context.Background(), press("buy:30:new")), "expected: not logged")
	require.Equal(t, userKeyLimitText, s.sent[0].Text)
	require.Equal(t, userKeyLimitText, preCheckoutErrorText(service.ErrKeyLimit))
}

func callbacks(kb [][]tgbot.InlineKeyboardButton) []string {
	var got []string
	for _, row := range kb {
		for _, b := range row {
			got = append(got, b.CallbackData)
		}
	}
	return got
}
