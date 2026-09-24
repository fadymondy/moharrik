package channels

import "github.com/fadymondy/moharrik/internal/secret"

// Every credential this package seals gets its own domain (internal/secret):
// a Telegram bot token and its webhook secret_token, a Slack bot token and
// its signing secret, and a Discord bot token. Two secrets of different kinds
// are never interchangeable, the same isolation every other pair in
// internal/secret already has.
var (
	telegramBotBox    = secret.New(secret.DomainTelegramBotToken, "the Telegram bot token", "add it again")
	telegramSecretBox = secret.New(secret.DomainTelegramSecretTok, "the Telegram webhook secret", "reconnect the channel")
	slackBotBox       = secret.New(secret.DomainSlackBotToken, "the Slack bot token", "add it again")
	slackSigningBox   = secret.New(secret.DomainSlackSigningSecret, "the Slack signing secret", "add it again")
	discordBotBox     = secret.New(secret.DomainDiscordBotToken, "the Discord bot token", "add it again")
)
