package handlers

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"vartalap/internal/config"
	"vartalap/internal/models"
	"vartalap/internal/render"
	"vartalap/internal/repository"
	"vartalap/internal/services"
	appws "vartalap/internal/websocket"
)

type testEnv struct {
	server *httptest.Server
	store  *repository.Store
	signer *services.IdentitySigner
}

type testIdentity struct {
	deviceID string
	token    string
	csrf     string
	userID   string
	username string
	encKey   *ecdsa.PrivateKey
	sigKey   *ecdsa.PrivateKey
	encPub   string
	sigPub   string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		Addr:                    ":0",
		DBPath:                  filepath.Join(dir, "test.db"),
		DataDir:                 dir,
		Environment:             "test",
		RatePerSecond:           1000,
		RateBurst:               1000,
		MaxAttachmentBytes:      1024 * 1024,
		MaxAvatarBytes:          1024 * 1024,
		MessageRetentionDays:    30,
		AttachmentRetentionDays: 7,
		MaxDevicesPerUser:       1,
	}
	store, err := repository.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	renderer, err := render.Parse()
	if err != nil {
		t.Fatalf("render parse: %v", err)
	}
	files, err := services.NewFileStore(cfg.DataDir)
	if err != nil {
		t.Fatalf("file store: %v", err)
	}
	signer := services.NewIdentitySigner([]byte("test-secret-that-is-long-enough-for-hmac"))
	hub := appws.NewHub(store)
	t.Cleanup(hub.Close)

	handler := New(Dependencies{
		Config:   cfg,
		Store:    store,
		Renderer: renderer,
		Hub:      hub,
		Files:    files,
		Limiter:  services.NewLimiter(1000, 1000),
		Signer:   signer,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &testEnv{server: server, store: store, signer: signer}
}

func newKeys(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	raw := elliptic.Marshal(elliptic.P256(), key.PublicKey.X, key.PublicKey.Y)
	return key, base64.StdEncoding.EncodeToString(raw)
}

func signRaw(t *testing.T, key *ecdsa.PrivateKey, message string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(message))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw := make([]byte, 64)
	r.FillBytes(raw[:32])
	s.FillBytes(raw[32:])
	return base64.StdEncoding.EncodeToString(raw)
}

func (env *testEnv) request(t *testing.T, identity *testIdentity, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequest(method, env.server.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if identity != nil {
		request.AddCookie(&http.Cookie{Name: services.DeviceIDCookieName, Value: identity.token})
		request.AddCookie(&http.Cookie{Name: services.DeviceSyncCookieName, Value: identity.token})
		request.AddCookie(&http.Cookie{Name: "vartalap_csrf", Value: identity.csrf})
		request.Header.Set("X-CSRF-Token", identity.csrf)
	}
	response, err := env.server.Client().Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	content, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	return response, content
}

func (env *testEnv) register(t *testing.T, username string) *testIdentity {
	t.Helper()
	deviceID, err := services.NewDeviceID()
	if err != nil {
		t.Fatal(err)
	}
	encKey, encPub := newKeys(t)
	sigKey, sigPub := newKeys(t)
	identity := &testIdentity{
		deviceID: deviceID,
		token:    env.signer.Issue(deviceID),
		csrf:     strings.Repeat("c", 32) + username,
		username: username,
		encKey:   encKey,
		sigKey:   sigKey,
		encPub:   encPub,
		sigPub:   sigPub,
	}
	timestamp := time.Now().UTC().UnixMilli()
	canonical := services.CanonicalRegistration(
		deviceID, username, "Display "+username, encPub, sigPub, timestamp)
	signature := signRaw(t, sigKey, canonical)
	response, content := env.request(t, identity, http.MethodPost, "/api/identity/register", map[string]any{
		"username":     username,
		"displayName":  "Display " + username,
		"deviceName":   "Test device",
		"encPublicKey": encPub,
		"sigPublicKey": sigPub,
		"timestamp":    timestamp,
		"signature":    signature,
		"backup": map[string]any{
			"ciphertext":    base64.StdEncoding.EncodeToString(make([]byte, 64)),
			"iv":            base64.StdEncoding.EncodeToString(make([]byte, 12)),
			"kdfSalt":       base64.StdEncoding.EncodeToString(make([]byte, 16)),
			"kdfIterations": 210000,
		},
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("register %s: status %d body %s", username, response.StatusCode, content)
	}
	var decoded struct {
		User models.Summary `json:"user"`
	}
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatalf("decode register: %v", err)
	}
	identity.userID = decoded.User.ID
	return identity
}

func decodeMap(t *testing.T, content []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatalf("decode json %s: %v", content, err)
	}
	return value
}

