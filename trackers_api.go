package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"asmgr-desktop/session"
	"asmgr-desktop/tracker"
)

// Frontend API for external issue trackers (see the tracker package).

const trackerCallTimeout = 45 * time.Second

// TrackerConnectionInfo is one connection as the frontend sees it. HasToken
// rather than the token: the key lives in the keyring and never reaches the UI.
type TrackerConnectionInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	BaseURL  string `json:"baseUrl"`
	HasToken bool   `json:"hasToken"`
}

// TrackerConnectionRequest is what the editor sends back. An empty Token
// leaves the stored one alone, so renaming a connection keeps its key.
type TrackerConnectionRequest struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BaseURL string `json:"baseUrl"`
	Token   string `json:"token"`
}

// TrackerBindingInfo is the binding a session or project reads from,
// resolved for display.
type TrackerBindingInfo struct {
	ConnectionID   string               `json:"connectionId"`
	ConnectionName string               `json:"connectionName"`
	Kind           string               `json:"kind"`
	Scope          string               `json:"scope"`
	ScopeName      string               `json:"scopeName"`
	Capabilities   tracker.Capabilities `json:"capabilities"`
	// Source is "session" when the session has a binding of its own,
	// "project" when it uses the project's.
	Source string `json:"source"`
	// ProjectBound and ProjectScopeName describe the project's binding, for
	// the "project default" choice a session can go back to.
	ProjectBound     bool   `json:"projectBound"`
	ProjectScopeName string `json:"projectScopeName"`
}

func trackerCallContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), trackerCallTimeout)
}

func connectionToInfo(conn *session.TrackerConnection) TrackerConnectionInfo {
	_, hasToken := session.LookupTrackerToken(conn.ID)
	return TrackerConnectionInfo{
		ID:       conn.ID,
		Name:     conn.Name,
		Kind:     conn.Kind,
		BaseURL:  conn.BaseURL,
		HasToken: hasToken,
	}
}

// GetTrackerConnections returns the configured tracker connections.
func (a *App) GetTrackerConnections() ([]TrackerConnectionInfo, error) {
	list, err := a.storage.LoadTrackers()
	if err != nil {
		return nil, err
	}
	out := make([]TrackerConnectionInfo, 0, len(list.Connections))
	for index := range list.Connections {
		out = append(out, connectionToInfo(&list.Connections[index]))
	}
	return out, nil
}

