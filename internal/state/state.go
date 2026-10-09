// Package state remembers small bits of kboba's own state between runs,
// such as the last namespace used in each context. It lives in kboba's
// config directory and never touches the kubeconfig.
package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"sigs.k8s.io/yaml"
)

// file is the on-disk format.
type file struct {
	// Namespaces maps a context name to the last namespace used in it.
	// "" means "all namespaces"; a missing key means "nothing remembered".
	Namespaces map[string]string `json:"namespaces,omitempty"`
}

// Store is the remembered state. It is safe for concurrent use: the UI
// updates it in memory, and Save may run in a background goroutine.
type Store struct {
	path string
	mu   sync.Mutex
	data file
}

// DefaultPath is $XDG_CONFIG_HOME/kboba/state.yaml (or the platform's
// equivalent, e.g. ~/.config on Linux).
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kboba", "state.yaml"), nil
}

// Open loads the state at path. A missing file is not an error. If the
// file can't be read or parsed, Open still returns a usable empty Store
// along with the error, so a broken state file never prevents kboba from
// starting (the next Save replaces it).
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, nil
	case err != nil:
		return s, fmt.Errorf("read state: %w", err)
	}
	if err := yaml.Unmarshal(raw, &s.data); err != nil {
		s.data = file{}
		return s, fmt.Errorf("parse state %s: %w", path, err)
	}
	return s, nil
}

// LastNamespace returns the namespace last used in context, if any.
func (s *Store) LastNamespace(context string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, ok := s.data.Namespaces[context]
	return ns, ok
}

// SetLastNamespace remembers namespace for context, in memory only; call
// Save to persist.
func (s *Store) SetLastNamespace(context, namespace string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Namespaces == nil {
		s.data.Namespaces = map[string]string{}
	}
	s.data.Namespaces[context] = namespace
}

// Save writes the current state to disk. It writes a temporary file and
// renames it over the old one, so a crash mid-write can't leave a
// half-written file behind.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	out, err := yaml.Marshal(s.data)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