func TestRegistrationAndIdentityEndpoints(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice")
	bob := env.register(t, "bob")

	response, content := env.request(t, alice, http.MethodGet, "/api/me", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("me status %d: %s", response.StatusCode, content)
	}
	me := decodeMap(t, content)
	user := me["user"].(map[string]any)
	if user["username"] != "alice" {
		t.Fatalf("me user = %#v", user)
	}
	if me["fingerprint"] == "" {
		t.Fatal("me should include a fingerprint")
	}

	response, content = env.request(t, alice, http.MethodGet, "/api/directory", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("directory status %d: %s", response.StatusCode, content)
	}
	if !strings.Contains(string(content), "bob") {
		t.Fatalf("directory should list bob: %s", content)
	}

	response, content = env.request(t, alice, http.MethodGet, "/api/users/search?q=bo", nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(content), "bob") {
		t.Fatalf("search = %d %s", response.StatusCode, content)
	}

	response, content = env.request(t, alice, http.MethodGet, "/api/keys/bob", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("keys status %d: %s", response.StatusCode, content)
	}
	bundle := decodeMap(t, content)["bundle"].(map[string]any)
	if bundle["encPublicKey"] != bob.encPub || bundle["sigPublicKey"] != bob.sigPub {
		t.Fatalf("bundle keys do not match bob: %#v", bundle)
	}

	response, _ = env.request(t, nil, http.MethodGet, "/api/me", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated me status = %d, want 401", response.StatusCode)
	}
}

