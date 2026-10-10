// Package statefs keeps stack records in the state directory: a public JSON
// record per stack for other tools, a private runtime file beside it, and a
// lock file for allocation.
package statefs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

const (
	publicExt  = ".json"
	privateExt = ".env.json"
)

// Store implements app.StateStore.
type Store struct {
	dir string
}

var _ app.StateStore = (*Store)(nil)

// New returns a store rooted at dir, creating it if needed.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "stacks"), 0o700); err != nil {
		return nil, fmt.Errorf("creating state directory %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Dir is the state directory.
func (s *Store) Dir() string { return s.dir }

// Lock takes an exclusive flock on the lock file, polling so ctx can cancel
// the wait.
func (s *Store) Lock(ctx context.Context) (func(), error) {
	path := filepath.Join(s.dir, "lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // fixed name in the state dir
	if err != nil {
		return nil, fmt.Errorf("opening lock %s: %w", path, err)
	}
	fd := int(f.Fd())
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("waiting for lock %s, another svx may be stuck: %w", path, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	return func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func (s *Store) path(id domain.Identity, ext string) string {
	return filepath.Join(s.dir, "stacks", id.Key()+ext)
}

// Load reads a stack with its runtime fields.
func (s *Store) Load(id domain.Identity) (domain.Stack, bool, error) {
	var rec record
	found, err := readJSON(s.path(id, publicExt), &rec)
	if err != nil || !found {
		return domain.Stack{}, false, err
	}
	var rt runtime
	if _, err := readJSON(s.path(id, privateExt), &rt); err != nil {
		return domain.Stack{}, false, err
	}
	return fromFiles(rec, &rt), true, nil
}

// Save writes both files, the private one first so a reader that finds the
// public record can always find its runtime.
func (s *Store) Save(stack domain.Stack) error {
	if err := writeJSON(s.path(stack.ID, privateExt), toRuntime(stack), 0o600); err != nil {
		return err
	}
	return writeJSON(s.path(stack.ID, publicExt), toRecord(stack), 0o644)
}

// List reads every public record, sorted by key.
func (s *Store) List() ([]domain.Stack, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, "stacks"))
	if err != nil {
		return nil, fmt.Errorf("reading state directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, publicExt) && !strings.HasSuffix(n, privateExt) {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	stacks := make([]domain.Stack, 0, len(names))
	for _, n := range names {
		var rec record
		found, err := readJSON(filepath.Join(s.dir, "stacks", n), &rec)
		if err != nil {
			return nil, err
		}
		if found {
			stacks = append(stacks, fromFiles(rec, nil))
		}
	}
	return stacks, nil
}

// SetStatus rewrites a stack's status in both files.
func (s *Store) SetStatus(id domain.Identity, status domain.StackStatus) error {
	stack, found, err := s.Load(id)
	if err != nil || !found {
		return err
	}
	stack.Status = status
	return s.Save(stack)
}

func readJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // paths are built from the state dir
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("parsing %s: %w; delete it to reset that stack", path, err)
	}
	return true, nil
}

// writeJSON writes to a temporary file and renames it into place, so readers
// never see half a file.
func writeJSON(path string, v any, mode os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
