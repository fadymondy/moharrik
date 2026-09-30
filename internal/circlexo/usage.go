package circlexo

import (
	"context"
	"time"

	"github.com/circlexo/circlexo-go"
	"github.com/circlexo/circlexo-go/usage"

	"github.com/fadymondy/moharrik/internal/contacts"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/workspaces"
)

/*
Pay-as-you-go usage, and the hooks into the rest of Moharrik.

Two meters exist in Moharrik and are reported, for hub-linked workspaces only (an unlinked or
inactive workspace, and everything when the integration is off, reports nothing):

  - contacts    +1 for each contact CREATED (contacts.Upsert, the one path every import, form, chat
                and manual add goes through); a match or a merge is not a new contact. It is a
                creation counter: deleting a contact does not report a negative.
  - emails_sent the number of messages a campaign send delivered, reported once when the send
                finishes. Test sends, confirmation mail, one-to-one inbox replies, automation mail
                and the trends digest are not campaign sends and are not metered.

Moharrik has no AI-credit, research or WhatsApp metering or budget of its own (there is nothing to
report), so none is invented. Reports are idempotent (contact id; campaign id + send attempt) and
buffered by the SDK reporter, which retries; a full buffer drops the report and logs it.
*/

func (s *Service) startUsage() {
	if s.Usage != nil {
		return
	}
	r := usage.NewReporter(s.Client, 1024, 2)
	r.OnResult = func(u circlexo.UsageReport, _ circlexo.UsageResult, err error) {
		if err != nil {
			s.Log.Warn("circlexo: usage report failed", "meter", u.Feature, "org", u.OrgID, "err", err)
		}
	}
	s.Usage = r
	contacts.OnCreated = func(ws, id string) { s.reportUsage(ws, MeterContacts, 1, "contact:"+id) }
	marketing.AfterCampaignSend = func(ws, campaign string, sent int64, attempt string) {
		s.reportUsage(ws, MeterEmails, sent, "campaign:"+campaign+":"+attempt)
	}
	marketing.BeforeCampaignSend = s.beforeCampaignSend
	workspaces.BeforeInvite = s.beforeInvite
}

func (s *Service) stopUsage() {
	contacts.OnCreated = nil
	marketing.AfterCampaignSend = nil
	marketing.BeforeCampaignSend = nil
	workspaces.BeforeInvite = nil
	if s.Usage == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Usage.Close(ctx)
}

// reportUsage queues one usage report for a workspace if the hub governs it.
func (s *Service) reportUsage(workspaceID, meter string, qty int64, key string) {
	if s.Usage == nil || qty <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	link, err := s.OrgByWorkspace(ctx, workspaceID)
	if err != nil || !link.Active {
		return
	}
	if err := s.Usage.Report(link.OrgID, meter, qty, key); err != nil {
		s.Log.Warn("circlexo: usage not queued", "meter", meter, "org", link.OrgID, "err", err)
	}
}