func TestProfileUpdateIsAtomicOnUsernameConflict(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice")
	env.register(t, "bob")

	// A taken username must not leave the display name or visibility changed.
	response, content := env.request(t, alice, http.MethodPost, "/api/profile", map[string]any{
		"displayName":  "Changed Name",
		"username":     "bob",
		"discoverable": false,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("conflicting profile update = %d %s", response.StatusCode, content)
	}

	response, content = env.request(t, alice, http.MethodGet, "/api/me", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("me = %d %s", response.StatusCode, content)
	}
	user := decodeMap(t, content)["user"].(map[string]any)
	if user["username"] != "alice" || user["displayName"] != "Display alice" || user["discoverable"] != true {
		t.Fatalf("partial profile update leaked: %#v", user)
	}

	// A valid update changes everything together.
	response, content = env.request(t, alice, http.MethodPost, "/api/profile", map[string]any{
		"displayName":  "Alice New",
		"username":     "alice_new",
		"discoverable": false,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("profile update = %d %s", response.StatusCode, content)
	}
	user = decodeMap(t, content)["user"].(map[string]any)
	if user["username"] != "alice_new" || user["displayName"] != "Alice New" || user["discoverable"] != false {
		t.Fatalf("profile update result = %#v", user)
	}
}

func TestConversationAndMessageFlow(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice")
	bob := env.register(t, "bob")

	response, content := env.request(t, alice, http.MethodPost, "/api/conversations", map[string]any{"username": "bob"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create conversation status %d: %s", response.StatusCode, content)
	}
	decoded := decodeMap(t, content)
	conversation := decoded["conversation"].(map[string]any)
	conversationID := conversation["id"].(string)
	if conversationID == "" {
		t.Fatal("missing conversation id")
	}

	envelope := makeEnvelope(t, alice, bob, conversationID)
	response, content = env.request(t, alice, http.MethodPost,
		"/api/conversations/"+conversationID+"/messages", envelope)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("send message status %d: %s", response.StatusCode, content)
	}
	sent := decodeMap(t, content)
	if sent["duplicate"] != false {
		t.Fatalf("first send duplicate = %v", sent["duplicate"])
	}

	// Retrying the same id is idempotent while the envelope is queued.
	response, content = env.request(t, alice, http.MethodPost,
		"/api/conversations/"+conversationID+"/messages", envelope)
	if response.StatusCode != http.StatusOK || decodeMap(t, content)["duplicate"] != true {
		t.Fatalf("duplicate send = %d %s", response.StatusCode, content)
	}

	response, content = env.request(t, bob, http.MethodGet, "/api/messages/pending", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("pending status %d: %s", response.StatusCode, content)
	}
	pending := decodeMap(t, content)["messages"].([]any)
	if len(pending) != 1 {
		t.Fatalf("pending count = %d, want 1", len(pending))
	}
	first := pending[0].(map[string]any)["envelope"].(map[string]any)
	if first["id"] != envelope.ID {
		t.Fatalf("pending id = %v, want %s", first["id"], envelope.ID)
	}

	response, content = env.request(t, bob, http.MethodPost, "/api/messages/ack",
		map[string]any{"messageIds": []string{envelope.ID}})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ack status %d: %s", response.StatusCode, content)
	}
	response, content = env.request(t, bob, http.MethodGet, "/api/messages/pending", nil)
	if response.StatusCode != http.StatusOK || len(decodeMap(t, content)["messages"].([]any)) != 0 {
		t.Fatalf("pending after ack = %d %s", response.StatusCode, content)
	}

	// A sender can delete an envelope that has not been acked.
	second := makeEnvelope(t, alice, bob, conversationID)
	response, content = env.request(t, alice, http.MethodPost,
		"/api/conversations/"+conversationID+"/messages", second)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("send second status %d: %s", response.StatusCode, content)
	}
	response, content = env.request(t, alice, http.MethodDelete, "/api/messages/"+second.ID, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d: %s", response.StatusCode, content)
	}
}

