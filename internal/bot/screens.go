package bot

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
	"github.com/irbgeo/geoirb-vpn-bot/internal/sysload"
	"github.com/irbgeo/geoirb-vpn-bot/internal/tunnel"
)

// This file owns every user-facing text (Russian).

// Step 1 and 2 of getting a key.
const (
	appsText = "🔑 Шаг 1. Установите приложение для подключения\n\n" +
		"⭐ AmneziaVPN — рекомендуем: iPhone, iPad, Android, Windows, macOS, Linux.\n" +
		"• AmneziaWG — проще: iPhone, iPad, Android.\n" +
		"• DefaultVPN — iPhone, iPad (iOS 16+), тоже от Amnezia.\n" +
		"• WG Tunnel — Android, Windows, Linux (сайт wgtunnel.com).\n" +
		"• Роутер Keenetic — AWG Manager (ставится через Entware); другой роутер — напишите в /support.\n\n" +
		"Установите приложение и нажмите «Дальше»."
	importText = "🔑 Шаг 2. Добавьте ключ в приложение\n\n" +
		"AmneziaVPN: «+» (Добавить) → отсканируйте QR-код выше или выберите файл .conf → «Подключиться».\n" +
		"AmneziaWG: «+» → «Сканировать QR-код» или «Импорт из файла» → включите переключатель.\n" +
		"DefaultVPN: только файл .conf — QR-кода в нём нет.\n" +
		"Другие приложения: добавьте туннель из QR-кода или файла .conf.\n\n" +
		"Проверка: откройте 2ip.ru — должен быть виден IP сервера, а не ваш.\n" +
		"Файл и QR — это ваш личный ключ: не пересылайте их другим людям."
)

// Commands are the bot's commands for the Telegram menu (SetMyCommands).
func Commands() []tgbot.BotCommand {
	return []tgbot.BotCommand{
		{
			Command:     "menu",
			Description: "Меню: ключ, мой доступ, оплата",
		},
		{
			Command:     "support",
			Description: "Связаться с поддержкой",
		},
		{
			Command:     "terms",
			Description: "Условия использования и оплаты",
		},
		{
			Command:     "paysupport",
			Description: "Вопросы по оплате",
		},
	}
}

// ReconcileText describes DB/server differences for admins.
func ReconcileText(r *service.ReconcileReport) string {
	var b strings.Builder
	b.WriteString("⚠️ Сверка базы и сервера: есть расхождения.\n")
	missing := peersGroup{
		Title: "Включены в базе, но нет на сервере",
		Peers: r.MissingOnServer,
	}
	b.WriteString(peersSection(missing))
	disabled := peersGroup{
		Title: "Отключены в базе, но есть на сервере",
		Peers: r.DisabledButOnServer,
	}
	b.WriteString(peersSection(disabled))
	fmt.Fprintf(&b, "\nКлючей, созданных вручную: %d.\n", r.Manual)
	b.WriteString("Бот ничего не менял автоматически.")
	return b.String()
}

// Store links for step 1 (checked 2026-09-27).
const (
	urlAppStore   = "https://apps.apple.com/app/id1600529900"
	urlGooglePlay = "https://play.google.com/store/apps/details?id=org.amnezia.vpn"
	urlDownloads  = "https://amnezia.org/ru/downloads"
)

func appsKeyboard() *tgbot.InlineKeyboardMarkup {
	return tgbot.InlineKeyboard(
		tgbot.Row(
			tgbot.URLButton("📱 iPhone / iPad", urlAppStore),
			tgbot.URLButton("🤖 Android", urlGooglePlay),
		),
		tgbot.Row(tgbot.URLButton("💻 Компьютер и другие", urlDownloads)),
		tgbot.Row(tgbot.Button("➡️ Приложение установлено — дальше", cbIssueKey)),
		menuRow(),
	)
}

// The last step of getting a key, also a menu button (splitvideo.go). "VPN"
// is avoided in our own wording, as everywhere else; the app's menu names
// are quoted as they are, and so are the iOS names in the iPhone guide.
const (
	splitAskText = "📱 Приложения банков и Госуслуг\n\n" +
		"Некоторые приложения могут не работать, пока подключение включено. " +
		"На Android и Windows их можно пустить напрямую, мимо подключения. " +
		"На iPhone и iPad телефон может сам выключать подключение, когда вы открываете такое приложение.\n\n" +
		"Какое у вас устройство?"
	splitHowToText = "В приложении AmneziaVPN:\n" +
		"1. Отключите соединение: нажмите на круглую кнопку в центре экрана.\n" +
		"2. Под кнопкой есть строка «Раздельное туннелирование» — нажмите на неё.\n" +
		"3. Откройте «Раздельное туннелирование приложений» и включите его.\n" +
		"4. Добавьте в список все российские приложения: банки, Госуслуги, маркетплейсы, " +
		"такси, доставку и остальные. Они будут работать напрямую.\n" +
		"5. Вернитесь на главный экран и подключитесь снова.\n\n" +
		"Все шаги — в видео ниже."
	splitVideoCaption    = "Видео снято на Android. На Windows шаги те же."
	splitVideoFailedText = "Не получилось отправить видео. Нажмите кнопку ещё раз через минуту."
	// splitNoneText answers Mac and Linux, and the old button «🍏 iPhone,
	// iPad, Mac, Linux» that messages sent before the iPhone guide still carry.
	splitNoneText = "На Mac и Linux в приложении такой настройки нет. " +
		"Российские сайты и так открываются напрямую — об этом заботится сервер. " +
		"Если приложение банка не работает, выключите подключение на время.\n\n" +
		"Для iPhone и iPad есть другой способ: откройте /menu → «📱 Банки и Госуслуги» → «🍏 iPhone, iPad»."
	iphoneVideoCaption = "Видео: как настроить автоматизацию в «Командах»."
	// iphoneHowToText: iOS can't route one app around the tunnel, but a
	// Shortcuts automation can switch the tunnel off while the app is open.
	iphoneHowToText = "🍏 iPhone и iPad: банк и Госуслуги без ручного выключения\n\n" +
		"На iPhone нельзя исключить отдельное приложение из подключения. Зато телефон умеет сам выключать подключение, когда вы открываете банк. Настраивается один раз, занимает 3 минуты.\n\n" +
		"Нужно приложение «Команды» (оно уже есть на iPhone; если удаляли — поставьте из App Store).\n\n" +
		"НАСТРОЙКА\n" +
		"1. Откройте «Команды» → внизу вкладка «Автоматизация».\n" +
		"2. Нажмите «+» (или «Новая автоматизация»).\n" +
		"3. Выберите «Приложение».\n" +
		"4. Нажмите «Выбрать» и отметьте нужные приложения: банк, Госуслуги и другие. Можно несколько сразу. Нажмите «Готово».\n" +
		"5. Оставьте галочку только у «Открыто».\n" +
		"6. Выберите «Немедленный запуск». Нажмите «Далее».\n" +
		"7. Нажмите «Новая пустая автоматизация».\n" +
		"8. В поиске действий наберите VPN и выберите действие про VPN («Настроить VPN»).\n" +
		"9. В действии нажмите на слово «Подключиться» и замените на «Отключиться».\n" +
		"10. Нажмите на слово «VPN» в действии и выберите наше подключение из списка.\n" +
		"11. Нажмите «Готово».\n\n" +
		"ПРОВЕРКА\n" +
		"Откройте банк: значок VPN вверху экрана должен пропасть через 1–2 секунды.\n\n" +
		"ЕСЛИ НЕ РАБОТАЕТ\n" +
		"• Подключение сразу включается обратно. В приложении AmneziaVPN или AmneziaWG выключите автоподключение («По запросу» / On-Demand) в настройках этого подключения.\n" +
		"• Приложение успело открыться раньше, чем выключилось подключение, и показало ошибку. Закройте его полностью и откройте снова.\n" +
		"• Нашего подключения нет в списке в пункте 10. Сначала подключитесь один раз из приложения, потом вернитесь в «Команды».\n" +
		"• Телефон каждый раз спрашивает разрешение. Откройте автоматизацию и выберите «Немедленный запуск» (на старых iOS — выключите «Спрашивать до запуска»).\n\n" +
		"Вопросы — в /support."
)

