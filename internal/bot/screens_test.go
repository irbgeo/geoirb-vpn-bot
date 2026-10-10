package bot

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
	"github.com/stretchr/testify/require"
)

// TestScreensDoNotSayVPN: user-facing texts never call the service a VPN.
// Only names users must find in the Amnezia apps may keep the word.
func TestScreensDoNotSayVPN(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "screens.go", nil, 0)
	require.NoError(t, err)

	allowed := strings.NewReplacer(
		"AmneziaVPN", "",
		"DefaultVPN", "",
		"org.amnezia.vpn", "",
		"geoirb-vpn-bot", "", // systemd unit in an admin hint
	)
	ast.Inspect(file, func(n ast.Node) bool {
		_, ok := n.(*ast.ImportSpec)
		if ok {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text := strings.ToLower(allowed.Replace(lit.Value))
		require.NotContains(t, text, "vpn", lit.Value)
		require.NotContains(t, text, "впн", lit.Value)
		return true
	})
}

// TestImportTextDefaultVPNFileOnly: DefaultVPN has no QR import, so step 2
// sends its users to the .conf file.
func TestImportTextDefaultVPNFileOnly(t *testing.T) {
	require.Contains(t, importText, "DefaultVPN: только файл .conf")
}

func TestConfigFileName(t *testing.T) {
	cases := map[string]string{
		"Ноутбук":       "key_Noutbuk.conf",
		"Мой телефон 2": "key_Moy_telefon_2.conf",
		"Щука ёж":       "key_Shchuka_yozh.conf",
		"tg:geo #2":     "key_geo_2.conf",
		"":              "key_.conf",
	}
	for name, want := range cases {
		p := &service.Peer{Name: name}
		require.Equal(t, want, configFileName(p), name)
	}
}

// A charge whose purchase was refused is recorded without days: no text
// may call it "0 месяцев".
func TestRefusedChargeIsNotShownAsZeroMonths(t *testing.T) {
	refused := &service.Payment{
		ChargeID:   "c1",
		UserID:     7,
		Stars:      150,
		CreatedAt:  time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		RefundedAt: time.Date(2026, 9, 1, 0, 0, 1, 0, time.UTC),
	}
	ps := []*service.Payment{
		refused,
	}
	texts := []string{
		paymentsText(ps),
		unfinishedPaymentsText(ps),
		refundConfirmText(refused),
		refundedToUserText(refused),
	}
	for _, text := range texts {
		require.NotContains(t, text, "0 месяцев")
		require.Contains(t, text, "150 ⭐")
	}
	require.Contains(t, texts[0], "150 ⭐, оплата отклонена, ↩️ возвращено")
	require.Contains(t, texts[1], "оплата отклонена")
	require.Contains(t, texts[2], "отклонённую оплату")
	require.Equal(t, "↩️ Вам вернули 150 ⭐.", texts[3])
}

func TestTariffLabel(t *testing.T) {
	cases := map[int]string{
		1:   "1 день",
		3:   "3 дня",
		7:   "7 дней",
		11:  "11 дней",
		14:  "14 дней",
		21:  "21 день",
		22:  "22 дня",
		45:  "45 дней",
		30:  "1 месяц",
		90:  "3 месяца",
		180: "6 месяцев",
		365: "12 месяцев",
	}
	for days, want := range cases {
		require.Equal(t, want, tariffLabel(days))
	}
}

func TestKeyTextsPointToTheMenu(t *testing.T) {
	require.Contains(t, hasKeyText, "Мой доступ")
	require.NotContains(t, hasKeyText, "скоро")
	require.Contains(t, trialUsedText, "Купить")
	require.NotContains(t, trialUsedText, "скоро")
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