func TestMessageRejectsBadSignatureAndCsrf(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice")
	bob := env.register(t, "bob")
	_, content := env.request(t, alice, http.MethodPost, "/api/conversations", map[string]any{"username": "bob"})
	conversationID := decodeMap(t, content)["conversation"].(map[string]any)["id"].(string)

	envelope := makeEnvelope(t, alice, bob, conversationID)
	envelope.Signature = signRaw(t, alice.sigKey, "not the transcript")
	response, content := env.request(t, alice, http.MethodPost,
		"/api/conversations/"+conversationID+"/messages", envelope)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("bad signature status = %d, body %s", response.StatusCode, content)
	}

	// CSRF is required for state-changing requests.
	request, _ := http.NewRequest(http.MethodPost, env.server.URL+"/api/conversations",
		strings.NewReader(`{"username":"bob"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: services.DeviceIDCookieName, Value: alice.token})
	request.AddCookie(&http.Cookie{Name: services.DeviceSyncCookieName, Value: alice.token})
	request.AddCookie(&http.Cookie{Name: "vartalap_csrf", Value: alice.csrf})
	response, err := env.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want 403", response.StatusCode)
	}
}

func makeEnvelope(t *testing.T, sender, recipient *testIdentity, conversationID string) models.Envelope {
	t.Helper()
	_, ephemeral := newKeys(t)
	envelope := models.Envelope{
		ID:                 mustUUID(t),
		ConversationID:     conversationID,
		SenderUserID:       sender.userID,
		SenderDeviceID:     sender.deviceID,
		RecipientUserID:    recipient.userID,
		Type:               "text",
		EphemeralPublicKey: ephemeral,
		IV:                 base64.StdEncoding.EncodeToString(randomBytes(t, 12)),
		Ciphertext:         base64.StdEncoding.EncodeToString(randomBytes(t, 32)),
		SenderSigningKey:   sender.sigPub,
		ClientCreatedAt:    time.Now().UTC().UnixMilli(),
	}
	envelope.Signature = signRaw(t, sender.sigKey, services.CanonicalMessage(envelope))
	return envelope
}

func randomBytes(t *testing.T, length int) []byte {
	t.Helper()
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal(err)
	}
	return buffer
}

func mustUUID(t *testing.T) string {
	t.Helper()
	id, err := services.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRecoveryRebindsDeviceAndRevokesOldOne(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice")

	newDeviceID, err := services.NewDeviceID()
	if err != nil {
		t.Fatal(err)
	}
	recovered := &testIdentity{
		deviceID: newDeviceID,
		token:    env.signer.Issue(newDeviceID),
		csrf:     strings.Repeat("r", 32),
	}

	response, content := env.request(t, recovered, http.MethodPost,
		"/api/identity/recovery/start", map[string]any{"username": "alice"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("recovery start = %d %s", response.StatusCode, content)
	}
	started := decodeMap(t, content)
	challenge := started["challenge"].(string)
	if challenge == "" {
		t.Fatal("missing challenge")
	}

	timestamp := time.Now().UTC().UnixMilli()
	canonical := services.CanonicalRecovery(alice.userID, newDeviceID, challenge, timestamp)
	signature := signRaw(t, alice.sigKey, canonical)
	response, content = env.request(t, recovered, http.MethodPost,
		"/api/identity/recovery/complete", map[string]any{
			"challenge":  challenge,
			"timestamp":  timestamp,
			"signature":  signature,
			"deviceName": "Recovered browser",
		})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("recovery complete = %d %s", response.StatusCode, content)
	}

	response, content = env.request(t, recovered, http.MethodGet, "/api/me", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("recovered me = %d %s", response.StatusCode, content)
	}
	me := decodeMap(t, content)
	if me["user"].(map[string]any)["username"] != "alice" {
		t.Fatalf("recovered user = %#v", me["user"])
	}
	if me["device"].(map[string]any)["id"] != newDeviceID {
		t.Fatalf("recovered device = %#v", me["device"])
	}

	response, _ = env.request(t, alice, http.MethodGet, "/api/me", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old device should be revoked, got %d", response.StatusCode)
	}

	// A consumed challenge cannot be replayed.
	response, _ = env.request(t, recovered, http.MethodPost,
		"/api/identity/recovery/complete", map[string]any{
			"challenge":  challenge,
			"timestamp":  timestamp,
			"signature":  signature,
			"deviceName": "Again",
		})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("replayed challenge status = %d, want 400", response.StatusCode)
	}
}

func TestAttachmentUploadAndMembership(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice")
	bob := env.register(t, "bob")
	mallory := env.register(t, "mallory")
	_, content := env.request(t, alice, http.MethodPost, "/api/conversations", map[string]any{"username": "bob"})
	conversationID := decodeMap(t, content)["conversation"].(map[string]any)["id"].(string)

	payload := randomBytes(t, 512)
	request, err := http.NewRequest(http.MethodPost,
		env.server.URL+"/api/conversations/"+conversationID+"/attachments", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("X-CSRF-Token", alice.csrf)
	request.AddCookie(&http.Cookie{Name: services.DeviceIDCookieName, Value: alice.token})
	request.AddCookie(&http.Cookie{Name: services.DeviceSyncCookieName, Value: alice.token})
	request.AddCookie(&http.Cookie{Name: "vartalap_csrf", Value: alice.csrf})
	response, err := env.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d %s", response.StatusCode, body)
	}
	attachmentID := decodeMap(t, body)["id"].(string)

	response, downloaded := env.request(t, bob, http.MethodGet, "/api/attachments/"+attachmentID, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d %s", response.StatusCode, downloaded)
	}
	if !bytes.Equal(downloaded, payload) {
		t.Fatal("downloaded ciphertext does not match upload")
	}

	response, _ = env.request(t, mallory, http.MethodGet, "/api/attachments/"+attachmentID, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("non-member download status = %d, want 403", response.StatusCode)
	}
}

func dialWS(t *testing.T, env *testEnv, identity *testIdentity) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Origin", env.server.URL)
	if identity != nil {
		header.Set("Cookie", fmt.Sprintf("%s=%s; %s=%s; vartalap_csrf=%s",
			services.DeviceIDCookieName, identity.token,
			services.DeviceSyncCookieName, identity.token,
			identity.csrf))
	}
	url := "ws" + strings.TrimPrefix(env.server.URL, "http") + "/ws"
	conn, response, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial websocket: %v (status %d)", err, status)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readWSEvent(t *testing.T, conn *websocket.Conn, wanted string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read websocket %q: %v", wanted, err)
		}
		var event map[string]any
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode websocket event %s: %v", payload, err)
		}
		if event["type"] == wanted {
			return event
		}
	}
}

func TestWebSocketDeliveryAndAck(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice")
	bob := env.register(t, "bob")
	_, content := env.request(t, alice, http.MethodPost, "/api/conversations", map[string]any{"username": "bob"})
	conversationID := decodeMap(t, content)["conversation"].(map[string]any)["id"].(string)

	aliceWS := dialWS(t, env, alice)
	readWSEvent(t, aliceWS, "ready")
	bobWS := dialWS(t, env, bob)
	readWSEvent(t, bobWS, "ready")

	envelope := makeEnvelope(t, alice, bob, conversationID)
	response, body := env.request(t, alice, http.MethodPost,
		"/api/conversations/"+conversationID+"/messages", envelope)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("send = %d %s", response.StatusCode, body)
	}

	messageEvent := readWSEvent(t, bobWS, "message")
	received := messageEvent["envelope"].(map[string]any)
	if received["id"] != envelope.ID {
		t.Fatalf("received id = %v, want %s", received["id"], envelope.ID)
	}
	if sender := messageEvent["sender"].(map[string]any); sender["username"] != "alice" {
		t.Fatalf("sender = %#v", sender)
	}

	if err := bobWS.WriteJSON(map[string]any{
		"type": "ack", "messageIds": []string{envelope.ID},
	}); err != nil {
		t.Fatalf("write ack: %v", err)
	}
	delivery := readWSEvent(t, aliceWS, "delivery")
	if delivery["messageId"] != envelope.ID || delivery["state"] != "delivered" {
		t.Fatalf("delivery = %#v", delivery)
	}

	if err := aliceWS.WriteJSON(map[string]any{
		"type": "typing", "conversationId": conversationID, "state": "start",
	}); err != nil {
		t.Fatalf("write typing: %v", err)
	}
	typing := readWSEvent(t, bobWS, "typing")
	if typing["conversationId"] != conversationID || typing["state"] != "start" {
		t.Fatalf("typing = %#v", typing)
	}
}

func TestWebSocketRequiresAuthAndSameOrigin(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice")
	url := "ws" + strings.TrimPrefix(env.server.URL, "http") + "/ws"

	header := http.Header{}
	header.Set("Origin", env.server.URL)
	if _, response, err := websocket.DefaultDialer.Dial(url, header); err == nil {
		t.Fatal("unauthenticated websocket should fail")
	} else if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthenticated status = %v", response)
	}

	header = http.Header{}
	header.Set("Origin", "https://evil.example")
	header.Set("Cookie", fmt.Sprintf("%s=%s; %s=%s",
		services.DeviceIDCookieName, alice.token,
		services.DeviceSyncCookieName, alice.token))
	if _, response, err := websocket.DefaultDialer.Dial(url, header); err == nil {
		t.Fatal("cross-origin websocket should fail")
	} else if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin status = %v", response)
	}
}
