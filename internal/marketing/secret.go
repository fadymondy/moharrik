package marketing

import "github.com/fadymondy/moharrik/internal/secret"

// The SMTP password is sealed at rest (internal/secret), so a database dump
// alone does not hand out a mailbox. It is never returned by the API.
var box = secret.New(secret.DomainSMTP, "the SMTP password", "enter it again")

func seal(plain string) (string, error)    { return box.Seal(plain) }
func unseal(sealed string) (string, error) { return box.Open(sealed) }
