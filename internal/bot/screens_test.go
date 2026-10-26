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
		"github.com/amnezia-vpn/", "", // the release link (not shown, only behind a button)
		"geoirb-vpn-bot", "", // systemd unit in an admin hint
		// what iOS itself shows (the iPhone guide)
		"наберите VPN", "",
		"действие про VPN", "",
		"«Настроить VPN»", "",
		"«VPN»", "",
		"значок VPN", "",
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
	require.Contains(t, importText, "DefaultVPN не умеет читать QR, поэтому используйте файл.")
}

// TestImportTextCheckNamesNoCountry: the check sends users to a foreign site
// (a Russian one goes direct and keeps showing Russia) and names no country.
func TestImportTextCheckNamesNoCountry(t *testing.T) {
	require.Contains(t, importText, "ipinfo.io")
	require.Contains(t, importText, "местоположение")
	require.NotContains(t, importText, "2ip.ru")
	require.NotContains(t, importText, "Нидерланд")
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
	require.NotContains(t, keyLimitText, n, "vip users have their own limit")
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

func TestVIPGreetingShowsTheirLimit(t *testing.T) {
	text := greeting(
		&service.User{
			Role:    service.RoleVIP,
			MaxKeys: 7,
		},
	)
	require.Contains(t, text, "VIP")
	require.Contains(t, text, "до 7 ключей")
}

func TestVIPCardShowsTheLimit(t *testing.T) {
	vip := userCardText(
		&service.User{
			ID:      1,
			Role:    service.RoleVIP,
			MaxKeys: 7,
		},
	)
	require.Contains(t, vip, "Лимит ключей: 7")
	plain := userCardText(
		&service.User{
			ID:   1,
			Role: service.RoleUser,
		},
	)
	require.NotContains(t, plain, "Лимит ключей")
}

// The release page lists many files: step 1 says which one to take.
func TestAppsTextSaysWhichFileToDownload(t *testing.T) {
	for _, file := range []string{
		"android11+_arm64-v8a.apk",
		"android9-10_arm64-v8a.apk",
		"windows_x64.exe",
		"macos_x64.pkg",
		"linux_x64.run",
		"Assets",
	} {
		require.Contains(t, appsText, file)
	}
	require.NotContains(t, appsText, "5.0.3.0", "no version: the text outlives the release")
	require.Less(t, len([]rune(appsText)), 4096, "one Telegram message")
}
