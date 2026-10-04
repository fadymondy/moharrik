package inbox

import (
	"strings"
	"testing"
)

func TestStripHTMLDropsStyleAndHead(t *testing.T) {
	in := `<html><head><title>Freeletics</title><style>* { box-sizing: border-box; } body { margin:0 }
@media (max-width:600px){ .x{display:none} }</style></head>
<body><script>var a = {b:1};</script><div><h1>Your plan is ready</h1><p>Hi Fady,</p><p>Start&nbsp;today &amp; win.</p></div></body></html>`
	got := stripHTML(in)
	want := "Your plan is ready\n\nHi Fady,\n\nStart today & win."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestStripHTMLCommentsAndEntities(t *testing.T) {
	in := `<!-- tracking: { a: b } --><p>It&#8217;s done&nbsp;&nbsp;now &amp;nbsp; ok<br>next</p><!--[if mso]><style>p{x:y}</style><![endif]-->`
	got := stripHTML(in)
	if strings.Contains(got, "tracking") || strings.Contains(got, "mso") || strings.Contains(got, "p{") {
		t.Fatalf("comment leaked: %q", got)
	}
	if want := "It’s done now &nbsp; ok\nnext"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestStripHTMLArabic(t *testing.T) {
	got := stripHTML(`<div dir="rtl"><style>p{color:red}</style><p>مرحبا فادي</p><p>شكرا لك</p></div>`)
	if want := "مرحبا فادي\n\nشكرا لك"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPreviewText(t *testing.T) {
	cases := map[string]string{
		"* { box-sizing: border-box; } body { margin:0 } Hello Fady":                          "Hello Fady",
		"@font-face { font-family: X; src: url(a) } @media (max-width:5px) { a { b: c } } Hi": "Hi",
		"Use a { b } in a sentence":         "Use a { b } in a sentence",
		"Please Set a { color: red } later": "Please Set a { color: red } later",
		"مرحبا { } فادي":                    "مرحبا { } فادي",
		"96  *{box-sizing:border-box}body{margin:0;padding:0}a[x-apple-data-detectors]{color:inherit!important}#MessageViewBody a{color:inherit} 5 kids. 1 decision.": "96 5 kids. 1 decision.",
		"Hello {name}, welcome": "Hello {name}, welcome",
		"  plain text  ":        "plain text",
	}
	for in, want := range cases {
		if got := previewText(in); got != want {
			t.Errorf("previewText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPreviewLine(t *testing.T) {
	cases := map[string]string{
		"sup, &#8202;   \n\n  مرحباً   بك":              "sup, مرحباً بك",
		"\u034f \u200c \u034f \u200c Hello\u00ad there": "Hello there",
		"Here's what worked.         Start":             "Here's what worked. Start",
	}
	for in, want := range cases {
		if got := previewLine(in); got != want {
			t.Errorf("previewLine(%q) = %q, want %q", in, got, want)
		}
	}
}
