// Package cas implements a private content-addressed filesystem store for
// artifact bytes. Paths are derived only from verified content hashes; callers
// never supply filesystem locations.
package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	platformerrors "drift.local/drift-next/internal/platform/errors"
)

const hashPrefix = "sha256:"

// Store is a workspace-scoped content-addressed object store rooted under
// application data. It never follows caller-supplied absolute paths.
type Store struct {
	root string
}

// Open prepares a CAS root. root must be an absolute directory path owned by
// the control plane; relative and empty roots are rejected.
func Open(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "CAS root is required")
	}
	if !filepath.IsAbs(root) {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "CAS root must be absolute")
	}
	clean := filepath.Clean(root)
	if err := os.MkdirAll(filepath.Join(clean, "objects"), 0o700); err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInternal, "create CAS objects directory", err)
	}
	if err := os.MkdirAll(filepath.Join(clean, "tmp"), 0o700); err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInternal, "create CAS tmp directory", err)
	}
	return &Store{root: clean}, nil
}

// Root returns the absolute CAS root.
func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// Put writes payload atomically: temp -> verify hash/size -> rename.
// DeclaredHash may be empty; when set it must match the computed hash.
// Duplicate content is idempotent.
func (s *Store) Put(workspaceID, declaredHash string, payload []byte) (string, error) {
	if s == nil || s.root == "" {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "CAS store is required")
	}
	if strings.TrimSpace(workspaceID) == "" {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "workspace is required")
	}
	if payload == nil {
		payload = []byte{}
	}
	computed := HashBytes(payload)
	if declaredHash != "" && declaredHash != computed {
		return "", platformerrors.New(platformerrors.CodePreconditionFailed, "artifact content hash mismatch")
	}
	objectPath, err := s.objectPath(workspaceID, computed)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(objectPath); err == nil {
		if verifyErr := s.Verify(workspaceID, computed, int64(len(payload))); verifyErr != nil {
			return "", verifyErr
		}
		return computed, nil
	} else if !os.IsNotExist(err) {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "stat CAS object", err)
	}

	if err := os.MkdirAll(filepath.Dir(objectPath), 0o700); err != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "create CAS object prefix", err)
	}
	tmpDir := filepath.Join(s.root, "tmp", sanitizeSegment(workspaceID))
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "create CAS temp directory", err)
	}
	tmp, err := os.CreateTemp(tmpDir, "put-*")
	if err != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "create CAS temp file", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	written, writeErr := tmp.Write(payload)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if writeErr != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "write CAS temp file", writeErr)
	}
	if written != len(payload) {
		return "", platformerrors.New(platformerrors.CodeInternal, "CAS temp write truncated")
	}
	if syncErr != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "sync CAS temp file", syncErr)
	}
	if closeErr != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "close CAS temp file", closeErr)
	}

	info, err := os.Lstat(tmpName)
	if err != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "stat CAS temp file", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", platformerrors.New(platformerrors.CodePreconditionFailed, "CAS temp path must not be a symlink")
	}
	if info.Size() != int64(len(payload)) {
		return "", platformerrors.New(platformerrors.CodePreconditionFailed, "CAS temp size mismatch")
	}
	verified, err := HashFile(tmpName)
	if err != nil {
		return "", err
	}
	if verified != computed {
		return "", platformerrors.New(platformerrors.CodePreconditionFailed, "CAS temp hash mismatch")
	}

	if err := os.Rename(tmpName, objectPath); err != nil {
		if _, existsErr := os.Lstat(objectPath); existsErr == nil {
			if verifyErr := s.Verify(workspaceID, computed, int64(len(payload))); verifyErr == nil {
				return computed, nil
			}
		}
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "rename CAS object into place", err)
	}
	_ = os.Chmod(objectPath, 0o600)
	return computed, nil
}

// Get returns verified object bytes for a workspace content hash.
func (s *Store) Get(workspaceID, contentHash string) ([]byte, error) {
	path, err := s.objectPath(workspaceID, contentHash)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, platformerrors.New(platformerrors.CodeNotFound, "CAS object not found")
		}
		return nil, platformerrors.Wrap(platformerrors.CodeInternal, "stat CAS object", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, platformerrors.New(platformerrors.CodePreconditionFailed, "CAS object path must not be a symlink")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInternal, "read CAS object", err)
	}
	if HashBytes(payload) != contentHash {
		return nil, platformerrors.New(platformerrors.CodePreconditionFailed, "CAS object hash mismatch")
	}
	return payload, nil
}

