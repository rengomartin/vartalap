package services

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileStore stores opaque encrypted attachment blobs and public avatar images
// on disk. Filenames are server-generated identifiers, never user input.
type FileStore struct {
	root string
}

// NewFileStore creates the storage directories.
func NewFileStore(root string) (*FileStore, error) {
	for _, dir := range []string{"attachments", "avatars"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			return nil, fmt.Errorf("create %s directory: %w", dir, err)
		}
	}
	return &FileStore{root: root}, nil
}

// SaveAttachment writes an encrypted blob atomically and returns its size.
func (f *FileStore) SaveAttachment(id string, reader io.Reader, maxBytes int64) (int64, error) {
	if !IsValidUUID(id) {
		return 0, fmt.Errorf("invalid attachment id")
	}
	dir := filepath.Join(f.root, "attachments")
	temp, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		return 0, err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()

	limited := io.LimitReader(reader, maxBytes+1)
	written, err := io.Copy(temp, limited)
	if err != nil {
		_ = temp.Close()
		return 0, err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return 0, err
	}
	if err := temp.Close(); err != nil {
		return 0, err
	}
	if written > maxBytes {
		return 0, fmt.Errorf("attachment is too large")
	}
	if written == 0 {
		return 0, fmt.Errorf("attachment is empty")
	}
	if err := os.Rename(tempName, f.attachmentPath(id)); err != nil {
		return 0, err
	}
	return written, nil
}

// OpenAttachment opens a stored blob.
func (f *FileStore) OpenAttachment(id string) (io.ReadCloser, int64, error) {
	if !IsValidUUID(id) {
		return nil, 0, os.ErrNotExist
	}
	return openFile(f.attachmentPath(id))
}

// DeleteAttachment removes a stored blob, ignoring missing files.
func (f *FileStore) DeleteAttachment(id string) error {
	if !IsValidUUID(id) {
		return nil
	}
	err := os.Remove(f.attachmentPath(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// SaveAvatar writes a validated image and records its content type.
func (f *FileStore) SaveAvatar(userID string, data []byte, contentType string) error {
	if !IsValidUUID(userID) {
		return fmt.Errorf("invalid user id")
	}
	dir := filepath.Join(f.root, "avatars")
	temp, err := os.CreateTemp(dir, "avatar-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, filepath.Join(dir, userID)); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, userID+".type"), []byte(contentType), 0o640)
}

// OpenAvatar opens a user's avatar and returns its content type.
func (f *FileStore) OpenAvatar(userID string) (io.ReadCloser, string, error) {
	if !IsValidUUID(userID) {
		return nil, "", os.ErrNotExist
	}
	contentType, err := os.ReadFile(filepath.Join(f.root, "avatars", userID+".type"))
	if err != nil {
		return nil, "", err
	}
	file, _, err := openFile(filepath.Join(f.root, "avatars", userID))
	if err != nil {
		return nil, "", err
	}
	return file, strings.TrimSpace(string(contentType)), nil
}

// DeleteAvatar removes an avatar and its content type sidecar.
func (f *FileStore) DeleteAvatar(userID string) {
	if !IsValidUUID(userID) {
		return
	}
	_ = os.Remove(filepath.Join(f.root, "avatars", userID))
	_ = os.Remove(filepath.Join(f.root, "avatars", userID+".type"))
}

func (f *FileStore) attachmentPath(id string) string {
	return filepath.Join(f.root, "attachments", id+".bin")
}

func openFile(path string) (io.ReadCloser, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}
