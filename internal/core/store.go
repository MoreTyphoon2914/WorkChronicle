package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"worktracker/internal/browsercontext"
	"worktracker/internal/coreprotocol"
)

type persistedState struct {
	SchemaVersion int                                     `json:"schema_version"`
	LastIngest    time.Time                               `json:"last_ingest"`
	Windows       []coreprotocol.WindowObservation        `json:"windows"`
	AFK           []coreprotocol.AFKObservation           `json:"afk"`
	StoredContext []coreprotocol.StoredContextObservation `json:"stored_context"`
	HostContext   []coreprotocol.HostContextObservation   `json:"host_context"`
	Browser       []browsercontext.Observation            `json:"browser"`
}

type Store struct {
	mu        sync.RWMutex
	path      string
	retention time.Duration
	state     persistedState
}

func OpenStore(dataDir string, retention time.Duration) (*Store, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	if retention <= 0 {
		return nil, fmt.Errorf("retention must be positive")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	s := &Store{path: filepath.Join(dataDir, "state.json"), retention: retention}
	s.state.SchemaVersion = 1
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read persisted state: %w", err)
	}
	if err := json.Unmarshal(b, &s.state); err != nil {
		return nil, fmt.Errorf("decode persisted state: %w", err)
	}
	if s.state.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported persisted state schema %d", s.state.SchemaVersion)
	}
	return s, nil
}

func (s *Store) Ingest(batch coreprotocol.Batch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Windows = mergeWindows(s.state.Windows, batch.Windows)
	s.state.AFK = mergeAFK(s.state.AFK, batch.AFK)
	s.state.StoredContext = mergeStoredContext(s.state.StoredContext, batch.StoredContext)
	s.state.HostContext = mergeHostContext(s.state.HostContext, batch.HostContext)
	s.state.Browser = mergeBrowser(s.state.Browser, batch.Browser)
	if batch.SentAt.After(s.state.LastIngest) {
		s.state.LastIngest = batch.SentAt
	}
	s.pruneLocked(batch.SentAt.Add(-s.retention))
	return s.persistLocked()
}

func (s *Store) Snapshot() persistedState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copyState := s.state
	copyState.Windows = append([]coreprotocol.WindowObservation(nil), s.state.Windows...)
	copyState.AFK = append([]coreprotocol.AFKObservation(nil), s.state.AFK...)
	copyState.StoredContext = append([]coreprotocol.StoredContextObservation(nil), s.state.StoredContext...)
	copyState.HostContext = append([]coreprotocol.HostContextObservation(nil), s.state.HostContext...)
	copyState.Browser = append([]browsercontext.Observation(nil), s.state.Browser...)
	return copyState
}

func (s *Store) pruneLocked(cutoff time.Time) {
	s.state.Windows = filter(s.state.Windows, func(v coreprotocol.WindowObservation) bool { return !v.End.Before(cutoff) })
	s.state.AFK = filter(s.state.AFK, func(v coreprotocol.AFKObservation) bool { return !v.End.Before(cutoff) })
	s.state.StoredContext = filter(s.state.StoredContext, func(v coreprotocol.StoredContextObservation) bool { return !v.End.Before(cutoff) })
	s.state.HostContext = filter(s.state.HostContext, func(v coreprotocol.HostContextObservation) bool { return !v.End.Before(cutoff) })
	s.state.Browser = filter(s.state.Browser, func(v browsercontext.Observation) bool { return !v.ObservedAt.Before(cutoff) })
}

func (s *Store) persistLocked() error {
	b, err := json.Marshal(s.state)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

func mergeWindows(existing, incoming []coreprotocol.WindowObservation) []coreprotocol.WindowObservation {
	return merge(existing, incoming,
		func(v coreprotocol.WindowObservation) string {
			return v.Start.UTC().Format(time.RFC3339Nano) + "\x00" + v.Executable + "\x00" + v.Title
		},
		func(a, b coreprotocol.WindowObservation) coreprotocol.WindowObservation {
			if b.End.After(a.End) {
				return b
			}
			return a
		},
		func(a, b coreprotocol.WindowObservation) bool { return a.Start.Before(b.Start) })
}

func mergeAFK(existing, incoming []coreprotocol.AFKObservation) []coreprotocol.AFKObservation {
	return merge(existing, incoming,
		func(v coreprotocol.AFKObservation) string {
			return v.Start.UTC().Format(time.RFC3339Nano) + "\x00" + v.Status
		},
		func(a, b coreprotocol.AFKObservation) coreprotocol.AFKObservation {
			if b.End.After(a.End) {
				return b
			}
			return a
		},
		func(a, b coreprotocol.AFKObservation) bool { return a.Start.Before(b.Start) })
}

func mergeStoredContext(existing, incoming []coreprotocol.StoredContextObservation) []coreprotocol.StoredContextObservation {
	return merge(existing, incoming,
		func(v coreprotocol.StoredContextObservation) string { return v.Start.UTC().Format(time.RFC3339Nano) },
		func(a, b coreprotocol.StoredContextObservation) coreprotocol.StoredContextObservation {
			if b.End.After(a.End) {
				return b
			}
			return a
		},
		func(a, b coreprotocol.StoredContextObservation) bool { return a.Start.Before(b.Start) })
}

func mergeHostContext(existing, incoming []coreprotocol.HostContextObservation) []coreprotocol.HostContextObservation {
	return merge(existing, incoming,
		func(v coreprotocol.HostContextObservation) string { return v.Start.UTC().Format(time.RFC3339Nano) },
		func(_ coreprotocol.HostContextObservation, b coreprotocol.HostContextObservation) coreprotocol.HostContextObservation {
			return b
		},
		func(a, b coreprotocol.HostContextObservation) bool { return a.Start.Before(b.Start) })
}

func mergeBrowser(existing, incoming []browsercontext.Observation) []browsercontext.Observation {
	return merge(existing, incoming,
		func(v browsercontext.Observation) string {
			return v.ObservedAt.UTC().Format(time.RFC3339Nano) + "\x00" + v.SourceID()
		},
		func(_ browsercontext.Observation, b browsercontext.Observation) browsercontext.Observation { return b },
		func(a, b browsercontext.Observation) bool { return a.ObservedAt.Before(b.ObservedAt) })
}

func merge[T any](existing, incoming []T, key func(T) string, choose func(T, T) T, less func(T, T) bool) []T {
	items := make(map[string]T, len(existing)+len(incoming))
	for _, item := range existing {
		items[key(item)] = item
	}
	for _, item := range incoming {
		k := key(item)
		if current, ok := items[k]; ok {
			items[k] = choose(current, item)
		} else {
			items[k] = item
		}
	}
	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func filter[T any](items []T, keep func(T) bool) []T {
	out := items[:0]
	for _, item := range items {
		if keep(item) {
			out = append(out, item)
		}
	}
	return out
}
