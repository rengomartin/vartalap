package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"vartalap/internal/models"
)

func TestFragmentsUseHTMXSwapAttributes(t *testing.T) {
	renderer, err := Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	message := models.Message{
		ID: 1, DeviceID: "own-device", SenderName: "<b>Aap</b>",
		Content: "<script>x</script>", CreatedAt: time.Now(),
	}

	messageHTML, err := renderer.RenderMessage(message, "own-device")
	if err != nil {
		t.Fatalf("RenderMessage: %v", err)
	}
	rendered := string(messageHTML)
	for _, expected := range []string{
		`id="message-list"`, `hx-swap-oob="beforeend"`,
		`data-mine="true"`, `&lt;script&gt;`, `&lt;b&gt;Aap&lt;/b&gt;`,
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("message fragment missing %q: %s", expected, rendered)
		}
	}

	otherHTML, err := renderer.RenderMessage(message, "another-device")
	if err != nil || !strings.Contains(string(otherHTML), `data-mine="false"`) {
		t.Fatalf("other viewer fragment: %s err=%v", otherHTML, err)
	}

	presence, err := renderer.RenderPresence(3)
	if err != nil || !strings.Contains(string(presence), `id="online-count" hx-swap-oob="true"`) {
		t.Fatalf("presence fragment: %s err=%v", presence, err)
	}
	if !strings.Contains(string(presence), ">3<") {
		t.Fatalf("presence count missing: %s", presence)
	}

	name, err := renderer.RenderName("New Name")
	if err != nil || !strings.Contains(string(name), `hx-swap-oob="outerHTML"`) || !strings.Contains(string(name), "New Name") {
		t.Fatalf("name fragment: %s err=%v", name, err)
	}

	history, err := renderer.Fragment("history", models.MessageList{
		Messages: []models.Message{message}, ViewerID: "own-device", HasMore: true, OldestID: 1,
	})
	if err != nil || !strings.Contains(string(history), `hx-target="#load-more" hx-swap="afterend"`) {
		t.Fatalf("history fragment: %s err=%v", history, err)
	}
}

func TestChatPageConnectsWebSocket(t *testing.T) {
	renderer, err := Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var page bytes.Buffer
	if err := renderer.Page(&page, "chat", models.PageData{
		Title: "Public chat", DisplayName: "Aap", Online: 2, MaxRunes: 2000,
	}); err != nil {
		t.Fatalf("Page: %v", err)
	}
	rendered := page.String()
	for _, expected := range []string{
		`id="chat-view"`, `hx-ext="ws"`, `ws-connect="/ws"`,
		`id="message-list"`, `id="composer"`, `id="online-count">2<`,
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("chat page missing %q", expected)
		}
	}
	// The composer sends through the websocket wrapper in app.js so that the
	// sender can render an optimistic message immediately.
	if strings.Contains(rendered, `ws-send`) {
		t.Fatalf("chat page should not rely on htmx ws-send")
	}
}

func TestRenderedMessagesStayLeftAligned(t *testing.T) {
	renderer, err := Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	message := models.Message{ID: 7, DeviceID: "device", SenderName: "Aap", Content: "hi", CreatedAt: time.Now()}
	message.Mine = true
	fragment, err := renderer.Fragment("message-fragment", struct {
		Message models.Message
	}{Message: message})
	if err != nil {
		t.Fatalf("fragment: %v", err)
	}
	rendered := string(fragment)
	if strings.Contains(rendered, "justify-end") || strings.Contains(rendered, "text-right") {
		t.Fatalf("own message should stay left aligned: %s", rendered)
	}
	if !strings.Contains(rendered, `msg-own`) {
		t.Fatalf("own message missing accent styling: %s", rendered)
	}
}

func TestNonceIsEchoedInMessageFragment(t *testing.T) {
	renderer, err := Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	message := models.Message{ID: 8, SenderName: "Aap", Content: "hi", CreatedAt: time.Now(), Nonce: "abc123"}
	fragment, err := renderer.RenderMessage(message, "device")
	if err != nil {
		t.Fatalf("RenderMessage: %v", err)
	}
	if !strings.Contains(string(fragment), `data-nonce="abc123"`) {
		t.Fatalf("nonce not rendered: %s", fragment)
	}
}
