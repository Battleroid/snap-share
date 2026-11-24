package items_test

import (
	"net/url"
	"strings"
	"testing"
)

// The unlock card says what to do, names nothing about the item, and puts a
// wrong password on the field.
func TestUnlockPageStatesWhatToDoAndErrorsOnTheField(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	slug := str(e.put("ann", "private-notes.txt", "secret", "", header("X-Snap-Password", "hunter2-long")), "slug")

	_, page := e.do("GET", "/"+slug, nil)
	for _, want := range []string{"Password required", "Enter the password the sender gave you", `for="unlock-password"`, "data-autofocus-first", "auth-form.js"} {
		if !strings.Contains(page, want) {
			t.Errorf("unlock page lacks %q", want)
		}
	}
	if strings.Contains(page, "private-notes") || strings.Contains(page, "aria-invalid") {
		t.Error("a fresh unlock page names the item or is already invalid")
	}

	form := strings.NewReader(url.Values{"password": {"not it"}, "next": {"/" + slug}}.Encode())
	res, body := e.do("POST", "/unlock/"+slug, form, header("Content-Type", "application/x-www-form-urlencoded"))
	if res.StatusCode != 403 {
		t.Fatalf("wrong password: %d", res.StatusCode)
	}
	for _, want := range []string{`aria-invalid="true"`, `id="unlock-password-error"`, "Wrong password.", `aria-describedby="unlock-password-error"`} {
		if !strings.Contains(body, want) {
			t.Errorf("wrong password page lacks %q", want)
		}
	}
	if strings.Contains(body, "private-notes") || strings.Contains(body, "pb-notice") {
		t.Error("the wrong-password page names the item or shows the error as a banner")
	}
}
