package bot

import tgbot "github.com/irbgeo/go-tgbot"

// buttonQuery asks containsButton whether Keyboard has a button with Data.
type buttonQuery struct {
	Keyboard *tgbot.InlineKeyboardMarkup
	Data     string
}