func splitAskKeyboard() *tgbot.InlineKeyboardMarkup {
	return tgbot.InlineKeyboard(
		tgbot.Row(
			tgbot.Button("🤖 Android", cbSplitVideo),
			tgbot.Button("💻 Windows", cbSplitVideo),
		),
		tgbot.Row(tgbot.Button("🍏 iPhone, iPad", cbSplitIPhone)),
		tgbot.Row(tgbot.Button("🖥 Mac, Linux", cbSplitNone)),
		menuRow(),
	)
}

// msk: dates are shown in Moscow time (fixed zone, no tzdata needed).
var msk = time.FixedZone("MSK", 3*60*60)

const (
	hasKeyText             = "У вас уже есть ключ — он в «📋 Мой доступ»: там можно получить конфиг ещё раз и продлить срок."
	trialUsedText          = "Пробный период уже использован. Чтобы подключиться, нажмите «💳 Купить / продлить» в /menu."
	internalErrorText      = "Не получилось создать ключ. Попробуйте позже — админ уже знает."
	invoiceFailedText      = "Не получилось выставить счёт. Попробуйте позже или напишите в /support."
	configFailedText       = "Не получилось собрать конфиг. Попробуйте позже или напишите в /support."
	paidDeliveryFailedText = "✅ Оплата получена, но ключ не удалось отправить сразу. Он уже в «📋 Мой доступ» — нажмите «📄 Конфиг». Если не получится, напишите в /paysupport."
	paidUnderReviewText    = "✅ Оплата получена. Администратор проверит её и продлит ключ в ближайшее время."
	qrTooLongText          = "Конфиг не поместился в QR-код — используйте файл выше."
	qrCaption              = "QR-код: отсканируйте его в приложении AmneziaVPN."
	noKeysText             = "У вас пока нет ключей."
	keyNotFoundText        = "Ключ не найден. Откройте «Мой доступ» ещё раз."
	keyUnreadableText      = "Этот ключ нельзя перевыпустить: его данные повреждены. Напишите в поддержку."
	blockedKeyText         = "Этот ключ отключил администратор, продлить его нельзя. Напишите в /support."
	unreadableKeyBuyText   = "Этот ключ нельзя продлить: его данные повреждены. Напишите в /support."
	blockedKeyDeleteText   = "Этот ключ отключил администратор, удалить его нельзя. Напишите в /support."
	noPrivateKeyText       = "Этот ключ создан в приложении Amnezia: его конфиг есть только на устройстве, где ключ создан. Нужен файл — получите новый ключ."
	buyText                = "💳 Выберите срок. Оплата — Telegram Stars. Если ключ уже есть, срок прибавится к нему."
	notForSaleText         = "Вам платить не нужно: ваш доступ бессрочный."
	noTariffText           = "Этот тариф больше недоступен. Откройте «Купить / продлить» ещё раз."
	staleInvoiceText       = "Счёт устарел. Откройте «Купить / продлить» и оплатите новый — деньги не списаны."
	refundedText           = "Не получилось применить оплату, звёзды возвращены. Попробуйте позже."
	refundFailedText       = "Не получилось применить оплату. Мы вернём звёзды вручную — напишите в /paysupport."
)

func greeting(u *service.User) string {
	switch u.Role {
	case service.RoleAdmin:
		return "Привет! Вы админ. Нажмите кнопку, чтобы получить свой ключ (сколько угодно, без срока)."
	case service.RoleUnlimited:
		return fmt.Sprintf("Привет! У вас безлимитный доступ: до %d ключей без срока. Нажмите кнопку, чтобы получить ключ.", service.MaxUnlimitedKeys)
	default:
		return "Привет! Нажмите кнопку — получите ключ и бесплатный пробный период."
	}
}

// mainKeyboard is the /start and /menu menu: plain users can buy, admins also get
// the admin buttons.
func mainKeyboard(v menuView) *tgbot.InlineKeyboardMarkup {
	role := v.Role
	rows := [][]tgbot.InlineKeyboardButton{
		tgbot.Row(tgbot.Button("🔑 Получить ключ", cbCreateKey)),
		tgbot.Row(tgbot.Button("📋 Мой доступ", cbMyAccess)),
	}
	if role == service.RoleUser {
		rows = append(rows, tgbot.Row(tgbot.Button("💳 Купить / продлить", cbBuy)))
	}
	if v.SplitVideo {
		rows = append(rows, tgbot.Row(tgbot.Button("📱 Банки и Госуслуги", cbSplitAsk)))
	}
	rows = append(
		rows,
		tgbot.Row(tgbot.Button("💬 Поддержка", cbSupport), tgbot.Button("📄 Условия", cbTerms)),
		tgbot.Row(tgbot.Button("💡 Отзывы и предложения", cbFeedback)),
	)
	if role == service.RoleAdmin {
		rows = append(
			rows,
			tgbot.Row(tgbot.Button("👥 Пользователи", cbAdminUsers+"0")),
			tgbot.Row(tgbot.Button("📊 Статистика", cbAdminStats)),
			tgbot.Row(tgbot.Button("💡 Отзывы", cbAdminFb+"0")),
			tgbot.Row(tgbot.Button("📣 Рассылка", cbAdminBc)),
			tgbot.Row(tgbot.Button("🔄 Обновить конфиги", cbAdminCfgs)),
			tgbot.Row(tgbot.Button(maintButtonText(v.Maintenance), cbAdminMnt)),
		)
	}
	return tgbot.InlineKeyboard(rows...)
}

