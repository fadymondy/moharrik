package inbox

import (
	"strings"
	"testing"
)

func TestCheckVisitorContact(t *testing.T) {
	for _, c := range []struct {
		name, email, phone string
		required           bool
		want               visitorContact
		err                string
	}{
		{"Mona", "mona@shop.test", "", true, visitorContact{"Mona", "mona@shop.test", ""}, ""},
		{"  Mona \n Adel ", "MONA@Shop.Test ", "", true, visitorContact{"Mona Adel", "mona@shop.test", ""}, ""},
		{"Mona", "", "+20 101 234 5678", true, visitorContact{"Mona", "", "+201012345678"}, ""},
		{"Mona", "", "0020-101-234-5678", true, visitorContact{"Mona", "", "+201012345678"}, ""},
		{"منى", "", "+٢٠١٠١٢٣٤٥٦٧٨", true, visitorContact{"منى", "", "+201012345678"}, ""},
		{"", "mona@shop.test", "", true, visitorContact{}, "name"},
		{"Mona", "", "", true, visitorContact{}, "email or a phone"},
		{"Mona", "mona@", "", true, visitorContact{}, "email"},
		{"Mona", "Mona <mona@shop.test>", "", true, visitorContact{}, "email"},
		{"Liam", "o'connor@shop.test", "", true, visitorContact{"Liam", "o'connor@shop.test", ""}, ""},
		{"Mona", "mo..na@shop.test", "", true, visitorContact{}, "email"},
		{"Mona", "mona@shop", "", true, visitorContact{}, "email"},
		{"Mona", "", "01012345678", true, visitorContact{}, "country code"},
		{"Mona", "", "+12", true, visitorContact{}, "country code"},
		// The form off: lenient, as before.
		{"", "", "", false, visitorContact{}, ""},
		{"Visitor", "not-an-email", "", false, visitorContact{"Visitor", "not-an-email", ""}, ""},
	} {
		got, err := checkVisitorContact(c.name, c.email, c.phone, c.required)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), "contact_required") || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q %q %q: err = %v, want one about %q", c.name, c.email, c.phone, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q %q %q: got %+v, %v want %+v", c.name, c.email, c.phone, got, err, c.want)
		}
	}
	if got, _ := checkVisitorContact(strings.Repeat("x", 300), "a@b.test", "", true); len(got.Name) != 120 {
		t.Errorf("a long name is capped at 120, got %d", len(got.Name))
	}
}