// Delete removes an object. Missing objects are treated as already deleted.
func (s *Store) Delete(workspaceID, contentHash string) error {
	path, err := s.objectPath(workspaceID, contentHash)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return platformerrors.Wrap(platformerrors.CodeInternal, "stat CAS object for delete", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return platformerrors.New(platformerrors.CodePreconditionFailed, "refusing to delete CAS symlink")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return platformerrors.Wrap(platformerrors.CodeCleanupFailed, "delete CAS object", err)
	}
	return nil
}

// Verify checks that an object exists, is not a symlink, and matches size/hash.
func (s *Store) Verify(workspaceID, contentHash string, expectedSize int64) error {
	path, err := s.objectPath(workspaceID, contentHash)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return platformerrors.New(platformerrors.CodeNotFound, "CAS object not found")
		}
		return platformerrors.Wrap(platformerrors.CodeInternal, "stat CAS object", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return platformerrors.New(platformerrors.CodePreconditionFailed, "CAS object path must not be a symlink")
	}
	if expectedSize >= 0 && info.Size() != expectedSize {
		return platformerrors.New(platformerrors.CodePreconditionFailed, "CAS object size mismatch")
	}
	got, err := HashFile(path)
	if err != nil {
		return err
	}
	if got != contentHash {
		return platformerrors.New(platformerrors.CodePreconditionFailed, "CAS object hash mismatch")
	}
	return nil
}

// Exists reports whether a verified object path is present (symlink-safe).
func (s *Store) Exists(workspaceID, contentHash string) (bool, error) {
	path, err := s.objectPath(workspaceID, contentHash)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, platformerrors.Wrap(platformerrors.CodeInternal, "stat CAS object", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, platformerrors.New(platformerrors.CodePreconditionFailed, "CAS object path must not be a symlink")
	}
	return true, nil
}

func (s *Store) objectPath(workspaceID, contentHash string) (string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || strings.Contains(workspaceID, "..") || strings.ContainsAny(workspaceID, `/\`) {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "workspace path segment is invalid")
	}
	hexHash, err := normalizeHash(contentHash)
	if err != nil {
		return "", err
	}
	if len(hexHash) < 4 {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "content hash is too short")
	}
	prefix := hexHash[:2]
	path := filepath.Join(s.root, "objects", sanitizeSegment(workspaceID), prefix, hexHash)
	clean := filepath.Clean(path)
	rootWithSep := s.root + string(os.PathSeparator)
	if clean != s.root && !strings.HasPrefix(clean, rootWithSep) {
		return "", platformerrors.New(platformerrors.CodePreconditionFailed, "CAS path escapes store root")
	}
	return clean, nil
}

func normalizeHash(contentHash string) (string, error) {
	contentHash = strings.TrimSpace(contentHash)
	if contentHash == "" {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "content hash is required")
	}
	if strings.Contains(contentHash, "..") || strings.ContainsAny(contentHash, `/\`) {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "content hash path characters are rejected")
	}
	if !strings.HasPrefix(contentHash, hashPrefix) {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "content hash must use sha256: prefix")
	}
	hexPart := strings.TrimPrefix(contentHash, hashPrefix)
	if len(hexPart) != 64 {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "content hash digest length is invalid")
	}
	for _, r := range hexPart {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", platformerrors.New(platformerrors.CodeInvalidInput, "content hash digest must be lowercase hex")
		}
	}
	return hexPart, nil
}

func sanitizeSegment(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, value)
}

// HashBytes returns the canonical sha256:<hex> digest for payload.
func HashBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hashPrefix + hex.EncodeToString(sum[:])
}

// HashFile hashes a regular file without following symlinks via Open.
func HashFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "stat file for hash", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", platformerrors.New(platformerrors.CodePreconditionFailed, "refusing to hash symlink")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "open file for hash", err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "hash file", err)
	}
	return hashPrefix + hex.EncodeToString(hasher.Sum(nil)), nil
}

// ObjectRelPath returns the relative CAS object path for diagnostics (never for
// authorization). It reuses the same validation as objectPath.
func (s *Store) ObjectRelPath(workspaceID, contentHash string) (string, error) {
	abs, err := s.objectPath(workspaceID, contentHash)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(s.root, abs)
	if err != nil {
		return "", fmt.Errorf("relative CAS path: %w", err)
	}
	return rel, nil
}