// menuKeyboard is a keyboard with only the "◀️ Меню" button.
func menuKeyboard() *tgbot.InlineKeyboardMarkup {
	return tgbot.InlineKeyboard(menuRow())
}

func createKeyKeyboard() *tgbot.InlineKeyboardMarkup {
	return tgbot.InlineKeyboard(
		tgbot.Row(tgbot.Button("🔑 Получить ключ", cbCreateKey)),
		menuRow(),
	)
}

// accessKeyboard has a "config again" button per key, and "extend" for
// users who buy.
func accessKeyboard(v accessView) *tgbot.InlineKeyboardMarkup {
	rows := make([][]tgbot.InlineKeyboardButton, 0, len(v.Keys))
	for _, k := range v.Keys {
		row := tgbot.Row(tgbot.Button("📄 Конфиг: "+keyLabel(k.Peer), cbConfig+k.Peer.PublicKey))
		if v.CanBuy && !k.Peer.ExpiresAt.IsZero() { // a forever key has nothing to extend
			row = append(row, tgbot.Button("💳 Продлить", cbBuyKey+k.Peer.PublicKey))
		}
		rows = append(
			rows,
			row,
			tgbot.Row(
				tgbot.Button("🔄 Перевыпустить", cbReissueAsk+k.Peer.PublicKey),
				tgbot.Button("🗑 Удалить", cbDeleteAsk+k.Peer.PublicKey),
			),
		)
	}
	rows = append(rows, menuRow())
	return tgbot.InlineKeyboard(rows...)
}

// tariffsKeyboard: one button per tariff, e.g. "1 месяц — 150 ⭐".
func tariffsKeyboard(v tariffsView) *tgbot.InlineKeyboardMarkup {
	rows := make([][]tgbot.InlineKeyboardButton, 0, len(v.Tariffs))
	for _, t := range v.Tariffs {
		data := cbTariff + strconv.Itoa(t.Days)
		if v.PublicKey != "" {
			data += ":" + v.PublicKey
		}
		rows = append(rows, tgbot.Row(tgbot.Button(fmt.Sprintf("%s — %d ⭐", tariffLabel(t.Days), t.Stars), data)))
	}
	rows = append(rows, menuRow())
	return tgbot.InlineKeyboard(rows...)
}

func invoiceTitle(days int) string {
	return "Доступ на " + tariffLabel(days)
}

func invoiceDescription(days int) string {
	return "Доступ к сервису на " + tariffLabel(days) + ". Если ключ уже есть, срок прибавится к нему. " +
		"Оплачивая, вы принимаете условия: /terms"
}

func extendedText(p *service.Peer) string {
	return "✅ Оплата получена. Ключ " + p.Name + " продлён " + keyUntil(p) + "."
}

func paymentAlertText(a paymentAlert) string {
	what := "продление"
	if a.Result.NewKey {
		what = "новый ключ"
	}
	user := service.User{
		ID:       a.Payer.ID,
		Username: a.Payer.Username,
	}
	text := fmt.Sprintf(
		"💰 Оплата: %s (id %d) — %d ⭐, %s, %s.",
		userLabel(&user),
		a.Payer.ID,
		a.Stars,
		tariffLabel(a.Result.Days),
		what,
	)
	if a.DeliveryErr != nil {
		text += "\n⚠️ Ключ или сообщение не дошли до пользователя: " + a.DeliveryErr.Error()
	}
	return text
}

func refundAlertText(a refundAlert) string {
	if a.RefundErr != nil {
		return fmt.Sprintf(
			"🔴 Оплата %s от id %d не применена (%v) и НЕ возвращена (%v). Верните вручную.",
			a.ChargeID,
			a.UserID,
			a.Cause,
			a.RefundErr,
		)
	}
	if a.RecordErr != nil {
		return fmt.Sprintf(
			"⚠️ Оплата %s от id %d не применена (%v), звёзды возвращены, но возврат не записан в базе (%v). "+
				"В карточке пользователя останется кнопка возврата: нажмите её, чтобы записать — второй раз Telegram не вернёт.",
			a.ChargeID,
			a.UserID,
			a.Cause,
			a.RecordErr,
		)
	}
	return fmt.Sprintf("⚠️ Оплата %s от id %d не применена (%v), звёзды возвращены.", a.ChargeID, a.UserID, a.Cause)
}

// paymentReviewText tells admins that a payment was not applied: its record
// was already there, unfinished, and the days may have been added.
func paymentReviewText(in paymentReviewInput) string {
	return fmt.Sprintf(
		"🔴 Оплата %s от id %d (%d ⭐) пришла, но её запись уже была не завершена. "+
			"Ничего не применено: проверьте срок ключа и примените вручную или верните звёзды.",
		in.ChargeID,
		in.UserID,
		in.Stars,
	)
}

// unfinishedPaymentsText lists payments that were neither applied nor
// refunded (the bot stopped in the middle).
func unfinishedPaymentsText(ps []*service.Payment) string {
	var b strings.Builder
	b.WriteString("🔴 Оплаты не отмечены как применённые и не возвращены (бот остановился в процессе). " +
		"Дни могли уже добавиться: сначала проверьте срок ключа в карточке, потом верните звёзды или продлите вручную:\n")
	for _, p := range ps {
		key := "новый ключ"
		if p.PeerKey != "" {
			key = "ключ " + shortKey(p.PeerKey)
		}
		fmt.Fprintf(&b, "• id %d — %d ⭐, %s, %s, %s\n", p.UserID, p.Stars, paidFor(p), key, mskTime(p.CreatedAt))
	}
	return b.String()
}

// shortKey is the start of a public key, enough to find it in the card.
func shortKey(publicKey string) string {
	return publicKey[:min(8, len(publicKey))] + "…"
}

func refundNotRecordedText(p *service.Payment) string {
	return fmt.Sprintf(
		"⚠️ Звёзды (%d ⭐) пользователю id %d возвращены, но возврат не записан в базе: кнопка возврата останется. Второй раз Telegram не вернёт.",
		p.Stars,
		p.UserID,
	)
}

func madeForeverText(p *service.Peer) string {
	return "✅ Ваш доступ теперь бессрочный: ключ " + p.Name + " работает без срока, продлевать не нужно."
}

func expiredText(p *service.Peer) string {
	return "⛔️ Срок ключа " + p.Name + " закончился, ключ отключён. Продлите — заработает тот же ключ, настраивать заново не нужно."
}

func remind3dText(p *service.Peer) string {
	return "⏳ Ключ " + p.Name + " действует меньше 3 дней: " + keyUntil(p) + ". Продлите заранее, чтобы доступ не отключился."
}

