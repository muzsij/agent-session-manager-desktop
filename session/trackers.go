package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

// External issue trackers: the connections (a Redmine instance and the key to
// it) and which project reads its work from which of them.
//
// Kept beside servers.json, out of Settings, for the same reasons: Settings is
// per project and copied into every backup, and a connection is something the
// user has rather than a property of one project — two projects can use the
// same Redmine, and one project's binding names a connection by ID. The API
// key is not in this file at all; it is in the system keyring.

const (
	trackerListMaxBytes = 1 << 20
	trackerListMaxItems = 100
)

// TrackerConnection is one tracker the app can read from.
type TrackerConnection struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
}

// TrackerBinding ties a project to a connection. Scope narrows it to part of
// the tracker — a Redmine project — and is empty for "everything assigned to
// me there".
type TrackerBinding struct {
	ConnectionID string `json:"connection_id"`
	Scope        string `json:"scope,omitempty"`
	// ScopeName is what Scope was called when it was chosen, so the panel can
	// say what it shows without asking the tracker first.
	ScopeName string `json:"scope_name,omitempty"`
}

// TrackerList is the stored file.
type TrackerList struct {
	Connections []TrackerConnection `json:"connections"`
	// Bindings is keyed by project ID; the default project is "".
	Bindings map[string]TrackerBinding `json:"bindings,omitempty"`
	// SessionBindings override the project's binding for one session — a
	// session that works on one Redmine project of several. Keyed by
	// SessionBindingKey, since session IDs are only unique within a project.
	SessionBindings map[string]TrackerBinding `json:"session_bindings,omitempty"`
}

// SessionBindingKey is the SessionBindings key of a session.
func SessionBindingKey(projectID, sessionID string) string {
	return projectID + "/" + sessionID
}

// Binding sources, as EffectiveBinding reports them.
const (
	TrackerBindingFromSession = "session"
	TrackerBindingFromProject = "project"
)

// EffectiveBinding is the binding a session reads from: its own when it has
// one, the project's otherwise. An empty sessionID asks for the project's.
func (list *TrackerList) EffectiveBinding(projectID, sessionID string) (TrackerBinding, string, bool) {
	if sessionID != "" {
		if binding, ok := list.SessionBindings[SessionBindingKey(projectID, sessionID)]; ok {
			return binding, TrackerBindingFromSession, true
		}
	}
	binding, ok := list.Bindings[projectID]
	return binding, TrackerBindingFromProject, ok
}

// FindConnection returns the connection with this ID, or nil.
func (list *TrackerList) FindConnection(id string) *TrackerConnection {
	for index := range list.Connections {
		if list.Connections[index].ID == id {
			return &list.Connections[index]
		}
	}
	return nil
}

func (s *Storage) trackersPath() string {
	return filepath.Join(s.configDir, "trackers.json")
}

func (s *Storage) trackersLockPath() string {
	return filepath.Join(s.configDir, "trackers.lock")
}

// LoadTrackers returns the tracker list, or an empty one when nothing is saved.
func (s *Storage) LoadTrackers() (*TrackerList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadTrackersLocked()
}

func (s *Storage) loadTrackersLocked() (*TrackerList, error) {
	data, err := readFileAtMost(s.trackersPath(), trackerListMaxBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return &TrackerList{Bindings: map[string]TrackerBinding{}, SessionBindings: map[string]TrackerBinding{}}, nil
		}
		return nil, err
	}
	var list TrackerList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("the tracker list is unreadable: %w", err)
	}
	if list.Bindings == nil {
		list.Bindings = map[string]TrackerBinding{}
	}
	if list.SessionBindings == nil {
		list.SessionBindings = map[string]TrackerBinding{}
	}
	if err := validateTrackerList(&list); err != nil {
		return nil, fmt.Errorf("the tracker list is invalid: %w", err)
	}
	return &list, nil
}

func validateTrackerList(list *TrackerList) error {
	if len(list.Connections) > trackerListMaxItems {
		return fmt.Errorf("tracker list exceeds the item limit")
	}
	seen := make(map[string]struct{}, len(list.Connections))
	for _, conn := range list.Connections {
		if strings.TrimSpace(conn.ID) == "" {
			return fmt.Errorf("tracker connection has no id")
		}
		if strings.TrimSpace(conn.BaseURL) == "" {
			return fmt.Errorf("tracker connection %q has no address", conn.Name)
		}
		if _, duplicate := seen[conn.ID]; duplicate {
			return fmt.Errorf("duplicate tracker connection id %q", conn.ID)
		}
		seen[conn.ID] = struct{}{}
	}
	return nil
}

// UpdateTrackers runs a complete load/change/save cycle under the lock, so
// two edits arriving together cannot each save a copy of the same older list.
func (s *Storage) UpdateTrackers(change func(*TrackerList) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withCrossProcessFileLock(s.trackersLockPath(), func() error {
		list, err := s.loadTrackersLocked()
		if err != nil {
			return err
		}
		if err := change(list); err != nil {
			return err
		}
		// A binding to a connection that is gone would only produce an error
		// in the panel; dropping it shows the built-in list again instead.
		for _, bindings := range []map[string]TrackerBinding{list.Bindings, list.SessionBindings} {
			for key, binding := range bindings {
				if list.FindConnection(binding.ConnectionID) == nil {
					delete(bindings, key)
				}
			}
		}
		if err := validateTrackerList(list); err != nil {
			return err
		}
		data, err := json.MarshalIndent(list, "", "  ")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(s.configDir, 0o700); err != nil {
			return err
		}
		return writeFileAtomic(s.trackersPath(), data, 0600)
	})
}

// Tracker API keys live in the system keyring, under the same service as the
// server passwords and in a namespace of their own. When the keyring cannot
// be reached they are held in memory for the run, as server passwords are.

func trackerKeyringAccount(connectionID string) string {
	return "tracker:" + connectionID
}

// StoreTrackerToken saves a connection's API key, falling back to memory for
// this run when the keyring refuses it. The error says which happened.
func StoreTrackerToken(connectionID, token string) error {
	if strings.TrimSpace(connectionID) == "" {
		return fmt.Errorf("cannot store a token without a connection id")
	}
	if err := keyring.Set(keyringService, trackerKeyringAccount(connectionID), token); err != nil {
		sessionPasswords.Store(trackerKeyringAccount(connectionID), token)
		return fmt.Errorf("the system keyring refused the token: %w", err)
	}
	sessionPasswords.Delete(trackerKeyringAccount(connectionID))
	return nil
}

// LookupTrackerToken returns a connection's API key and whether one was found.
func LookupTrackerToken(connectionID string) (string, bool) {
	if strings.TrimSpace(connectionID) == "" {
		return "", false
	}
	account := trackerKeyringAccount(connectionID)
	if secret, err := keyring.Get(keyringService, account); err == nil && secret != "" {
		return secret, true
	}
	if held, ok := sessionPasswords.Load(account); ok {
		if secret, isString := held.(string); isString && secret != "" {
			return secret, true
		}
	}
	return "", false
}

// ForgetTrackerToken removes a connection's API key from both places.
func ForgetTrackerToken(connectionID string) {
	if strings.TrimSpace(connectionID) == "" {
		return
	}
	account := trackerKeyringAccount(connectionID)
	sessionPasswords.Delete(account)
	_ = keyring.Delete(keyringService, account)
}