// SaveTrackerConnection creates or updates one connection and returns the
// saved list.
func (a *App) SaveTrackerConnection(req TrackerConnectionRequest) ([]TrackerConnectionInfo, error) {
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = tracker.KindRedmine
	}
	// Validated through the provider itself, so an address it cannot use is
	// refused here rather than in the panel later. A placeholder token stands
	// in when the stored one is kept.
	probeToken := req.Token
	if probeToken == "" {
		probeToken = "-"
	}
	if _, err := tracker.New(tracker.Connection{Kind: kind, BaseURL: req.BaseURL, Token: probeToken}); err != nil {
		return nil, err
	}

	id := strings.TrimSpace(req.ID)
	if id == "" {
		if req.Token == "" {
			return nil, fmt.Errorf("error.trackerNoToken")
		}
		id = uuid.New().String()
	}
	err := a.storage.UpdateTrackers(func(list *session.TrackerList) error {
		entry := session.TrackerConnection{
			ID:      id,
			Name:    strings.TrimSpace(req.Name),
			Kind:    kind,
			BaseURL: strings.TrimSpace(req.BaseURL),
		}
		if existing := list.FindConnection(id); existing != nil {
			*existing = entry
			return nil
		}
		list.Connections = append(list.Connections, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if req.Token != "" {
		if err := session.StoreTrackerToken(id, req.Token); err != nil {
			// Held in memory for this run instead; worth a log line, not a
			// failed save.
			log.Printf("[Tracker] %v", err)
		}
	}
	return a.GetTrackerConnections()
}

// DeleteTrackerConnection removes a connection, its key, and every project
// binding that used it.
func (a *App) DeleteTrackerConnection(id string) ([]TrackerConnectionInfo, error) {
	err := a.storage.UpdateTrackers(func(list *session.TrackerList) error {
		kept := list.Connections[:0]
		for _, conn := range list.Connections {
			if conn.ID != id {
				kept = append(kept, conn)
			}
		}
		list.Connections = kept
		return nil
	})
	if err != nil {
		return nil, err
	}
	session.ForgetTrackerToken(id)
	return a.GetTrackerConnections()
}

// TestTrackerConnection checks an address and key — the typed key when one is
// given, the stored one otherwise — and returns whose they are.
func (a *App) TestTrackerConnection(req TrackerConnectionRequest) (string, error) {
	token := req.Token
	if token == "" {
		token, _ = session.LookupTrackerToken(req.ID)
	}
	kind := req.Kind
	if kind == "" {
		kind = tracker.KindRedmine
	}
	provider, err := tracker.New(tracker.Connection{Kind: kind, BaseURL: req.BaseURL, Token: token})
	if err != nil {
		return "", err
	}
	ctx, cancel := trackerCallContext()
	defer cancel()
	return provider.Check(ctx)
}

// TrackerListScopes returns what a project can be bound to on a connection.
func (a *App) TrackerListScopes(connectionID string) ([]tracker.Scope, error) {
	list, err := a.storage.LoadTrackers()
	if err != nil {
		return nil, err
	}
	provider, _, err := providerFor(list, connectionID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := trackerCallContext()
	defer cancel()
	return provider.ListScopes(ctx)
}

func providerFor(list *session.TrackerList, connectionID string) (tracker.Provider, *session.TrackerConnection, error) {
	conn := list.FindConnection(connectionID)
	if conn == nil {
		return nil, nil, fmt.Errorf("error.trackerNoConnection")
	}
	token, ok := session.LookupTrackerToken(conn.ID)
	if !ok {
		return nil, nil, fmt.Errorf("error.trackerNoToken")
	}
	provider, err := tracker.New(tracker.Connection{Kind: conn.Kind, BaseURL: conn.BaseURL, Token: token})
	if err != nil {
		return nil, nil, err
	}
	return provider, conn, nil
}

// boundProvider resolves the binding a session reads from — its own, or the
// project's — to a provider. An empty sessionID asks for the project's. No
// binding at all returns a nil binding and no error.
func (a *App) boundProvider(projectID, sessionID string) (tracker.Provider, *session.TrackerBinding, error) {
	list, err := a.storage.LoadTrackers()
	if err != nil {
		return nil, nil, err
	}
	binding, _, ok := list.EffectiveBinding(projectID, sessionID)
	if !ok {
		return nil, nil, nil
	}
	provider, _, err := providerFor(list, binding.ConnectionID)
	if err != nil {
		return nil, &binding, err
	}
	return provider, &binding, nil
}

// GetTrackerBinding returns the binding a session reads from, or the
// project's when sessionID is empty; nil when neither has one and the built-in
// task list is used.
func (a *App) GetTrackerBinding(projectID, sessionID string) (*TrackerBindingInfo, error) {
	list, err := a.storage.LoadTrackers()
	if err != nil {
		return nil, err
	}
	binding, source, ok := list.EffectiveBinding(projectID, sessionID)
	if !ok {
		return nil, nil
	}
	conn := list.FindConnection(binding.ConnectionID)
	if conn == nil {
		return nil, nil
	}
	info := &TrackerBindingInfo{
		ConnectionID:   conn.ID,
		ConnectionName: conn.Name,
		Kind:           conn.Kind,
		Scope:          binding.Scope,
		ScopeName:      binding.ScopeName,
		Source:         source,
	}
	if projectBinding, bound := list.Bindings[projectID]; bound {
		info.ProjectBound = true
		info.ProjectScopeName = projectBinding.ScopeName
	}
	if token, ok := session.LookupTrackerToken(conn.ID); ok {
		if provider, err := tracker.New(tracker.Connection{Kind: conn.Kind, BaseURL: conn.BaseURL, Token: token}); err == nil {
			info.Capabilities = tracker.CapabilitiesOf(provider)
		}
	}
	return info, nil
}

// SetTrackerBinding binds the active project to a connection, or unbinds it
// when connectionID is empty.
func (a *App) SetTrackerBinding(connectionID, scope, scopeName, expectedProjectID string) error {
	return a.setTrackerBinding(expectedProjectID, "", connectionID, scope, scopeName)
}

// SetSessionTrackerBinding gives one session of the active project a binding
// of its own, or — with an empty connectionID — returns it to the project's.
func (a *App) SetSessionTrackerBinding(sessionID, connectionID, scope, scopeName, expectedProjectID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("error.sessionNotFound")
	}
	return a.setTrackerBinding(expectedProjectID, sessionID, connectionID, scope, scopeName)
}

func (a *App) setTrackerBinding(projectID, sessionID, connectionID, scope, scopeName string) error {
	done, err := a.beginExpectedProjectMutation(projectID)
	if err != nil {
		return err
	}
	defer done()
	return a.storage.UpdateTrackers(func(list *session.TrackerList) error {
		bindings, key := list.Bindings, projectID
		if sessionID != "" {
			bindings, key = list.SessionBindings, session.SessionBindingKey(projectID, sessionID)
		}
		if strings.TrimSpace(connectionID) == "" {
			delete(bindings, key)
			return nil
		}
		if list.FindConnection(connectionID) == nil {
			return fmt.Errorf("error.trackerNoConnection")
		}
		bindings[key] = session.TrackerBinding{
			ConnectionID: connectionID,
			Scope:        strings.TrimSpace(scope),
			ScopeName:    strings.TrimSpace(scopeName),
		}
		return nil
	})
}

// TrackerListIssues returns the open issues assigned to the user in the
// tracker the session reads from.
func (a *App) TrackerListIssues(projectID, sessionID string) ([]tracker.Issue, error) {
	provider, binding, err := a.boundProvider(projectID, sessionID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, fmt.Errorf("error.trackerNotBound")
	}
	ctx, cancel := trackerCallContext()
	defer cancel()
	return provider.ListAssignedToMe(ctx, binding.Scope)
}

// TrackerGetIssue returns one issue from the tracker the session reads from.
func (a *App) TrackerGetIssue(projectID, sessionID, issueID string) (*tracker.Issue, error) {
	provider, binding, err := a.boundProvider(projectID, sessionID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, fmt.Errorf("error.trackerNotBound")
	}
	ctx, cancel := trackerCallContext()
	defer cancel()
	return provider.GetIssue(ctx, issueID)
}

// TrackerSendIssueToAgent types an issue into a session's tab — the active
// one when tabID is empty.
//
// The issue is fetched fresh rather than taken from the list the panel shows,
// so the agent gets the description as it stands now, and fetched before the
// project lock is taken, so a slow tracker does not hold up every other edit.
func (a *App) TrackerSendIssueToAgent(sessionID, issueID, tabID, expectedProjectID string) error {
	issue, err := a.TrackerGetIssue(expectedProjectID, sessionID, issueID)
	if err != nil {
		return err
	}
	done, err := a.beginExpectedProjectMutation(expectedProjectID)
	if err != nil {
		return err
	}
	defer done()
	inst, err := a.storage.GetInstance(sessionID)
	if err != nil {
		return err
	}
	log.Printf("[Tracker] SendToAgent issue=%s session=%s tab=%q", issue.Ref, sessionID, tabID)
	return inst.SendTaskToAgent(tracker.FormatPrompt(issue), tabID)
}