func remind1dText(p *service.Peer) string {
	return "⏰ Ключ " + p.Name + " отключится меньше чем через сутки: " + keyUntil(p) + "."
}

func subnetAlertText(m *service.Maintenance) string {
	return fmt.Sprintf(
		"⚠️ Подсеть почти заполнена: занято %d из %d адресов. Новые ключи скоро некуда будет выдавать.",
		m.SubnetUsed,
		m.SubnetTotal,
	)
}

func onlineDropText(d onlineDrop) string {
	return fmt.Sprintf(
		"⚠️ Клиенты отключаются: сейчас онлайн %d, а за последний час было до %d.\n\n"+
			"Похоже на блокировку IP сервера в России: открытые соединения живут, новые не проходят. "+
			"Проверьте из России напрямую, без туннеля: ping до сервера и подключение своим ключом.",
		d.Online,
		d.Peak,
	)
}

func loadAlertText(a sysload.Alert) string {
	name := loadMetricNames()[a.Metric]
	if a.Recovered {
		return fmt.Sprintf("✅ Сервер: %s снова в норме — %d%%.", name, a.Percent)
	}
	text := fmt.Sprintf("⚠️ Сервер: %s — %d%% (предел %d%%).", name, a.Percent, a.Limit)
	switch a.Metric {
	case sysload.Conntrack:
		text += " Когда таблица заполнится, у пользователей перестанут открываться сайты."
	case sysload.CPU:
		text += " Уже 5 минут подряд: подключение может тормозить, нужна машина мощнее."
	case sysload.Memory:
		text += " Может не хватить памяти для туннеля, базы и бота."
	case sysload.Disk:
		text += " Могут перестать работать бэкапы и база."
	}
	return text
}

// loadMetricNames name the server limits for admins.
func loadMetricNames() map[sysload.Metric]string {
	return map[sysload.Metric]string{
		sysload.Conntrack: "таблица соединений",
		sysload.Memory:    "память",
		sysload.Disk:      "диск",
		sysload.CPU:       "процессор",
	}
}

func extendKeyboard(p *service.Peer) *tgbot.InlineKeyboardMarkup {
	return tgbot.InlineKeyboard(
		tgbot.Row(tgbot.Button("💳 Продлить", cbBuyKey+p.PublicKey)),
	)
}

const (
	askBroadcastText     = "✍️ Напишите текст рассылки одним сообщением. Его получат все, у кого есть включённый ключ."
	cancelledText        = "Отменено."
	needTextText         = "Нужен текст — пришлите его одним сообщением."
	broadcastStartedText = "📣 Рассылка началась. Пришлю отчёт, когда закончу."
)

func broadcastPreviewText(v broadcastView) string {
	return fmt.Sprintf("📣 Отправить это %d пользователям?\n\n%s", v.Recipients, v.Text)
}

// sendCancelKeyboard is a preview's keyboard: its "send" button and
// "cancel". Both carry the preview's token, so they send or cancel only
// what they sit under.
func sendCancelKeyboard(b previewButtons) *tgbot.InlineKeyboardMarkup {
	return tgbot.InlineKeyboard(
		tgbot.Row(
			tgbot.Button(b.Label, b.SendData+":"+b.Token),
			tgbot.Button("Отмена", cbAdminCanc+":"+b.Token),
		),
	)
}

const (
	statsUnavailableText  = "Данные о подключениях временно недоступны."
	staleButtonText       = "Эта кнопка устарела — начните заново."
	previewExpiredText    = "Этот предпросмотр устарел — ничего не отправлено. Откройте /menu и начните заново."
	repeatedPressText     = "Это действие только что выполнено — повторное нажатие пропущено. Нужно ещё раз — нажмите через 10 секунд."
	oldPreviewText        = "Эта кнопка от старого предпросмотра — ничего не отправлено. Нажмите «Отправить» под последним."
	oldCancelText         = "Эта «Отмена» от старого сообщения — ничего не отменено. Нажмите «Отмена» под последним."
	massSendBusyText      = "📣 Сейчас уже идёт рассылка — дождитесь её отчёта и нажмите «Отправить» ещё раз."
	keyDeliveryFailedText = "🔑 Ключ создан, но отправить его сразу не получилось. Он в «📋 Мой доступ» — нажмите «📄 Конфиг»."
)

// maintAlreadyText: the preview's change is already made (by another
// admin or an earlier press), so nothing is sent.
func maintAlreadyText(on bool) string {
	if on {
		return "🛠 Техработы уже включены — сообщение не отправлено."
	}
	return "✅ Техработы уже закончены — сообщение не отправлено."
}

const (
	maintenanceText = "🛠 На сервере идут технические работы. " +
		"Подключение может ненадолго отключаться или работать медленнее — это нормально, ничего делать не нужно. " +
		"Напишем, когда закончим."
	maintenanceEndText = "✅ Технические работы закончены, всё работает как обычно. " +
		"Если не подключается — выключите и включите подключение в приложении, а если не поможет — напишите в /support."
)

const (
	configsStartedText = "🔄 Рассылаю просьбу обновить конфиг. Пришлю отчёт, когда закончу."
	configsNoticeText  = "🔄 Настройки сервера изменились — обновите ключ в приложении.\n\n" +
		"1. Нажмите «📋 Мой доступ» (кнопка ниже или в /menu).\n" +
		"2. У нужного ключа нажмите «📄 Конфиг» — придут новый файл и QR-код.\n" +
		"3. В приложении удалите старое подключение и добавьте новое из файла или QR-кода.\n\n" +
		"Ключ и срок остаются прежними. Если ключей несколько — повторите для каждого."
)

func configsAskText(recipients int) string {
	return fmt.Sprintf(
		"🔄 Попросить %d пользователям (всем, у кого есть включённый ключ) обновить конфиг? "+
			"Файлы не рассылаются: каждый получит их сам в «📋 Мой доступ».\n\n"+
			"Конфиг собирается из текущих настроек сервера и ENDPOINT_HOST. "+
			"Если сменился IP и в ENDPOINT_HOST указан IP, сначала поменяйте его в .env и сделайте make deploy.\n\n"+
			"Пользователи получат (с кнопкой «📋 Мой доступ»):\n\n%s",
		recipients,
		configsNoticeText,
	)
}

const (
	askFeedbackText = "💡 Напишите отзыв или предложение одним сообщением — что нравится, что мешает, чего не хватает. " +
		"Мы читаем всё. Передумали — нажмите «◀️ Меню»."
	needFeedbackTextText = "Нужен текст — напишите отзыв одним сообщением."
	feedbackThanksText   = "🙏 Спасибо! Сохранили ваш отзыв."
	feedbackFailedText   = "Не получилось сохранить отзыв. Попробуйте позже."
	feedbackLimitText    = "Слишком много отзывов за час. Напишите позже — мы всё прочитаем."
)

