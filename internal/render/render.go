// Package render owns HTML template parsing and fragment rendering.
package render

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"vartalap/internal/models"
	assets "vartalap/templates"
)

// Renderer renders full pages and small HTML fragments.
type Renderer struct {
	pages     map[string]*template.Template
	fragments *template.Template
}

// Parse loads all embedded templates.
func Parse() (*Renderer, error) {
	funcs := template.FuncMap{
		"formatTime": formatTime,
		"rfc3339":    rfc3339,
		"initial":    initial,
		"avatarURL":  avatarURL,
		"hasAvatar":  func(updatedAt int64) bool { return updatedAt > 0 },
		"lower":      strings.ToLower,
	}

	base := template.New("base").Funcs(funcs)
	base, err := base.ParseFS(assets.FS, "layouts/*.html", "components/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse base templates: %w", err)
	}

	pages := map[string]string{
		"onboarding": "pages/onboarding.html",
		"app":        "pages/app.html",
		"profile":    "pages/profile.html",
		"notfound":   "pages/notfound.html",
	}
	renderer := &Renderer{pages: make(map[string]*template.Template), fragments: base}
	for name, file := range pages {
		page, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone base for %s: %w", name, err)
		}
		if _, err := page.ParseFS(assets.FS, file); err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		renderer.pages[name] = page
	}
	return renderer, nil
}

// Page renders a complete HTML document.
func (r *Renderer) Page(w io.Writer, name string, data models.PageData) error {
	page, ok := r.pages[name]
	if !ok {
		return fmt.Errorf("unknown page %q", name)
	}
	return page.ExecuteTemplate(w, "layout", data)
}

// Fragment renders a named partial into a byte slice.
func (r *Renderer) Fragment(name string, data any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := r.fragments.ExecuteTemplate(&buffer, name, data); err != nil {
		return nil, fmt.Errorf("render fragment %s: %w", name, err)
	}
	return buffer.Bytes(), nil
}

// HasFragment reports whether a named fragment exists (used by tests).
func (r *Renderer) HasFragment(name string) bool {
	return r.fragments.Lookup(name) != nil
}

// FileSystem returns the embedded template filesystem (used by tests).
func FileSystem() fs.FS { return assets.FS }

func formatTime(value time.Time) string {
	local := value.Local()
	now := time.Now().Local()
	if local.Year() == now.Year() && local.YearDay() == now.YearDay() {
		return local.Format("15:04")
	}
	return local.Format("2 Jan, 15:04")
}

func rfc3339(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}

// initial returns an uppercase first grapheme-ish character for avatars.
func initial(name string) string {
	trimmed := strings.TrimSpace(name)
	for _, r := range trimmed {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// avatarURL returns a cache-busting avatar URL, or empty when there is none.
func avatarURL(userID string, updatedAt int64) string {
	if updatedAt <= 0 || userID == "" {
		return ""
	}
	return "/avatar/" + userID + "?v=" + strconv.FormatInt(updatedAt, 10)
}
