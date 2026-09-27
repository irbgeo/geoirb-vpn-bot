package main

import (
	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/bot"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// serveInput is what serve runs.
type serveInput struct {
	Client  *tgbot.Client
	Router  *bot.Router
	Service *service.Service
}
