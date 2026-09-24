package prospects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/research"
)

/*
Outreach drafts (MH-397). A draft is TEXT: stored, shown, copied by a person.
Nothing in this file — or anywhere else — sends it.

With a model configured, the draft is written by the "draft" task from the
evidence only (the prompt says so and gives nothing else). Without one — the
production deployment today — it is a template in the requested language,
built from the strongest piece of evidence and the profile's offer, and it
says it is a template. Either way the evidence it rests on is recorded with
it.
*/

// signalPhrase is how a draft mentions the news: "{company} {phrase}".
var signalPhrase = map[string][2]string{
	"funding":     {"recently raised funding", "حصلت مؤخراً على تمويل"},
	"hiring":      {"is growing its team", "توسّع فريقها"},
	"expansion":   {"is expanding", "تتوسّع"},
	"launch":      {"just launched something new", "أطلقت مؤخراً منتجاً جديداً"},
	"partnership": {"announced a new partnership", "أعلنت عن شراكة جديدة"},
	"leadership":  {"has new leadership", "لديها قيادة جديدة"},
	"technology":  {"is modernising its technology", "تحدّث تقنياتها"},
	"regulatory":  {"is working through new regulation", "تتعامل مع تنظيمات جديدة"},
}

// MakeDraft writes an outreach draft for a prospect and stores it.
func MakeDraft(ctx context.Context, db *sql.DB, client *ai.Client, ws, userID, id, locale string) (*ProspectDraft, error) {
	if locale != "ar" {
		locale = "en"
	}
	d, err := Get(ctx, db, ws, id)
	if err != nil {
		return nil, err
	}
	if len(d.Timeline) == 0 {
		return nil, errors.New("this prospect has no evidence left, and a draft with nothing to point to would be invented outreach")
	}
	prof, err := GetProfile(ctx, db, ws)
	if err != nil {
		return nil, err
	}
	// The strongest pieces of evidence, newest first among equals.
	ev := append([]research.Evidence(nil), d.Timeline...)
	for i := 1; i < len(ev); i++ {
		for j := i; j > 0 && (ev[j].Confidence > ev[j-1].Confidence); j-- {
			ev[j], ev[j-1] = ev[j-1], ev[j]
		}
	}
	if len(ev) > 5 {
		ev = ev[:5]
	}
	var wsName string
	_ = db.QueryRowContext(ctx, `SELECT name FROM workspaces WHERE id = $1`, ws).Scan(&wsName)

	var subject, body, generator string
	if client != nil {
		subject, body, generator, err = modelDraft(ctx, client, d.Prospect, ev, prof.Offer, wsName, locale)
	}
	if client == nil || err != nil {
		subject, body = templateDraft(d.Prospect, ev[0], prof.Offer, wsName, locale)
		generator = "template"
	}
	ids := make([]string, len(ev))
	for i, e := range ev {
		ids[i] = e.ID
	}
	var out ProspectDraft
	if err := db.QueryRowContext(ctx, `INSERT INTO prospect_drafts (workspace_id, prospect_id, locale, subject, body, generator, evidence_ids, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id, created_at`,
		ws, id, locale, trim(subject, 200), trim(body, 4000), generator, pgArr(ids), userID).Scan(&out.ID, &out.CreatedAt); err != nil {
		return nil, err
	}
	out.Locale, out.Subject, out.Body, out.Generator, out.EvidenceIDs = locale, trim(subject, 200), trim(body, 4000), generator, ids
	return &out, nil
}

