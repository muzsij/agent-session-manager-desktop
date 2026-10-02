package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Redmine through its REST API, authenticated with the user's API key.
//
// The API has to be enabled by an administrator (Administration → Settings →
// API); the key is on the user's "My account" page.

const (
	// redminePageSize is the most the API returns per request.
	redminePageSize = 100
	// redmineMaxItems bounds a listing. Someone with more open issues assigned
	// than this is not going to pick one from a list either.
	redmineMaxItems = 1000
	// redmineMaxBody bounds one response, so a misconfigured address that
	// answers with something huge cannot exhaust memory.
	redmineMaxBody = 16 << 20
	redmineTimeout = 30 * time.Second
)

type redmine struct {
	base   *url.URL
	key    string
	client *http.Client
}

func newRedmine(baseURL, key string, client *http.Client) (*redmine, error) {
	base, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: redmineTimeout}
	}
	return &redmine{base: base, key: key, client: client}, nil
}

// parseBaseURL accepts the address the way it is copied out of a browser,
// including installs under a sub-path (https://host/redmine/).
func parseBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("error.trackerNoURL")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("error.trackerBadURL")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

func (r *redmine) endpoint(path string, query url.Values) string {
	u := *r.base
	u.Path = r.base.Path + path
	u.RawQuery = query.Encode()
	return u.String()
}

func (r *redmine) get(ctx context.Context, path string, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint(path, query), nil)
	if err != nil {
		return err
	}
	// The header rather than ?key=: a query string ends up in proxy and
	// server logs, a header usually does not.
	req.Header.Set("X-Redmine-API-Key", r.key)
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("redmine: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("error.trackerUnauthorized")
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("error.trackerForbidden")
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("error.trackerNotFound")
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("redmine: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, redmineMaxBody+1))
	if err != nil {
		return fmt.Errorf("redmine: %w", err)
	}
	if len(body) > redmineMaxBody {
		return fmt.Errorf("redmine: response too large")
	}
	if err := json.Unmarshal(body, out); err != nil {
		// An HTML page where JSON was expected is the usual sign of a wrong
		// address or a disabled REST API, not of a broken server.
		return fmt.Errorf("error.trackerNotJSON")
	}
	return nil
}

type redmineRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type redmineIssue struct {
	ID          int        `json:"id"`
	Subject     string     `json:"subject"`
	Description string     `json:"description"`
	Project     redmineRef `json:"project"`
	Tracker     redmineRef `json:"tracker"`
	Status      struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		IsClosed bool   `json:"is_closed"`
	} `json:"status"`
	Priority  redmineRef  `json:"priority"`
	Parent    *redmineRef `json:"parent"`
	DueDate   string      `json:"due_date"`
	UpdatedOn time.Time   `json:"updated_on"`
}

func (r *redmine) toIssue(src *redmineIssue) Issue {
	id := strconv.Itoa(src.ID)
	issue := Issue{
		ID:          id,
		Ref:         "#" + id,
		Title:       src.Subject,
		Description: src.Description,
		Status: Status{
			ID:     strconv.Itoa(src.Status.ID),
			Name:   src.Status.Name,
			Closed: src.Status.IsClosed,
		},
		Priority:  src.Priority.Name,
		Tracker:   src.Tracker.Name,
		Project:   src.Project.Name,
		URL:       r.endpoint("/issues/"+id, nil),
		UpdatedAt: src.UpdatedOn,
	}
	if src.Parent != nil && src.Parent.ID != 0 {
		issue.ParentID = strconv.Itoa(src.Parent.ID)
	}
	if src.DueDate != "" {
		if due, err := time.ParseInLocation("2006-01-02", src.DueDate, time.Local); err == nil {
			issue.DueAt = &due
		}
	}
	return issue
}

func (r *redmine) ListAssignedToMe(ctx context.Context, scope string) ([]Issue, error) {
	query := url.Values{
		"assigned_to_id": {"me"},
		"status_id":      {"open"},
		"sort":           {"updated_on:desc"},
		"limit":          {strconv.Itoa(redminePageSize)},
	}
	if scope = strings.TrimSpace(scope); scope != "" {
		query.Set("project_id", scope)
	}
	issues := []Issue{}
	for offset := 0; offset < redmineMaxItems; offset += redminePageSize {
		query.Set("offset", strconv.Itoa(offset))
		var page struct {
			Issues     []redmineIssue `json:"issues"`
			TotalCount int            `json:"total_count"`
		}
		if err := r.get(ctx, "/issues.json", query, &page); err != nil {
			return nil, err
		}
		for index := range page.Issues {
			issues = append(issues, r.toIssue(&page.Issues[index]))
		}
		if len(page.Issues) < redminePageSize || offset+redminePageSize >= page.TotalCount {
			break
		}
	}
	return issues, nil
}

func (r *redmine) GetIssue(ctx context.Context, id string) (*Issue, error) {
	// Only a number goes into the path: anything else could walk to another
	// endpoint ("1/../../users").
	id = strings.TrimPrefix(strings.TrimSpace(id), "#")
	if _, err := strconv.Atoi(id); err != nil {
		return nil, fmt.Errorf("error.trackerNotFound")
	}
	var body struct {
		Issue redmineIssue `json:"issue"`
	}
	if err := r.get(ctx, "/issues/"+id+".json", nil, &body); err != nil {
		return nil, err
	}
	issue := r.toIssue(&body.Issue)
	return &issue, nil
}

func (r *redmine) ListScopes(ctx context.Context) ([]Scope, error) {
	query := url.Values{"limit": {strconv.Itoa(redminePageSize)}}
	scopes := []Scope{}
	for offset := 0; offset < redmineMaxItems; offset += redminePageSize {
		query.Set("offset", strconv.Itoa(offset))
		var page struct {
			Projects []struct {
				ID         int         `json:"id"`
				Name       string      `json:"name"`
				Identifier string      `json:"identifier"`
				Parent     *redmineRef `json:"parent"`
			} `json:"projects"`
			TotalCount int `json:"total_count"`
		}
		if err := r.get(ctx, "/projects.json", query, &page); err != nil {
			return nil, err
		}
		for _, project := range page.Projects {
			scope := Scope{ID: strconv.Itoa(project.ID), Name: project.Name}
			if project.Parent != nil {
				scope.Parent = project.Parent.Name
			}
			scopes = append(scopes, scope)
		}
		if len(page.Projects) < redminePageSize || offset+redminePageSize >= page.TotalCount {
			break
		}
	}
	return scopes, nil
}

func (r *redmine) Check(ctx context.Context) (string, error) {
	var body struct {
		User struct {
			Login     string `json:"login"`
			Firstname string `json:"firstname"`
			Lastname  string `json:"lastname"`
		} `json:"user"`
	}
	if err := r.get(ctx, "/users/current.json", nil, &body); err != nil {
		return "", err
	}
	name := strings.TrimSpace(body.User.Firstname + " " + body.User.Lastname)
	if name == "" {
		name = body.User.Login
	}
	return name, nil
}
