package render

import (
	"bytes"
	"strings"
	"testing"

	"vartalap/internal/models"
)

func parse(t *testing.T) *Renderer {
	t.Helper()
	renderer, err := Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return renderer
}

func TestPagesRender(t *testing.T) {
	renderer := parse(t)
	user := &models.User{ID: "u", Username: "neel", DisplayName: "Neel"}
	profile := &models.Summary{ID: "u", Username: "neel", DisplayName: "Neel", Discoverable: true}
	cases := []struct {
		name     string
		data     models.PageData
		contains []string
	}{
		{"onboarding", models.PageData{Title: "Welcome", View: "onboarding", CSRF: "csrf"}, []string{"Create identity", "Restore with phrase", "onboarding.js"}},
		{"app", models.PageData{Title: "Chats", View: "app", CSRF: "csrf", User: user}, []string{"data-user-id=\"u\"", "message-input", "app.js"}},
		{"profile", models.PageData{Title: "Neel", View: "profile", CSRF: "csrf", Profile: profile}, []string{"@neel", "Start a private chat"}},
		{"notfound", models.PageData{Title: "Missing", View: "notfound"}, []string{"Nothing here"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var buffer bytes.Buffer
			if err := renderer.Page(&buffer, test.name, test.data); err != nil {
				t.Fatalf("render %s: %v", test.name, err)
			}
			html := buffer.String()
			for _, marker := range test.contains {
				if !strings.Contains(html, marker) {
					t.Fatalf("%s output missing %q", test.name, marker)
				}
			}
		})
	}
}

func TestDiscoverFragment(t *testing.T) {
	renderer := parse(t)
	if !renderer.HasFragment("discover-results") {
		t.Fatal("discover-results fragment should exist")
	}
	page := models.DirectoryPage{
		Users: []models.Summary{{ID: "u", Username: "neel", DisplayName: "Neel"}},
	}
	output, err := renderer.Fragment("discover-results", page)
	if err != nil {
		t.Fatalf("fragment: %v", err)
	}
	html := string(output)
	for _, marker := range []string{"data-start-chat=\"neel\"", "@neel", "Neel"} {
		if !strings.Contains(html, marker) {
			t.Fatalf("fragment missing %q: %s", marker, html)
		}
	}
}

func TestUnknownPageFails(t *testing.T) {
	renderer := parse(t)
	if err := renderer.Page(&bytes.Buffer{}, "nope", models.PageData{}); err == nil {
		t.Fatal("unknown page should fail")
	}
}
