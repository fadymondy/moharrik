package notifications

import "github.com/fadymondy/moharrik/internal/secret"

// A webhook source's signing secret is sealed at rest (internal/secret). It is
// shown once, at creation or rotation, and never returned by the API again.
var box = secret.New(secret.DomainWebhookSecret, "the webhook secret", "rotate it")

func seal(plain string) (string, error)    { return box.Seal(plain) }
func unseal(sealed string) (string, error) { return box.Open(sealed) }

// newSecret makes a 32-byte random signing secret, hex-encoded (64 chars) —
// long enough that a curl example fits on one line and unambiguous in a URL
// or a shell without quoting tricks.
func newSecret() (string, error) { return secret.RandomHex(32) }
