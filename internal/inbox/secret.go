package inbox

import "github.com/fadymondy/moharrik/internal/secret"

// A mailbox password is sealed at rest (internal/secret). It is write-only: no
// endpoint returns it, and the UI can only replace it. A mailbox password opens
// a real mail account, so there is no "reveal".
var box = secret.New(secret.DomainMailboxPassword, "the mailbox password", "enter it again")

func seal(plain string) (string, error)    { return box.Seal(plain) }
func unseal(sealed string) (string, error) { return box.Open(sealed) }
