// Package render owns HTML template parsing and fragment rendering.
package render

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"strings"
	"time"

	"vartalap/internal/models"
	"vartalap/internal/services"
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
		"timeTitle":  timeTitle,
		"rfc3339":    rfc3339,
		"senderKey":  services.SenderKey,
		"initial":    initial,
	}

	base := template.New("base").Funcs(funcs)
	base, err := base.ParseFS(assets.FS, "layouts/*.html", "components/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse base templates: %w", err)
	}

	renderer := &Renderer{pages: make(map[string]*template.Template), fragments: base}
	for name, file := range map[string]string{"welcome": "pages/welcome.html", "chat": "pages/chat.html"} {
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

// RenderMessage renders one message as an out-of-band WebSocket fragment for a viewer.
func (r *Renderer) RenderMessage(message models.Message, viewerID string) ([]byte, error) {
	message.Mine = message.DeviceID == viewerID
	return r.Fragment("message-fragment", struct {
		Message models.Message
	}{Message: message})
}

// RenderPresence renders the online count fragment.
func (r *Renderer) RenderPresence(count int) ([]byte, error) {
	return r.Fragment("presence-fragment", struct{ Count int }{Count: count})
}

// RenderNotice renders a toast fragment.
func (r *Renderer) RenderNotice(level, text string) ([]byte, error) {
	return r.Fragment("notice-fragment", struct{ Level, Text string }{Level: level, Text: text})
}

// RenderName renders the updated display name fragment.
func (r *Renderer) RenderName(name string) ([]byte, error) {
	return r.Fragment("name-fragment", struct{ Name string }{Name: name})
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

func timeTitle(value time.Time) string {
	return value.Local().Format("Monday, 2 January 2006 at 15:04")
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
