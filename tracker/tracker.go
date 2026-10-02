// Package tracker reads work items from external issue trackers — Redmine
// today, others later — through one small model.
//
// The model is deliberately narrower than any one tracker. Trackers disagree
// about almost everything (statuses are global in Redmine and per list in
// ClickUp; hierarchies differ; IDs differ), so the shared part is only what a
// list, a detail view and a prompt for an agent need. Whatever a provider can
// do beyond reading is offered through the optional interfaces below, which a
// caller discovers with a type assertion — a provider that cannot set a status
// simply does not implement StatusWriter, and the UI hides the action.
package tracker

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Kinds of tracker a connection can point at.
const (
	KindRedmine = "redmine"
)

// Status is an issue's state as the tracker names it. Closed is the one
// property every tracker can answer, and the only one the shared code relies
// on: "open" means not closed, whatever the tracker calls it.
type Status struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Closed bool   `json:"closed"`
}

// Issue is one work item.
type Issue struct {
	// ID is what the provider is asked about the issue with.
	ID string `json:"id"`
	// Ref is how the issue is written by people: "#1234" in Redmine.
	Ref         string     `json:"ref"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      Status     `json:"status"`
	Priority    string     `json:"priority"`
	Tracker     string     `json:"tracker,omitempty"`
	Project     string     `json:"project,omitempty"`
	URL         string     `json:"url"`
	ParentID    string     `json:"parentId,omitempty"`
	DueAt       *time.Time `json:"dueAt,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// Scope is something a project can be bound to: a Redmine project, a ClickUp
// list. Its ID is opaque to everything but the provider that produced it.
type Scope struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Parent names the enclosing scope, for display only.
	Parent string `json:"parent,omitempty"`
}

// Provider is what every tracker supports.
type Provider interface {
	// ListAssignedToMe returns the open issues assigned to the connection's
	// user, limited to scope when it is not empty.
	ListAssignedToMe(ctx context.Context, scope string) ([]Issue, error)
	// GetIssue returns one issue.
	GetIssue(ctx context.Context, id string) (*Issue, error)
	// ListScopes returns what a project can be bound to.
	ListScopes(ctx context.Context) ([]Scope, error)
	// Check verifies the address and the credentials, returning the name of
	// the user they belong to.
	Check(ctx context.Context) (string, error)
}

// Optional capabilities. Not implemented yet by any provider; declared so that
// adding write-back later does not change the Provider interface.

// StatusWriter changes an issue's status.
type StatusWriter interface {
	Statuses(ctx context.Context, issueID string) ([]Status, error)
	SetStatus(ctx context.Context, issueID, statusID string) error
}

// Commenter adds a comment to an issue.
type Commenter interface {
	AddComment(ctx context.Context, issueID, text string) error
}

// TimeLogger records time spent on an issue.
type TimeLogger interface {
	LogTime(ctx context.Context, issueID string, spent time.Duration, note string) error
}

// Connection is everything needed to reach a tracker except the secret.
type Connection struct {
	Kind    string
	BaseURL string
	Token   string
}

// New returns the provider for a connection.
func New(conn Connection) (Provider, error) {
	if strings.TrimSpace(conn.Token) == "" {
		return nil, fmt.Errorf("error.trackerNoToken")
	}
	switch conn.Kind {
	case KindRedmine:
		return newRedmine(conn.BaseURL, conn.Token, nil)
	default:
		return nil, fmt.Errorf("unknown tracker kind %q", conn.Kind)
	}
}

// Capabilities reports which optional interfaces a provider implements, for
// the UI to decide which actions to offer.
type Capabilities struct {
	SetStatus bool `json:"setStatus"`
	Comment   bool `json:"comment"`
	LogTime   bool `json:"logTime"`
}

// CapabilitiesOf inspects a provider.
func CapabilitiesOf(p Provider) Capabilities {
	_, status := p.(StatusWriter)
	_, comment := p.(Commenter)
	_, logTime := p.(TimeLogger)
	return Capabilities{SetStatus: status, Comment: comment, LogTime: logTime}
}

// FormatPrompt turns an issue into the text typed into an agent's tab.
//
// The reference and the link come first, so an agent with access to the
// tracker (through an MCP server, say) knows which issue to update without
// being told twice.
func FormatPrompt(issue *Issue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s: %s\n\n", issue.Ref, issue.Title)
	if issue.URL != "" {
		fmt.Fprintf(&b, "Issue: %s\n", issue.URL)
	}
	if issue.Tracker != "" {
		fmt.Fprintf(&b, "Type: %s\n", issue.Tracker)
	}
	if issue.Status.Name != "" {
		fmt.Fprintf(&b, "Status: %s\n", issue.Status.Name)
	}
	if issue.Priority != "" {
		fmt.Fprintf(&b, "Priority: %s\n", issue.Priority)
	}
	if desc := strings.TrimSpace(issue.Description); desc != "" {
		fmt.Fprintf(&b, "\n%s\n", desc)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
