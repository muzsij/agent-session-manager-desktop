package session

import (
	"os"
	"testing"
)

func TestLoadTrackersWithNoFile(t *testing.T) {
	s := &Storage{configDir: t.TempDir()}
	list, err := s.LoadTrackers()
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Connections) != 0 || list.Bindings == nil {
		t.Fatalf("unexpected empty list %+v", list)
	}
}

func TestTrackerBindingsFollowTheirConnection(t *testing.T) {
	dir := t.TempDir()
	s := &Storage{configDir: dir}
	err := s.UpdateTrackers(func(list *TrackerList) error {
		list.Connections = append(list.Connections,
			TrackerConnection{ID: "a", Name: "A", Kind: "redmine", BaseURL: "https://a"},
			TrackerConnection{ID: "b", Name: "B", Kind: "redmine", BaseURL: "https://b"})
		list.Bindings[""] = TrackerBinding{ConnectionID: "a", Scope: "1"}
		list.Bindings["work"] = TrackerBinding{ConnectionID: "b"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Removing a connection drops the bindings that used it, and only those.
	err = s.UpdateTrackers(func(list *TrackerList) error {
		list.Connections = list.Connections[1:]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.LoadTrackers()
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := list.Bindings[""]; kept {
		t.Error("binding to a deleted connection survived")
	}
	if got := list.Bindings["work"]; got.ConnectionID != "b" {
		t.Errorf("unrelated binding lost: %+v", list.Bindings)
	}

	info, err := os.Stat(s.trackersPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("trackers.json mode = %v", info.Mode().Perm())
	}
}

func TestTrackerListRejectsDuplicateIDs(t *testing.T) {
	s := &Storage{configDir: t.TempDir()}
	err := s.UpdateTrackers(func(list *TrackerList) error {
		list.Connections = append(list.Connections,
			TrackerConnection{ID: "a", BaseURL: "https://a"},
			TrackerConnection{ID: "a", BaseURL: "https://b"})
		return nil
	})
	if err == nil {
		t.Fatal("duplicate ids saved")
	}
}

func TestSessionBindingOverridesTheProjects(t *testing.T) {
	s := &Storage{configDir: t.TempDir()}
	err := s.UpdateTrackers(func(list *TrackerList) error {
		list.Connections = append(list.Connections,
			TrackerConnection{ID: "a", BaseURL: "https://a"},
			TrackerConnection{ID: "b", BaseURL: "https://b"})
		list.Bindings["p"] = TrackerBinding{ConnectionID: "a"}
		list.SessionBindings[SessionBindingKey("p", "s1")] = TrackerBinding{ConnectionID: "b", Scope: "7"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.LoadTrackers()
	if err != nil {
		t.Fatal(err)
	}
	if got, source, ok := list.EffectiveBinding("p", "s1"); !ok || source != TrackerBindingFromSession || got.Scope != "7" {
		t.Errorf("s1 = %+v %q %v", got, source, ok)
	}
	if got, source, ok := list.EffectiveBinding("p", "s2"); !ok || source != TrackerBindingFromProject || got.ConnectionID != "a" {
		t.Errorf("s2 = %+v %q %v", got, source, ok)
	}
	// The same session ID in another project is another session.
	if _, _, ok := list.EffectiveBinding("q", "s1"); ok {
		t.Error("a session binding leaked into another project")
	}

	err = s.UpdateTrackers(func(list *TrackerList) error {
		list.Connections = list.Connections[:1]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	list, _ = s.LoadTrackers()
	if len(list.SessionBindings) != 0 {
		t.Errorf("session binding to a deleted connection survived: %+v", list.SessionBindings)
	}
}