var badFeedbackText = fmt.Sprintf(
	"Слишком длинно: нужно до %d символов. Сократите и отправьте ещё раз.",
	service.MaxFeedbackLen,
)

var (
	keyLimitText   = fmt.Sprintf("Больше ключей создать нельзя: максимум — %d.", service.MaxUnlimitedKeys)
	askKeyNameText = "✍️ Как назвать ключ? Напишите, например, «iPhone» или «Ноутбук» — " +
		"так будет проще отличать ключи. Или нажмите «Пропустить»."
	badKeyNameText = fmt.Sprintf(
		"Имя не подходит: нужна одна строка до %d символов. Напишите другое или нажмите «Пропустить».",
		service.MaxKeyNameLen,
	)
)

func skipKeyNameKeyboard() *tgbot.InlineKeyboardMarkup {
	return tgbot.InlineKeyboard(
		tgbot.Row(tgbot.Button("⏭ Пропустить", cbKeyNoName)),
	)
}

const (
	reissuedText = "🔄 Готово: выше новый файл и QR-код. Старый ключ больше не работает — " +
		"удалите старое подключение в приложении и добавьте новое. Если ключ стоял на нескольких устройствах, обновите на каждом."
	keyDeletedText   = "🗑 Ключ удалён. Удалите подключение и в приложении — оно больше не работает."
	ownKeyFailedText = "Не получилось. Попробуйте позже или напишите в /support."
	tooOftenText     = "Слишком часто. Подождите минуту и нажмите ещё раз."
)

func reissueAskText(p *service.Peer) string {
	return "🔄 Перевыпустить ключ «" + keyLabel(p) + "»?\n\n" +
		"Вы получите новый файл и QR-код, а старый ключ сразу перестанет работать — на всех устройствах, где он стоит. " +
		"Срок и всё остальное останутся прежними. Подходит, если потеряли телефон или файл попал к кому-то ещё."
}

func deleteAskText(p *service.Peer) string {
	lost := "Ключ удалится навсегда."
	if !p.ExpiresAt.IsZero() {
		lost = "Оставшийся срок (" + keyUntil(p) + ") пропадёт, звёзды не возвращаются."
	}
	return "🗑 Удалить ключ «" + keyLabel(p) + "»?\n\n" + lost + " Отменить удаление нельзя."
}

// confirmKeyboard: "yes" (data) and "◀️ Меню" to cancel.
func confirmKeyboard(data string) *tgbot.InlineKeyboardMarkup {
	return tgbot.InlineKeyboard(
		tgbot.Row(tgbot.Button("✅ Да", data)),
		menuRow(),
	)
}

func maintButtonText(on bool) string {
	if on {
		return "✅ Закончить техработы"
	}
	return "🛠 Техработы"
}

func myAccessKeyboard() *tgbot.InlineKeyboardMarkup {
	return tgbot.InlineKeyboard(
		tgbot.Row(tgbot.Button("📋 Мой доступ", cbMyAccess)),
	)
}

func configsReportText(r broadcastResult) string {
	return fmt.Sprintf(
		"🔄 Отчёт о просьбе обновить конфиг: получили %d, не доставлено %d (заблокировали бота или удалили чат).%s",
		r.Sent,
		r.Failed,
		stoppedNote(r),
	)
}

func broadcastReportText(r broadcastResult) string {
	return fmt.Sprintf(
		"📣 Отчёт о рассылке: доставлено %d, не доставлено %d (заблокировали бота или удалили чат).%s",
		r.Sent,
		r.Failed,
		stoppedNote(r),
	)
}

// stoppedNote ends the report of a mass send that a shutdown cut short: it
// is not done, and so many users were never tried.
func stoppedNote(r broadcastResult) string {
	if r.Skipped == 0 {
		return ""
	}
	return fmt.Sprintf("\n⚠️ Отправка остановлена, потому что бот выключался: не отправлено %d. Отправьте ещё раз, если нужно.", r.Skipped)
}

// statsText is the admin overview. Traffic counters restart when the VPN
// server restarts.
func statsText(st *service.Stats) string {
	var b strings.Builder
	b.WriteString("📊 Статистика\n\n")
	fmt.Fprintf(&b, "Пользователей: %d\n", st.Users)
	fmt.Fprintf(&b, "Ключи: активных %d, отключённых %d, истекают за 7 дней: %d\n", st.Active, st.Disabled, st.Expiring7d)
	fmt.Fprintf(&b, "В сети сейчас: %d\n", st.Online)
	fmt.Fprintf(&b, "Подсеть: занято %d из %d\n", st.SubnetUsed, st.SubnetTotal)
	fmt.Fprintf(&b, "Выручка за 30 дней: %d ⭐ (%d оплат)\n", st.Revenue30d, st.Payments30d)
	if len(st.TopTraffic) > 0 {
		b.WriteString("\nТрафик (с последнего перезапуска туннеля):\n")
		for _, k := range st.TopTraffic {
			fmt.Fprintf(&b, "• %s (%s) — %s\n", k.Peer.Name, k.Peer.IP, humanBytes(k.Sent+k.Received))
		}
	}
	return b.String()
}

// accessText lists the user's own keys.
func accessText(keys []service.KeyInfo) string {
	return "📋 Ваш доступ\n" + keysText(keys)
}

// keysText describes each key: status, end date, last connection,
// traffic. Traffic counters restart when the VPN server restarts.
func keysText(keys []service.KeyInfo) string {
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "\n🔑 %s — %s\n", k.Peer.Name, k.Peer.IP)
		fmt.Fprintf(&b, "Статус: %s\n", keyStatus(k))
		fmt.Fprintf(&b, "Срок: %s\n", keyUntil(k.Peer))
		last := "никогда"
		if !k.LastHandshake.IsZero() {
			last = mskTime(k.LastHandshake)
		}
		fmt.Fprintf(&b, "Последнее подключение: %s\n", last)
		fmt.Fprintf(&b, "Трафик: ↓ %s скачано, ↑ %s отправлено\n", humanBytes(k.Received), humanBytes(k.Sent))
	}
	if len(keys) > 0 && keys[0].StatsUnavailable {
		b.WriteString("\n" + statsUnavailableText + "\n")
	}
	return b.String()
}

func keyStatus(k service.KeyInfo) string {
	switch {
	case !k.Peer.Enabled:
		return "⛔️ отключён"
	case k.Online:
		return "🟢 в сети"
	}
	return "⚪️ не в сети"
}

// humanBytes: 1536 → "1.5 КБ".
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d Б", n)
	}
	units := []string{
		"КБ",
		"МБ",
		"ГБ",
		"ТБ",
		"ПБ",
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < len(units)-1; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), units[exp])
}

