package whatsapp

import "github.com/fadymondy/moharrik/internal/secret"

// A WhatsApp webhook token is sealed at rest (internal/secret). It is the whole
// authorization for a public endpoint, so it is never returned by any API: it
// goes from here to Evolution's webhook headers and nowhere else.
var box = secret.New(secret.DomainWhatsAppToken, "the WhatsApp token", "link the number again")

func seal(plain string) (string, error)    { return box.Seal(plain) }
func unseal(sealed string) (string, error) { return box.Open(sealed) }
