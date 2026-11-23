package handlers

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	appmiddleware "vartalap/internal/middleware"
	"vartalap/internal/models"
	"vartalap/internal/repository"
	"vartalap/internal/services"
)

const (
	backupCiphertextMax = 32 * 1024
	backupSaltBytes     = 16
	backupIVBytes       = 12
	minKDFIterations    = 100000
	maxKDFIterations    = 2000000
	proofSkew           = 10 * time.Minute
)

type backupPayload struct {
	Ciphertext    string `json:"ciphertext"`
	IV            string `json:"iv"`
	KDFSalt       string `json:"kdfSalt"`
	KDFIterations int    `json:"kdfIterations"`
}

type registerRequest struct {
	Username     string        `json:"username"`
	DisplayName  string        `json:"displayName"`
	DeviceName   string        `json:"deviceName"`
	EncPublicKey string        `json:"encPublicKey"`
	SigPublicKey string        `json:"sigPublicKey"`
	Timestamp    int64         `json:"timestamp"`
	Signature    string        `json:"signature"`
	Backup       backupPayload `json:"backup"`
}

type deviceView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"createdAt"`
	LastSeenAt int64  `json:"lastSeenAt"`
}

type identityResponse struct {
	User        models.Summary `json:"user"`
	Device      deviceView     `json:"device"`
	Fingerprint string         `json:"fingerprint"`
}

func (h *handler) register(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(w, r) {
		return
	}
	deviceID := appmiddleware.DeviceID(r.Context())
	if !services.IsValidDeviceID(deviceID) {
		apiError(w, http.StatusBadRequest, "invalid_device", "This browser has no valid device identity.")
		return
	}
	if !h.allow("register", deviceID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Too many identity attempts. Please wait.")
		return
	}
	if _, _, found, err := h.store.GetUserByDeviceID(r.Context(), deviceID); err != nil {
		h.jsonServerError(w, err)
		return
	} else if found {
		apiError(w, http.StatusConflict, "already_registered", "This device already has an identity.")
		return
	}

	var request registerRequest
	if !decodeJSON(w, r, &request, 64*1024) {
		return
	}
	username, err := services.ValidateUsername(request.Username)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_username", err.Error())
		return
	}
	displayName, err := services.ValidateDisplayName(request.DisplayName)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_display_name", err.Error())
		return
	}
	deviceName, err := services.ValidateDeviceName(request.DeviceName)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_device_name", err.Error())
		return
	}
	if err := services.ValidatePublicKey(request.EncPublicKey); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_key", "The encryption key is invalid.")
		return
	}
	if err := services.ValidatePublicKey(request.SigPublicKey); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_key", "The signing key is invalid.")
		return
	}
	if skew(request.Timestamp) {
		apiError(w, http.StatusBadRequest, "stale_proof", "The identity proof has expired. Reload the page.")
		return
	}
	canonical := services.CanonicalRegistration(deviceID, username, displayName,
		request.EncPublicKey, request.SigPublicKey, request.Timestamp)
	if err := services.VerifySignature(request.SigPublicKey, request.Signature, canonical); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_proof", "The identity proof could not be verified.")
		return
	}
	if err := validateBackup(request.Backup); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_backup", err.Error())
		return
	}

	userID, err := services.NewUUID()
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	now := time.Now().UTC()
	user := models.User{
		ID:           userID,
		Username:     username,
		DisplayName:  displayName,
		Discoverable: true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	device := models.Device{
		ID:           deviceID,
		UserID:       userID,
		Name:         deviceName,
		EncPublicKey: request.EncPublicKey,
		SigPublicKey: request.SigPublicKey,
		CreatedAt:    now,
		LastSeenAt:   now,
	}
	backup := models.IdentityBackup{
		UserID:        userID,
		Ciphertext:    request.Backup.Ciphertext,
		IV:            request.Backup.IV,
		KDFSalt:       request.Backup.KDFSalt,
		KDFIterations: request.Backup.KDFIterations,
	}
	if err := h.store.CreateAccount(r.Context(), &user, &device, backup); err != nil {
		if errors.Is(err, repository.ErrUsernameTaken) {
			apiError(w, http.StatusConflict, "username_taken", "That username is already taken.")
			return
		}
		h.jsonServerError(w, err)
		return
	}
	user.PrimaryDeviceID = device.ID
	writeJSON(w, http.StatusCreated, identityResponse{
		User:        user.Summary(),
		Device:      toDeviceView(device),
		Fingerprint: services.Fingerprint(device.EncPublicKey, device.SigPublicKey),
	})
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	device := mustDevice(r)
	_, hasBackup, err := h.store.GetIdentityBackup(r.Context(), user.ID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":              user.Summary(),
		"device":            toDeviceView(device),
		"fingerprint":       services.Fingerprint(device.EncPublicKey, device.SigPublicKey),
		"hasRecoveryBackup": hasBackup,
	})
}

type profileRequest struct {
	Username     *string `json:"username"`
	DisplayName  string  `json:"displayName"`
	Discoverable *bool   `json:"discoverable"`
}

func (h *handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(w, r) {
		return
	}
	user := mustUser(r)
	if !h.allow("profile", user.ID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Too many profile changes. Please wait.")
		return
	}
	var request profileRequest
	if !decodeJSON(w, r, &request, 8*1024) {
		return
	}
	displayName, err := services.ValidateDisplayName(request.DisplayName)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_display_name", err.Error())
		return
	}
	discoverable := user.Discoverable
	if request.Discoverable != nil {
		discoverable = *request.Discoverable
	}
	username := ""
	if request.Username != nil && services.NormalizeUsername(*request.Username) != user.Username {
		validated, err := services.ValidateUsername(*request.Username)
		if err != nil {
			apiError(w, http.StatusBadRequest, "invalid_username", err.Error())
			return
		}
		username = validated
	}
	if err := h.store.UpdateProfile(r.Context(), user.ID, displayName, discoverable, username); err != nil {
		if errors.Is(err, repository.ErrUsernameTaken) {
			apiError(w, http.StatusConflict, "username_taken", "That username is already taken.")
			return
		}
		h.jsonServerError(w, err)
		return
	}
	if username != "" {
		user.Username = username
	}
	user.DisplayName = displayName
	user.Discoverable = discoverable
	writeJSON(w, http.StatusOK, map[string]any{"user": user.Summary()})
}

func (h *handler) uploadAvatar(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(w, r) {
		return
	}
	user := mustUser(r)
	if !h.allow("profile", user.ID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Too many profile changes. Please wait.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxAvatarBytes+64*1024)
	if err := r.ParseMultipartForm(h.cfg.MaxAvatarBytes + 64*1024); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_upload", "That image could not be read.")
		return
	}
	file, _, err := r.FormFile("avatar")
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_upload", "Choose an image to upload.")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, h.cfg.MaxAvatarBytes+1))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_upload", "That image could not be read.")
		return
	}
	if int64(len(data)) > h.cfg.MaxAvatarBytes {
		apiError(w, http.StatusRequestEntityTooLarge, "too_large", "That image is too large.")
		return
	}
	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/png", "image/jpeg", "image/webp":
	default:
		apiError(w, http.StatusBadRequest, "invalid_type", "Only PNG, JPEG or WebP images are supported.")
		return
	}
	if err := h.files.SaveAvatar(user.ID, data, contentType); err != nil {
		h.jsonServerError(w, err)
		return
	}
	now := time.Now().UTC()
	if err := h.store.SetAvatar(r.Context(), user.ID, now); err != nil {
		h.jsonServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"avatarUrl": avatarURL(user.ID, now.UnixMilli())})
}

func (h *handler) forgetDevice(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(w, r) {
		return
	}
	user := mustUser(r)
	if err := h.store.RevokeDevices(r.Context(), user.ID, ""); err != nil {
		h.jsonServerError(w, err)
		return
	}
	appmiddleware.ClearIdentityCookies(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type recoveryStartRequest struct {
	Username string `json:"username"`
}

func (h *handler) recoveryStart(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(w, r) {
		return
	}
	deviceID := appmiddleware.DeviceID(r.Context())
	if !h.allow("recovery", deviceID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Too many recovery attempts. Please wait.")
		return
	}
	var request recoveryStartRequest
	if !decodeJSON(w, r, &request, 4*1024) {
		return
	}
	username, err := services.ValidateUsername(request.Username)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_username", err.Error())
		return
	}
	user, found, err := h.store.GetUserByUsername(r.Context(), username)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusNotFound, "not_found", "No recoverable identity was found for that username.")
		return
	}
	device, found, err := h.store.GetActiveDeviceForUser(r.Context(), user.ID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusConflict, "no_device", "That identity has no active device.")
		return
	}
	backup, found, err := h.store.GetIdentityBackup(r.Context(), user.ID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusNotFound, "no_backup", "That identity has no recovery backup.")
		return
	}
	nonce, err := services.RandomToken(32)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if err := h.store.CreateRecoveryChallenge(r.Context(), user.ID, nonce, time.Now().UTC().Add(5*time.Minute)); err != nil {
		h.jsonServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"challenge":    nonce,
		"userId":       user.ID,
		"username":     user.Username,
		"displayName":  user.DisplayName,
		"encPublicKey": device.EncPublicKey,
		"sigPublicKey": device.SigPublicKey,
		"backup": map[string]any{
			"ciphertext":    backup.Ciphertext,
			"iv":            backup.IV,
			"kdfSalt":       backup.KDFSalt,
			"kdfIterations": backup.KDFIterations,
		},
	})
}

type recoveryCompleteRequest struct {
	Challenge  string `json:"challenge"`
	Timestamp  int64  `json:"timestamp"`
	Signature  string `json:"signature"`
	DeviceName string `json:"deviceName"`
}

func (h *handler) recoveryComplete(w http.ResponseWriter, r *http.Request) {
	if !h.csrfOK(w, r) {
		return
	}
	deviceID := appmiddleware.DeviceID(r.Context())
	if !services.IsValidDeviceID(deviceID) {
		apiError(w, http.StatusBadRequest, "invalid_device", "This browser has no valid device identity.")
		return
	}
	if !h.allow("recovery", deviceID) {
		apiError(w, http.StatusTooManyRequests, "rate_limited", "Too many recovery attempts. Please wait.")
		return
	}
	var request recoveryCompleteRequest
	if !decodeJSON(w, r, &request, 8*1024) {
		return
	}
	if request.Challenge == "" || skew(request.Timestamp) {
		apiError(w, http.StatusBadRequest, "stale_proof", "The recovery proof has expired. Start again.")
		return
	}
	deviceName, err := services.ValidateDeviceName(request.DeviceName)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_device_name", err.Error())
		return
	}
	userID, ok, err := h.store.ConsumeRecoveryChallenge(r.Context(), request.Challenge, time.Now().UTC())
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_challenge", "That recovery challenge is invalid or expired.")
		return
	}
	user, found, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusNotFound, "not_found", "That identity no longer exists.")
		return
	}
	oldDevice, found, err := h.store.GetActiveDeviceForUser(r.Context(), userID)
	if err != nil {
		h.jsonServerError(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusConflict, "no_device", "That identity has no active device.")
		return
	}
	canonical := services.CanonicalRecovery(userID, deviceID, request.Challenge, request.Timestamp)
	if err := services.VerifySignature(oldDevice.SigPublicKey, request.Signature, canonical); err != nil {
		apiError(w, http.StatusForbidden, "invalid_proof", "The recovery proof could not be verified.")
		return
	}
	now := time.Now().UTC()
	device := models.Device{
		ID:           deviceID,
		UserID:       userID,
		Name:         deviceName,
		EncPublicKey: oldDevice.EncPublicKey,
		SigPublicKey: oldDevice.SigPublicKey,
		CreatedAt:    now,
		LastSeenAt:   now,
	}
	if err := h.store.RebindDevice(r.Context(), &device); err != nil {
		h.jsonServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, identityResponse{
		User:        user.Summary(),
		Device:      toDeviceView(device),
		Fingerprint: services.Fingerprint(device.EncPublicKey, device.SigPublicKey),
	})
}

func validateBackup(backup backupPayload) error {
	if backup.Ciphertext == "" || backup.IV == "" || backup.KDFSalt == "" {
		return errors.New("a recovery backup is required")
	}
	if _, err := services.DecodeBase64(backup.Ciphertext, backupCiphertextMax); err != nil {
		return errors.New("the recovery backup is invalid")
	}
	iv, err := services.DecodeBase64(backup.IV, backupIVBytes)
	if err != nil || len(iv) != backupIVBytes {
		return errors.New("the recovery backup IV is invalid")
	}
	salt, err := services.DecodeBase64(backup.KDFSalt, backupSaltBytes)
	if err != nil || len(salt) != backupSaltBytes {
		return errors.New("the recovery salt is invalid")
	}
	if backup.KDFIterations < minKDFIterations || backup.KDFIterations > maxKDFIterations {
		return errors.New("the recovery key derivation parameters are invalid")
	}
	return nil
}

func skew(timestamp int64) bool {
	if timestamp <= 0 {
		return true
	}
	value := time.UnixMilli(timestamp)
	delta := time.Since(value)
	return delta > proofSkew || delta < -proofSkew
}

func toDeviceView(device models.Device) deviceView {
	return deviceView{
		ID:         device.ID,
		Name:       device.Name,
		CreatedAt:  device.CreatedAt.UnixMilli(),
		LastSeenAt: device.LastSeenAt.UnixMilli(),
	}
}

func avatarURL(userID string, updatedAt int64) string {
	if updatedAt <= 0 {
		return ""
	}
	return "/avatar/" + userID + "?v=" + strings.TrimSpace(time.UnixMilli(updatedAt).Format("20060102150405"))
}