func keyCaption(p *service.Peer) string {
	until := "Ключ бессрочный."
	if !p.ExpiresAt.IsZero() {
		until = "Ключ действует " + keyUntil(p) + "."
	}
	return "🔑 " + keyLabel(p) + "\n" + until + "\nИмпортируйте файл в приложение AmneziaVPN или AmneziaWG."
}

func keyUntil(p *service.Peer) string {
	if p.ExpiresAt.IsZero() {
		return "бессрочно"
	}
	return "до " + mskTime(p.ExpiresAt)
}

// cyrillicToLatin transliterates Russian letters so file names stay readable.
var cyrillicToLatin = strings.NewReplacer(
	"а", "a", "б", "b", "в", "v", "г", "g", "д", "d", "е", "e", "ё", "yo", "ж", "zh",
	"з", "z", "и", "i", "й", "y", "к", "k", "л", "l", "м", "m", "н", "n", "о", "o",
	"п", "p", "р", "r", "с", "s", "т", "t", "у", "u", "ф", "f", "х", "kh", "ц", "ts",
	"ч", "ch", "ш", "sh", "щ", "shch", "ъ", "", "ы", "y", "ь", "", "э", "e", "ю", "yu", "я", "ya",
	"А", "A", "Б", "B", "В", "V", "Г", "G", "Д", "D", "Е", "E", "Ё", "Yo", "Ж", "Zh",
	"З", "Z", "И", "I", "Й", "Y", "К", "K", "Л", "L", "М", "M", "Н", "N", "О", "O",
	"П", "P", "Р", "R", "С", "S", "Т", "T", "У", "U", "Ф", "F", "Х", "Kh", "Ц", "Ts",
	"Ч", "Ch", "Ш", "Sh", "Щ", "Shch", "Ъ", "", "Ы", "Y", "Ь", "", "Э", "E", "Ю", "Yu", "Я", "Ya",
)

// configFileName turns "tg:bob #2" into "key_bob_2.conf": only ASCII
// letters, digits and '-' survive, every other run becomes one '_'.
func configFileName(p *service.Peer) string {
	keep := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-'
	}
	name := cyrillicToLatin.Replace(strings.TrimPrefix(p.Name, "tg:"))
	parts := strings.FieldsFunc(name, func(r rune) bool { return !keep(r) })
	return "key_" + strings.Join(parts, "_") + ".conf"
}

func peersSection(g peersGroup) string {
	if len(g.Peers) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s:\n", g.Title)
	for _, p := range g.Peers {
		fmt.Fprintf(&b, "• %s (%s)\n", p.Name, p.IP)
	}
	return b.String()
}

func usersText(v usersView) string {
	return fmt.Sprintf("👥 Пользователи: всего %d, страница %d/%d", v.Total, v.Page+1, pages(v.Total))
}

// usersKeyboard: one button per user, then ◀️ / ▶️ when there are more pages.
func usersKeyboard(v usersView) *tgbot.InlineKeyboardMarkup {
	rows := make([][]tgbot.InlineKeyboardButton, 0, len(v.Users)+1)
	for _, u := range v.Users {
		label := fmt.Sprintf("%s · %s", userLabel(u), u.Role)
		rows = append(rows, tgbot.Row(tgbot.Button(label, cbAdminUser+strconv.FormatInt(u.ID, 10))))
	}
	navView := navView{
		Prefix: cbAdminUsers,
		Page:   v.Page,
		Total:  v.Total,
	}
	nav := navRow(navView)
	if nav != nil {
		rows = append(rows, nav)
	}
	rows = append(rows, menuRow())
	return tgbot.InlineKeyboard(rows...)
}

// feedbackListCut: how much of one review the list shows, so a page of
// adminPageSize fits one Telegram message (4096).
const feedbackListCut = 300

func feedbackListText(v feedbackView) string {
	if v.Total == 0 {
		return "💡 Отзывов пока нет."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "💡 Отзывы: всего %d, страница %d/%d\n", v.Total, v.Page+1, pages(v.Total))
	for _, f := range v.List {
		fmt.Fprintf(&b, "\n• %s, %s:\n%s\n", mskTime(f.CreatedAt), feedbackAuthor(f), cut(f.Text))
	}
	return b.String()
}

func feedbackKeyboard(v feedbackView) *tgbot.InlineKeyboardMarkup {
	var rows [][]tgbot.InlineKeyboardButton
	navView := navView{
		Prefix: cbAdminFb,
		Page:   v.Page,
		Total:  v.Total,
	}
	nav := navRow(navView)
	if nav != nil {
		rows = append(rows, nav)
	}
	rows = append(rows, menuRow())
	return tgbot.InlineKeyboard(rows...)
}

// navRow is "◀️ ▶️" for a paged list, or nil when there is one page.
func navRow(v navView) []tgbot.InlineKeyboardButton {
	var nav []tgbot.InlineKeyboardButton
	if v.Page > 0 {
		nav = append(nav, tgbot.Button("◀️", v.Prefix+strconv.FormatInt(v.Page-1, 10)))
	}
	if v.Page+1 < pages(v.Total) {
		nav = append(nav, tgbot.Button("▶️", v.Prefix+strconv.FormatInt(v.Page+1, 10)))
	}
	return nav
}

// menuRow is the "◀️ Меню" button: it turns its message back into the
// main menu (backToMenu).
func menuRow() []tgbot.InlineKeyboardButton {
	return tgbot.Row(tgbot.Button("◀️ Меню", cbMenu))
}

// feedbackAlertText tells admins about a new review, in full.
func feedbackAlertText(f *service.Feedback) string {
	return "💡 Новый отзыв от " + feedbackAuthor(f) + ":\n\n" + f.Text
}

// feedbackAuthor is "@username (id 42)", or "id 42" without a username.
func feedbackAuthor(f *service.Feedback) string {
	if f.Username == "" {
		return "id " + strconv.FormatInt(f.UserID, 10)
	}
	return "@" + f.Username + " (id " + strconv.FormatInt(f.UserID, 10) + ")"
}

// cut shortens a text to feedbackListCut letters, marking the cut with "…".
func cut(s string) string {
	r := []rune(s)
	if len(r) <= feedbackListCut {
		return s
	}
	return string(r[:feedbackListCut]) + "…"
}

func pages(total int64) int64 {
	return max(1, (total+adminPageSize-1)/adminPageSize)
}

func userCardText(u *service.User) string {
	trial := "не использован"
	if u.TrialUsed {
		trial = "использован"
	}
	return fmt.Sprintf(
		"👤 %s (id %d)\nРоль: %s\nКлючей: %d\nПробный период: %s\nВ боте с: %s\n",
		userLabel(u),
		u.ID,
		u.Role,
		u.KeysCount,
		trial,
		mskTime(u.CreatedAt),
	)
}

