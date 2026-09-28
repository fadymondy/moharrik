package inbox

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

// MH-658: a voice note keeps the length WhatsApp reported, and its download
// honours byte ranges — without them a browser reads an Ogg/Opus file's
// duration as Infinity and the player showed 0:00.
func TestVoiceNoteLengthAndRanges(t *testing.T) {
	t.Setenv("AUTH_SECRET", "voice-note-range-fixture-0123456789")
	a := testApp(t)
	f := ccWorkspace(t, a)
	phone := "20155505" + numericIDSuffix()[:4]
	audio := bytes.Repeat([]byte("OggS"), 1000)
	ext := "WAVOICE" + idSuffix()
	dl := func(context.Context) (whatsapp.MediaFile, error) {
		return whatsapp.MediaFile{Data: audio, MimeType: "audio/ogg; codecs=opus"}, nil
	}
	if err := FileWhatsApp(f.ctx, a, f.wsID, phone, "Rana", "", ext,
		&whatsapp.Media{Kind: "voice", MimeType: "audio/ogg; codecs=opus", Seconds: 5}, dl); err != nil {
		t.Fatal(err)
	}
	var id, seconds string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT t.id, coalesce(t.meta->>'seconds', '') FROM message_attachments t JOIN messages m ON m.id = t.message_id
		WHERE m.workspace_id = $1 AND m.external_id = $2`, f.wsID, ext).Scan(&id, &seconds); err != nil {
		t.Fatalf("the voice note was not filed: %v", err)
	}
	if seconds != "5" {
		t.Errorf("meta.seconds = %q, want 5", seconds)
	}

	_, api := humatest.New(t)
	RegisterAttachments(api, a)
	url := AttachmentURL(f.wsID, id)

	whole := api.Get(url)
	if whole.Code != http.StatusOK || whole.Body.Len() != len(audio) {
		t.Fatalf("full download: %d, %d bytes", whole.Code, whole.Body.Len())
	}
	if got := whole.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q, want bytes", got)
	}
	if got := whole.Header().Get("Content-Type"); got != "audio/ogg; codecs=opus" {
		t.Errorf("Content-Type = %q", got)
	}

	part := api.Get(url, "Range: bytes=3990-")
	if part.Code != http.StatusPartialContent {
		t.Fatalf("range request: %d, want 206", part.Code)
	}
	if part.Body.Len() != 10 || part.Header().Get("Content-Range") != "bytes 3990-3999/4000" {
		t.Errorf("range body %d bytes, Content-Range %q", part.Body.Len(), part.Header().Get("Content-Range"))
	}
}