func templateDraft(p Prospect, e research.Evidence, offer, wsName, locale string) (string, string) {
	phrase := signalPhrase[e.SignalType]
	who := p.Contact.Name
	first := strings.Fields(who)
	quote := trimWords(e.Quote, 220)
	src := e.SourceDomain
	if src == "" {
		src = e.SourceURL
	}
	if locale == "ar" {
		greet := "مرحباً فريق " + p.Name + "،"
		if len(first) > 0 {
			greet = "مرحباً " + first[0] + "،"
		}
		news := "قرأت خبراً عن " + p.Name
		if phrase[1] != "" {
			news = "قرأت أن " + p.Name + " " + phrase[1]
		}
		pitch := strings.TrimSpace(offer)
		if pitch == "" {
			pitch = "[اكتب هنا ما تقدّمه في جملة أو جملتين]"
		}
		subject := p.Name + " — " + firstNonEmpty(phrase[1], "فكرة سريعة")
		body := fmt.Sprintf("%s\n\n%s: «%s» (%s).\n\n%s\n\nهل تناسبكم مكالمة قصيرة الأسبوع القادم؟\n\nمع التحية،\n[اسمك]%s",
			greet, news, quote, src, pitch, sig(wsName, "\n"))
		return subject, body
	}
	greet := "Hi " + p.Name + " team,"
	if len(first) > 0 {
		greet = "Hi " + first[0] + ","
	}
	news := "I came across news about " + p.Name
	if phrase[0] != "" {
		news = "I read that " + p.Name + " " + phrase[0]
	}
	pitch := strings.TrimSpace(offer)
	if pitch == "" {
		pitch = "[Say what you offer in a sentence or two]"
	}
	subject := p.Name + " — " + firstNonEmpty(phrase[0], "a quick idea")
	body := fmt.Sprintf("%s\n\n%s: \"%s\" (%s).\n\n%s\n\nWould a short call next week be useful?\n\nBest,\n[Your name]%s",
		greet, news, quote, src, pitch, sig(wsName, "\n"))
	return subject, body
}

func sig(wsName, sep string) string {
	if strings.TrimSpace(wsName) == "" {
		return ""
	}
	return sep + wsName
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

const draftSystem = `You write a short, plain first outreach email from a small business to a company it has researched.

Rules:
- Use ONLY the facts in the evidence given. Do not invent numbers, names, dates or claims.
- Mention one piece of evidence, specifically, and say where it was reported.
- If no person is named, address the team. Never invent a name.
- Under 120 words. No hype. End with one simple question.
- Write in the language asked for.
- Answer as: first line "Subject: …", then a blank line, then the email.`

func modelDraft(ctx context.Context, client *ai.Client, p Prospect, ev []research.Evidence, offer, wsName, locale string) (string, string, string, error) {
	var b strings.Builder
	lang := "English"
	if locale == "ar" {
		lang = "Arabic"
	}
	fmt.Fprintf(&b, "Language: %s\nFrom: %s\nWhat we offer: %s\nTo company: %s", lang, wsName, firstNonEmpty(offer, "(not given — keep it general)"), p.Name)
	if p.Domain != "" {
		fmt.Fprintf(&b, " (%s)", p.Domain)
	}
	if p.Contact.Name != "" {
		fmt.Fprintf(&b, "\nTo person: %s, %s", p.Contact.Name, p.Contact.Title)
	} else {
		b.WriteString("\nTo person: none named — address the team")
	}
	b.WriteString("\n\nEvidence (the only facts you may use):\n")
	for i, e := range ev {
		fmt.Fprintf(&b, "%d. \"%s\" — %s\n", i+1, trim(e.Quote, 300), e.SourceURL)
	}
	res, err := client.Generate(ctx, ai.TaskDraft, ai.GenerateRequest{
		System: draftSystem, Messages: []ai.Message{{Role: "user", Content: b.String()}}, MaxTokens: 500, Temperature: 0.4,
	})
	if err != nil {
		return "", "", "", err
	}
	text := strings.TrimSpace(res.Text)
	if text == "" {
		return "", "", "", errors.New("the model wrote nothing")
	}
	subject := p.Name
	if strings.HasPrefix(strings.ToLower(text), "subject:") {
		line, rest, _ := strings.Cut(text, "\n")
		subject = strings.TrimSpace(line[len("subject:"):])
		text = strings.TrimSpace(rest)
	}
	return subject, text, "model:" + res.Provenance.Provider, nil
}

// trimWords shortens at a word boundary, so a quoted sentence never ends
// mid-word.
func trimWords(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexAny(cut, " 	"); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:") + "…"
}