// userLabel is "@username", or "id 123" for users without one.
func userLabel(u *service.User) string {
	if u.Username != "" {
		return "@" + u.Username
	}
	return "id " + strconv.FormatInt(u.ID, 10)
}

// userCardKeyboard: per key [disable|enable] [+30 days], [config] [delete];
// then "issue a key" and back to the list.
func userCardKeyboard(v cardView) *tgbot.InlineKeyboardMarkup {
	keys, userID := v.Keys, v.UserID
	rows := make([][]tgbot.InlineKeyboardButton, 0, 2*len(keys)+1)
	for _, k := range keys {
		pub, name := k.Peer.PublicKey, keyLabel(k.Peer)
		dis := tgbot.Button("⛔️ Отключить: "+name, cbAdminDis+pub)
		first := []tgbot.InlineKeyboardButton{dis}
		if !k.Peer.Enabled {
			first = []tgbot.InlineKeyboardButton{tgbot.Button("✅ Включить: "+name, cbAdminEn+pub)}
		}
		// A forever key has nothing to extend.
		if !k.Peer.ExpiresAt.IsZero() {
			extend := tgbot.Button(fmt.Sprintf("➕ %d дней: %s", adminExtendDays, name), cbAdminExt+pub)
			first = append(first, extend)
			if !k.Peer.Enabled && keyEnded(k.Peer) {
				first = tgbot.Row(extend) // enabling an ended key is undone within a minute
			}
		}
		rows = append(
			rows,
			first,
			tgbot.Row(tgbot.Button("📄 Конфиг: "+name, cbAdminCfg+pub), tgbot.Button("🗑 Удалить: "+name, cbAdminDel+pub)),
		)
	}
	for _, p := range recentPayments(v.Payments) {
		if p.RefundedAt.IsZero() {
			label := fmt.Sprintf("↩️ Вернуть %d ⭐ (%s)", p.Stars, p.CreatedAt.In(msk).Format("02.01"))
			ref := paymentRef{
				UserID: userID,
				Ref:    payRef(p.ChargeID),
			}
			rows = append(rows, tgbot.Row(tgbot.Button(label, cbAdminRef+ref.String())))
		}
	}
	rows = append(
		rows,
		tgbot.Row(tgbot.Button("🔑 Выдать ключ", cbAdminIss+strconv.FormatInt(userID, 10))),
		tgbot.Row(
			tgbot.Button("◀️ К списку", cbAdminUsers+"0"),
			tgbot.Button("◀️ Меню", cbMenu),
		),
	)
	return tgbot.InlineKeyboard(rows...)
}

// keyLabel names a key on buttons: its name, or its IP when it has none.
func keyLabel(p *service.Peer) string {
	if p.Name != "" {
		return p.Name
	}
	return p.IP
}

const (
	issueTermText = "🔑 Выдать ключ без оплаты. На какой срок?"
)

// issueTermKeyboard: 7 / 30 / 90 / 365 days or forever, then cancel.
func issueTermKeyboard(userID int64) *tgbot.InlineKeyboardMarkup {
	prefix := cbAdminIssD + strconv.FormatInt(userID, 10) + ":"
	cancel := cbAdminUser + strconv.FormatInt(userID, 10)
	return tgbot.InlineKeyboard(
		tgbot.Row(
			tgbot.Button("7 дней", prefix+"7"),
			tgbot.Button("30 дней", prefix+"30"),
			tgbot.Button("90 дней", prefix+"90"),
		),
		tgbot.Row(
			tgbot.Button("365 дней", prefix+"365"),
			tgbot.Button("♾ Бессрочно", prefix+"0"),
		),
		tgbot.Row(tgbot.Button("Отмена", cancel)),
	)
}

// paymentsText lists a user's payments for the admin card.
// cardPaymentsLimit: the card shows only the newest payments, so a long
// history can't push it over Telegram's message and keyboard limits.
const cardPaymentsLimit = 10

