package steward

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileKeyStore records service-client private keys as one PEM file per service
// under a single directory. It is both the KeySink reconcile writes through and
// the read side an Applier uses to decide whether a client's keypair already
// exists — the two halves of "mint once, reuse forever".
//
// It lives here rather than in a transport package because BOTH appliers need
// it: the setup-window applier (auth's SetupService) and the regular-surface
// applier (auth's permission-verified management RPCs) must agree on where a
// service's key lives, or a client provisioned by one path would be
// unverifiable by the other.
type FileKeyStore struct {
	dir string
}

func NewFileKeyStore(dir string) *FileKeyStore { return &FileKeyStore{dir: dir} }

// PrivateKey returns the recorded PEM for a service, and whether one existed.
// A missing file is not an error — it is the first-run signal to mint one.
func (s *FileKeyStore) PrivateKey(service string) (string, bool, error) {
	path, err := s.path(service)
	if err != nil {
		return "", false, err
	}
	b, err := os.ReadFile(path) // #nosec G304 -- path is built from a validated service name under the configured key dir
	if err == nil {
		return string(b), true, nil
	}
	if os.IsNotExist(err) {
		return "", false, nil
	}
	return "", false, fmt.Errorf("read steward key for %q: %w", service, err)
}

// Record writes a freshly generated key. It is create-exclusive: an existing
// file is left untouched, so a concurrent or repeated run can never overwrite a
// live credential.
func (s *FileKeyStore) Record(_ context.Context, service string, privateKeyPEM string) error {
	path, err := s.path(service)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- see PrivateKey
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(privateKeyPEM)
	return err
}

// Discard removes a key this run just minted but could not register — the
// rollback for "auth already holds a client under this name, so the key we just
// generated is not the one that authenticates it". Keeping the orphan would make
// the next run believe a matching key exists.
func (s *FileKeyStore) Discard(service string) error {
	path, err := s.path(service)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *FileKeyStore) path(service string) (string, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return "", fmt.Errorf("STEWARD_KEY_DIR is required to record generated service client keys")
	}
	name := strings.TrimSpace(service)
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, string(os.PathSeparator)) || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid steward service name %q", service)
	}
	return filepath.Join(s.dir, name+".pem"), nil
}
