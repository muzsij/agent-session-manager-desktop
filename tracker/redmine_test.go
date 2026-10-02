package tracker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestRedmineListAssignedToMeSendsFiltersAndPaginates(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Redmine-API-Key"); got != "secret" {
			t.Errorf("api key header = %q", got)
		}
		if r.URL.Query().Get("key") != "" {
			t.Error("api key leaked into the query string")
		}
		if r.URL.Path != "/redmine/issues.json" {
			t.Errorf("path = %q", r.URL.Path)
		}
		queries = append(queries, r.URL.RawQuery)
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		count := redminePageSize
		if offset > 0 {
			count = 2
		}
		var items []string
		for i := 0; i < count; i++ {
			items = append(items, fmt.Sprintf(`{"id":%d,"subject":"S%d","status":{"id":1,"name":"New","is_closed":false},"priority":{"id":2,"name":"Normal"},"due_date":"2026-10-05","updated_on":"2026-10-01T10:00:00Z"}`, offset+i+1, offset+i+1))
		}
		fmt.Fprintf(w, `{"issues":[%s],"total_count":%d,"offset":%d,"limit":100}`, strings.Join(items, ","), redminePageSize+2, offset)
	}))
	defer server.Close()

	r, err := newRedmine(server.URL+"/redmine/", "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	issues, err := r.ListAssignedToMe(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != redminePageSize+2 {
		t.Fatalf("got %d issues, want %d", len(issues), redminePageSize+2)
	}
	if len(queries) != 2 {
		t.Fatalf("made %d requests, want 2", len(queries))
	}
	for _, want := range []string{"assigned_to_id=me", "status_id=open", "project_id=42"} {
		if !strings.Contains(queries[0], want) {
			t.Errorf("query %q lacks %q", queries[0], want)
		}
	}
	first := issues[0]
	if first.Ref != "#1" || first.Title != "S1" || first.Status.Name != "New" || first.Priority != "Normal" {
		t.Errorf("unexpected issue %+v", first)
	}
	if first.URL != server.URL+"/redmine/issues/1" {
		t.Errorf("url = %q", first.URL)
	}
	if first.DueAt == nil || first.DueAt.Format("2006-01-02") != "2026-10-05" {
		t.Errorf("due = %v", first.DueAt)
	}
}

func TestRedmineMapsAuthErrorsAndHTML(t *testing.T) {
	status := http.StatusUnauthorized
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status == http.StatusOK {
			fmt.Fprint(w, "<html>login</html>")
			return
		}
		w.WriteHeader(status)
	}))
	defer server.Close()
	r, _ := newRedmine(server.URL, "k", server.Client())

	if _, err := r.Check(context.Background()); err == nil || err.Error() != "error.trackerUnauthorized" {
		t.Fatalf("401 error = %v", err)
	}
	status = http.StatusOK
	if _, err := r.Check(context.Background()); err == nil || err.Error() != "error.trackerNotJSON" {
		t.Fatalf("html error = %v", err)
	}
}

func TestRedmineGetIssueRejectsNonNumericIDs(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer server.Close()
	r, _ := newRedmine(server.URL, "k", server.Client())
	if _, err := r.GetIssue(context.Background(), "1/../../users/current"); err == nil {
		t.Fatal("expected an error")
	}
	if called {
		t.Fatal("a non-numeric id reached the server")
	}
}

func TestParseBaseURL(t *testing.T) {
	for _, bad := range []string{"", "redmine.local", "ftp://x", "https://"} {
		if _, err := parseBaseURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	u, err := parseBaseURL(" https://example.com/redmine/?x=1 ")
	if err != nil || u.String() != "https://example.com/redmine" {
		t.Fatalf("got %v, %v", u, err)
	}
}

func TestFormatPrompt(t *testing.T) {
	got := FormatPrompt(&Issue{Ref: "#7", Title: "Fix login", URL: "https://r/issues/7", Status: Status{Name: "New"}, Priority: "High", Description: "Steps\n"})
	for _, want := range []string{"## #7: Fix login", "Issue: https://r/issues/7", "Status: New", "Priority: High", "Steps"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
}
