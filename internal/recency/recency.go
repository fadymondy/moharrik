// Package recency is the one rule every channel applies to inbound history:
// the inbox collects what arrives from the moment a channel is connected, not
// everything the account ever held. Connecting a mailbox with years of unread
// mail filed 2019's newsletters as fresh conversations; a WhatsApp reconnect
// can replay old messages the same way. An import (MH-562) is the explicit,
// separate way to bring history in.
package recency

import "time"

// Grace is how far before the connection a message may be and still count as
// new: mail that landed while the owner was filling in the form, a WhatsApp
// message sent minutes before the QR scan.
const Grace = 24 * time.Hour

// Cutoff is the oldest send time a channel connected at `connected` files.
// A zero `connected` (a row from before the column existed) means "now".
func Cutoff(connected time.Time) time.Time {
	if connected.IsZero() {
		connected = time.Now()
	}
	return connected.Add(-Grace)
}

// TooOld reports whether a message sent at `sent` predates the channel's
// cutoff. A zero `sent` (the channel gave no date) is never too old: dropping
// a real message for missing metadata is worse than filing one stale one.
func TooOld(sent, connected time.Time) bool {
	return !sent.IsZero() && sent.Before(Cutoff(connected))
}