func paymentsText(ps []*service.Payment) string {
	if len(ps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n💳 Оплаты:\n")
	for _, p := range recentPayments(ps) {
		fmt.Fprintf(&b, "• %s — %d ⭐, %s", mskTime(p.CreatedAt), p.Stars, paidFor(p))
		if !p.RefundedAt.IsZero() {
			b.WriteString(", ↩️ возвращено")
		}
		b.WriteString("\n")
	}
	older := len(ps) - cardPaymentsLimit
	if older > 0 {
		fmt.Fprintf(&b, "…и ещё %d старых\n", older)
	}
	return b.String()
}

// recentPayments is the newest cardPaymentsLimit payments (ps is newest
// first).
func recentPayments(ps []*service.Payment) []*service.Payment {
	return ps[:min(len(ps), cardPaymentsLimit)]
}

// paidFor says what a payment bought: its term, or that the purchase was
// refused (such a charge is recorded without days: nothing was given).
func paidFor(p *service.Payment) string {
	if p.Days == 0 {
		return "оплата отклонена"
	}
	return tariffLabel(p.Days)
}

func refundConfirmText(p *service.Payment) string {
	if p.Days == 0 {
		return fmt.Sprintf("↩️ Вернуть %d ⭐ за отклонённую оплату (%s)?", p.Stars, mskTime(p.CreatedAt))
	}
	return fmt.Sprintf(
		"↩️ Вернуть %d ⭐ за «%s» (оплата %s)?\nКлюч не отключится — если нужно, отключите его отдельно.",
		p.Stars,
		tariffLabel(p.Days),
		mskTime(p.CreatedAt),
	)
}

func refundConfirmKeyboard(ref paymentRef) *tgbot.InlineKeyboardMarkup {
	return tgbot.InlineKeyboard(
		tgbot.Row(
			tgbot.Button("↩️ Да, вернуть", cbAdminRefOK+ref.String()),
			tgbot.Button("Отмена", cbAdminUser+strconv.FormatInt(ref.UserID, 10)),
		),
	)
}

func refundedToUserText(p *service.Payment) string {
	if p.Days == 0 {
		return fmt.Sprintf("↩️ Вам вернули %d ⭐.", p.Stars)
	}
	return fmt.Sprintf("↩️ Вам вернули %d ⭐ за «%s».", p.Stars, tariffLabel(p.Days))
}

// tariffLabel: 30 → "1 месяц", 90 → "3 месяца", 365 → "12 месяцев",
// anything else in days: 7 → "7 дней", 21 → "21 день".
func tariffLabel(days int) string {
	months := []string{
		"месяц",
		"месяца",
		"месяцев",
	}
	switch {
	case days == 365:
		return "12 " + months[pluralForm(12)]
	case days%30 == 0:
		return fmt.Sprintf("%d %s", days/30, months[pluralForm(days/30)])
	}
	dayForms := []string{
		"день",
		"дня",
		"дней",
	}
	return fmt.Sprintf("%d %s", days, dayForms[pluralForm(days)])
}

// pluralForm picks the Russian noun form for n: 0 for "1 день", 1 for
// "2 дня", 2 for "5 дней".
func pluralForm(n int) int {
	switch {
	case n%10 == 1 && n%100 != 11:
		return 0
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return 1
	}
	return 2
}

func deleteConfirmText(p *service.Peer) string {
	return fmt.Sprintf("🗑 Удалить ключ %s (%s)?\nКлюч перестанет работать сразу, отменить это нельзя.", p.Name, p.IP)
}

func deleteConfirmKeyboard(p *service.Peer) *tgbot.InlineKeyboardMarkup {
	return tgbot.InlineKeyboard(
		tgbot.Row(
			tgbot.Button("🗑 Да, удалить", cbAdminDelOK+p.PublicKey),
			tgbot.Button("Отмена", cbAdminUser+strconv.FormatInt(p.UserID, 10)),
		),
	)
}

const unknownCommandText = "Не знаю такой команды. Нажмите /menu — там всё меню."

// termsText is /terms: what is sold, payment, refunds, rules, data kept.
// Telegram requires it for bots that take Stars.
func termsText(contact string) string {
	return "📄 Условия использования\n\n" +
		"1. Что вы получаете. Доступ к сервису (протокол AmneziaWG) на выбранный срок: ключ-конфиг для приложений AmneziaVPN и AmneziaWG. " +
		"Бесплатный пробный период — один раз на Telegram-аккаунт.\n" +
		"2. Оплата — в Telegram Stars. Срок прибавляется к текущему; ключ, отключённый после окончания срока, включается обратно.\n" +
		"3. Возврат. Если оплату не удалось применить, звёзды возвращаются автоматически. " +
		"После начала пользования звёзды не возвращаются; спорные случаи — /paysupport.\n" +
		"4. Правила. Нельзя использовать сервис для незаконных действий, спама, атак и взлома, а также передавать ключ другим людям. " +
		"При нарушении доступ отключается без возврата.\n" +
		"5. Доступность. Мы стараемся, чтобы сервис работал всегда, но не гарантируем 100%: " +
		"возможны перерывы на обслуживание и блокировки со стороны провайдеров.\n" +
		"6. Данные. Мы храним ваш Telegram ID и username, ключи доступа (в зашифрованном виде), даты и суммы оплат, " +
		"время последнего подключения и объём трафика — только для работы сервиса. " +
		"Какие сайты вы открываете, мы не записываем.\n" +
		"7. Поддержка: " + contact + " или /support.\n\n" +
		"Оплачивая, вы принимаете эти условия."
}

// supportText is /support: who to write to and what to include.
func supportText(v supportView) string {
	return fmt.Sprintf(
		"💬 Поддержка: %s\n\nНапишите, что случилось: устройство, приложение и что не работает. "+
			"Укажите ваш ID: %d — так мы быстрее найдём ваш ключ.\nВопросы по оплате: /paysupport.",
		v.Contact,
		v.UserID,
	)
}

// paySupportText is /paysupport. Telegram requires it for Stars and wants
// it to say that Telegram support can't help with purchases in the bot.
func paySupportText(contact string) string {
	return "По вопросам оплаты пишите " + contact + ": укажите, когда платили и сколько звёзд. " +
		"Поддержка Telegram не может помочь с покупками в этом боте."
}

func cancelKeyboard() *tgbot.InlineKeyboardMarkup {
	return tgbot.InlineKeyboard(
		tgbot.Row(tgbot.Button("Отмена", cbAdminCanc)),
	)
}

// keyEnded: the key has an end date and it has passed.
func keyEnded(p *service.Peer) bool {
	return !p.ExpiresAt.IsZero() && !p.ExpiresAt.After(time.Now())
}

// adminErrorText explains a failed admin action. known is true for the
// expected cases; an unknown error gets a generic text (the caller logs the
// raw error, which stays out of the chat).
func adminErrorText(err error) (text string, known bool) {
	switch {
	case errors.Is(err, service.ErrExpired):
		return fmt.Sprintf("⚠️ Срок ключа закончился — нажмите «➕ %d дней», ключ включится сам.", adminExtendDays), true
	case errors.Is(err, service.ErrNoPrivateKey):
		return noPrivateKeyText, true
	case errors.Is(err, service.ErrNotFound):
		return "⚠️ Не найдено — возможно, ключ или пользователь уже удалены.", true
	case errors.Is(err, errPaymentNotFound):
		return "⚠️ Оплата не найдена — откройте карточку ещё раз.", true
	case errors.Is(err, service.ErrIPTaken):
		return "⚠️ IP этого ключа уже занят другим ключом на сервере. Проверьте сверку (Reconcile) в журнале.", true
	case errors.Is(err, service.ErrUnreadable):
		return "⚠️ Данные ключа повреждены (не расшифровываются): вернуть его на сервер нельзя. Удалите ключ и выдайте новый.", true
	case errors.Is(err, service.ErrBlocked):
		return "⚠️ Ключ отключён администратором: сначала включите его.", true
	}
	return "⚠️ Не получилось, подробности в журнале бота.", false
}

// backupAlertText: the last good backup is too old (zero = none found).
func backupAlertText(last time.Time) string {
	when := "не найден"
	if !last.IsZero() {
		when = "был " + mskTime(last)
	}
	return "⚠️ Свежего бэкапа нет: последний удачный бэкап " + when + ". " +
		"Проверьте на сервере: journalctl -u geoirb-vpn-bot-backup"
}

// Exit tunnel alerts (tunnel watcher).
const (
	tunnelDownText = "⚠️ Туннель за границу не работает: у клиентов не открываются зарубежные сайты (российские работают). Бот перешёл на прямое подключение."
	tunnelUpText   = "✅ Туннель за границу восстановлен."
)

// tunnelText is the alert for a new tunnel state.
func tunnelText(st tunnel.State) string {
	if st == tunnel.Up {
		return tunnelUpText
	}
	return tunnelDownText
}

// ruNetsAlertText: the RU networks list is too old (zero = never updated).
func ruNetsAlertText(last time.Time) string {
	when := "не обновлялся с " + mskTime(last)
	if last.IsZero() {
		when = "ни разу не обновлялся"
	}
	return "⚠️ Список российских сетей " + when + ": проверьте journalctl -u geoirb-ru-nets."
}

// mskTime formats a moment in Moscow time, saying so.
func mskTime(t time.Time) string {
	return t.In(msk).Format("02.01.2006 15:04") + " по Москве"
}

func reconcileFailedText(err error) string {
	return "⚠️ Сверка базы и сервера не удалась: " + err.Error()
}
