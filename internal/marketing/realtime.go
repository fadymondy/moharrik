package marketing

import (
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/realtime"
)

/*
Live campaigns and subscribers (MH-413).

The campaigns page polled every two seconds while a send ran, and the contacts
page not at all. Both now listen on the workspace's event stream, as the inbox
does: marketing publishes a thin "something changed, re-read it" event, and
the page revalidates through the normal, RLS-scoped endpoints.

	campaigns  a campaign was drafted, edited, deleted, claimed for sending,
	           moved on (every batch of recipients) or finished — for
	           members who may read campaigns
	contacts   a subscription changed: somebody subscribed, confirmed or
	           unsubscribed (by link or by a member), an address bounced, or
	           WhatsApp consent changed — for members who may read contacts

The audience is decided by realtime.Event.For like every other event, so a
member who cannot read campaigns never hears about them. Nothing is alerting:
a send progressing is not a reason to chime.
*/

// Event kinds marketing publishes.
const (
	EventCampaigns = "campaigns"
	EventContacts  = "contacts"
)

// PublishCampaigns tells the workspace's open tabs a campaign changed.
func PublishCampaigns(a *app.App, wsID, campaignID string) {
	if a == nil || a.Events == nil || wsID == "" {
		return
	}
	a.Events.Publish(wsID, realtime.Event{Kind: EventCampaigns, ItemID: campaignID, Permission: authz.CampaignsRead})
}

// PublishContacts tells them a contact's subscriptions or consent changed.
func PublishContacts(a *app.App, wsID string) {
	if a == nil || a.Events == nil || wsID == "" {
		return
	}
	a.Events.Publish(wsID, realtime.Event{Kind: EventContacts, Permission: authz.ContactsRead})
}
