package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	forge "github.com/git-pkgs/forge"
	"github.com/google/go-github/v82/github"
)

func TestGitHubListMergedPRsFiltersBeforeLimit(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "closed" {
			t.Errorf("expected API state=closed, got %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", fmt.Sprintf(`<%s/api/v3/repos/owner/repo/pulls?page=2&state=closed>; rel="next"`, srv.URL))
			_, _ = w.Write([]byte(`[{"number":1,"state":"closed","merged_at":null}]`))
		} else {
			_, _ = w.Write([]byte(`[{"number":2,"state":"closed","merged_at":"2026-01-01T00:00:00Z"}]`))
		}
	}))
	defer srv.Close()
	prs, err := newTestGitHubPRService(srv).List(context.Background(), "owner", "repo", forge.ListPROpts{State: "merged", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 2 || !prs[0].Merged || prs[0].State != "merged" {
		t.Fatalf("expected only merged PR 2, got %+v", prs)
	}
}

func TestGitHubMergeBranchDeletionSafety(t *testing.T) {
	for _, tt := range []struct {
		name       string
		getStatus  int
		head       string
		merged     bool
		wantMerge  bool
		wantDelete bool
		wantErr    bool
	}{
		{
			name: "fork branch", getStatus: http.StatusOK, merged: true,
			head:      `{"ref":"feature","repo":{"name":"fork","owner":{"login":"contributor"},"default_branch":"main"}}`,
			wantMerge: true, wantDelete: true,
		},
		{
			name: "merge refused", getStatus: http.StatusOK,
			head:      `{"ref":"feature","repo":{"name":"fork","owner":{"login":"contributor"}}}`,
			wantMerge: true, wantErr: true,
		},
		{name: "fetch failed", getStatus: http.StatusForbidden, wantErr: true},
		{name: "missing source", getStatus: http.StatusOK, head: `null`, wantErr: true},
		{
			name: "default branch", getStatus: http.StatusOK,
			head:    `{"ref":"main","repo":{"name":"fork","owner":{"login":"contributor"},"default_branch":"main"}}`,
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var mergeCalled, deleteCalled bool
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v3/repos/owner/repo/pulls/1", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.getStatus)
				if tt.getStatus == http.StatusOK {
					_, _ = fmt.Fprintf(w, `{"number":1,"head":%s}`, tt.head)
				}
			})
			mux.HandleFunc("PUT /api/v3/repos/owner/repo/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
				mergeCalled = true
				_, _ = fmt.Fprintf(w, `{"merged":%t,"message":"merge result"}`, tt.merged)
			})
			mux.HandleFunc("DELETE /api/v3/repos/contributor/fork/git/refs/heads/feature", func(w http.ResponseWriter, r *http.Request) {
				deleteCalled = true
				w.WriteHeader(http.StatusNoContent)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()
			err := newTestGitHubPRService(srv).Merge(context.Background(), "owner", "repo", 1, forge.MergePROpts{Delete: true})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr = %t", err, tt.wantErr)
			}
			if mergeCalled != tt.wantMerge || deleteCalled != tt.wantDelete {
				t.Fatalf("merge called = %t, deletion called = %t", mergeCalled, deleteCalled)
			}
		})
	}
}

func newTestGitHubPRService(srv *httptest.Server) *gitHubPRService {
	c := github.NewClient(nil)
	c, _ = c.WithEnterpriseURLs(srv.URL+"/api/v3", srv.URL+"/api/v3")
	return &gitHubPRService{client: c}
}

func TestGitHubGetPR(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/octocat/hello-world/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(github.PullRequest{
			Number:    ptrInt(1),
			Title:     ptr("Add feature"),
			Body:      ptr("This adds a new feature"),
			State:     ptr("open"),
			Draft:     ptrBool(false),
			Merged:    ptrBool(false),
			Mergeable: ptrBool(true),
			HTMLURL:   ptr("https://github.com/octocat/hello-world/pull/1"),
			DiffURL:   ptr("https://github.com/octocat/hello-world/pull/1.diff"),
			User: &github.User{
				Login:     ptr("octocat"),
				AvatarURL: ptr("https://avatars.githubusercontent.com/u/1"),
				HTMLURL:   ptr("https://github.com/octocat"),
			},
			Head: &github.PullRequestBranch{Ref: ptr("feature-branch")},
			Base: &github.PullRequestBranch{Ref: ptr("main")},
			RequestedReviewers: []*github.User{
				{Login: ptr("reviewer1")},
			},
			Labels: []*github.Label{
				{Name: ptr("enhancement"), Color: ptr("a2eeef")},
			},
			Comments:     ptrInt(2),
			Additions:    ptrInt(10),
			Deletions:    ptrInt(3),
			ChangedFiles: ptrInt(2),
			CreatedAt:    &github.Timestamp{Time: parseTime("2024-01-01T00:00:00Z")},
			UpdatedAt:    &github.Timestamp{Time: parseTime("2024-01-02T00:00:00Z")},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	pr, err := s.Get(context.Background(), "octocat", "hello-world", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertEqualInt(t, "Number", 1, pr.Number)
	assertEqual(t, "Title", "Add feature", pr.Title)
	assertEqual(t, "Body", "This adds a new feature", pr.Body)
	assertEqual(t, "State", "open", pr.State)
	assertEqualBool(t, "Draft", false, pr.Draft)
	assertEqualBool(t, "Merged", false, pr.Merged)
	assertEqualBool(t, "Mergeable", true, pr.Mergeable)
	assertEqual(t, "Head", "feature-branch", pr.Head)
	assertEqual(t, "Base", "main", pr.Base)
	assertEqual(t, "Author.Login", "octocat", pr.Author.Login)
	assertEqualInt(t, "Comments", 2, pr.Comments)
	assertEqualInt(t, "Additions", 10, pr.Additions)
	assertEqualInt(t, "Deletions", 3, pr.Deletions)
	assertEqualInt(t, "ChangedFiles", 2, pr.ChangedFiles)

	if len(pr.Reviewers) != 1 {
		t.Fatalf("expected 1 reviewer, got %d", len(pr.Reviewers))
	}
	assertEqual(t, "Reviewers[0].Login", "reviewer1", pr.Reviewers[0].Login)

	if len(pr.Labels) != 1 {
		t.Fatalf("expected 1 label, got %d", len(pr.Labels))
	}
	assertEqual(t, "Labels[0].Name", "enhancement", pr.Labels[0].Name)
}

func TestGitHubGetPRNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/octocat/hello-world/pulls/999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	_, err := s.Get(context.Background(), "octocat", "hello-world", 999)
	if err != forge.ErrNotFound {
		t.Fatalf("expected forge.ErrNotFound, got %v", err)
	}
}

func TestGitHubGetPRMerged(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/octocat/hello-world/pulls/5", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(github.PullRequest{
			Number: ptrInt(5),
			Title:  ptr("Merged PR"),
			State:  ptr("closed"),
			Merged: ptrBool(true),
			User:   &github.User{Login: ptr("octocat")},
			Head:   &github.PullRequestBranch{Ref: ptr("feature")},
			Base:   &github.PullRequestBranch{Ref: ptr("main")},
			MergedBy: &github.User{
				Login: ptr("merger"),
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	pr, err := s.Get(context.Background(), "octocat", "hello-world", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertEqual(t, "State", "merged", pr.State)
	assertEqualBool(t, "Merged", true, pr.Merged)
	if pr.MergedBy == nil {
		t.Fatal("expected MergedBy")
	}
	assertEqual(t, "MergedBy.Login", "merger", pr.MergedBy.Login)
}

func TestGitHubListPRs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/octocat/hello-world/pulls", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]*github.PullRequest{
			{
				Number: ptrInt(1),
				Title:  ptr("First PR"),
				State:  ptr("open"),
				User:   &github.User{Login: ptr("alice")},
				Head:   &github.PullRequestBranch{Ref: ptr("feature-1")},
				Base:   &github.PullRequestBranch{Ref: ptr("main")},
			},
			{
				Number: ptrInt(2),
				Title:  ptr("Second PR"),
				State:  ptr("open"),
				User:   &github.User{Login: ptr("bob")},
				Head:   &github.PullRequestBranch{Ref: ptr("feature-2")},
				Base:   &github.PullRequestBranch{Ref: ptr("main")},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	prs, err := s.List(context.Background(), "octocat", "hello-world", forge.ListPROpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("expected 2 PRs, got %d", len(prs))
	}
	assertEqual(t, "prs[0].Title", "First PR", prs[0].Title)
	assertEqual(t, "prs[0].Head", "feature-1", prs[0].Head)
	assertEqual(t, "prs[1].Title", "Second PR", prs[1].Title)
}

func TestGitHubCreatePR(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v3/repos/octocat/hello-world/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(github.PullRequest{
			Number:  ptrInt(10),
			Title:   ptr("New feature"),
			State:   ptr("open"),
			HTMLURL: ptr("https://github.com/octocat/hello-world/pull/10"),
			User:    &github.User{Login: ptr("octocat")},
			Head:    &github.PullRequestBranch{Ref: ptr("my-branch")},
			Base:    &github.PullRequestBranch{Ref: ptr("main")},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	pr, err := s.Create(context.Background(), "octocat", "hello-world", forge.CreatePROpts{
		Title: "New feature",
		Body:  "Description",
		Head:  "my-branch",
		Base:  "main",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqualInt(t, "Number", 10, pr.Number)
	assertEqual(t, "Title", "New feature", pr.Title)
	assertEqual(t, "State", "open", pr.State)
}

func TestGitHubClosePR(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v3/repos/octocat/hello-world/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(github.PullRequest{
			Number: ptrInt(1),
			State:  ptr("closed"),
			User:   &github.User{Login: ptr("octocat")},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	if err := s.Close(context.Background(), "octocat", "hello-world", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitHubReopenPR(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v3/repos/octocat/hello-world/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(github.PullRequest{
			Number: ptrInt(1),
			State:  ptr("open"),
			User:   &github.User{Login: ptr("octocat")},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	if err := s.Reopen(context.Background(), "octocat", "hello-world", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitHubMergePR(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v3/repos/octocat/hello-world/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(github.PullRequestMergeResult{
			Merged: ptrBool(true),
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	if err := s.Merge(context.Background(), "octocat", "hello-world", 1, forge.MergePROpts{Method: "squash"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitHubDiffPR(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/octocat/hello-world/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "application/vnd.github.v3.diff" {
			_, _ = w.Write([]byte("diff --git a/file.txt b/file.txt\n"))
			return
		}
		_ = json.NewEncoder(w).Encode(github.PullRequest{Number: ptrInt(1)})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	diff, err := s.Diff(context.Background(), "octocat", "hello-world", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff == "" {
		t.Error("expected non-empty diff")
	}
}

func TestGitHubPRCreateComment(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v3/repos/octocat/hello-world/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(github.IssueComment{
			ID:   ptrInt64(200),
			Body: ptr("LGTM"),
			User: &github.User{Login: ptr("reviewer")},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	comment, err := s.CreateComment(context.Background(), "octocat", "hello-world", 1, "LGTM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if comment.ID != 200 {
		t.Errorf("expected ID=200, got %d", comment.ID)
	}
	assertEqual(t, "Body", "LGTM", comment.Body)
	assertEqual(t, "Author.Login", "reviewer", comment.Author.Login)
}

func TestGitHubPRListComments(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/octocat/hello-world/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]*github.IssueComment{
			{
				ID:   ptrInt64(1),
				Body: ptr("First"),
				User: &github.User{Login: ptr("alice")},
			},
			{
				ID:   ptrInt64(2),
				Body: ptr("Second"),
				User: &github.User{Login: ptr("bob")},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	comments, err := s.ListComments(context.Background(), "octocat", "hello-world", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	assertEqual(t, "comments[0].Body", "First", comments[0].Body)
	assertEqual(t, "comments[1].Body", "Second", comments[1].Body)
}

func TestGitHubMergePRDeletesBranch(t *testing.T) {
	var deletedRef string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/octocat/hello-world/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(github.PullRequest{
			Number: ptrInt(1),
			Title:  ptr("Fix bug"),
			State:  ptr("open"),
			User:   &github.User{Login: ptr("octocat")},
			Head: &github.PullRequestBranch{
				Ref: ptr("feature-branch"),
				Repo: &github.Repository{
					Name: ptr("hello-world"), Owner: &github.User{Login: ptr("octocat")},
				},
			},
			Base: &github.PullRequestBranch{Ref: ptr("main")},
		})
	})
	mux.HandleFunc("PUT /api/v3/repos/octocat/hello-world/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(github.PullRequestMergeResult{
			Merged: ptrBool(true),
		})
	})
	mux.HandleFunc("DELETE /api/v3/repos/octocat/hello-world/git/refs/{ref...}", func(w http.ResponseWriter, r *http.Request) {
		deletedRef = r.PathValue("ref")
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	err := s.Merge(context.Background(), "octocat", "hello-world", 1, forge.MergePROpts{
		Method: "squash",
		Title:  "Fix bug in parser",
		Delete: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The ref deleted should be the PR's head branch, not the commit title.
	if deletedRef != "heads/feature-branch" {
		t.Errorf("expected deleted ref 'heads/feature-branch', got %q", deletedRef)
	}
}

func TestGitHubCreatePRWithReviewersError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v3/repos/octocat/hello-world/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(github.PullRequest{
			Number: ptrInt(10),
			Title:  ptr("New feature"),
			State:  ptr("open"),
			User:   &github.User{Login: ptr("octocat")},
			Head:   &github.PullRequestBranch{Ref: ptr("my-branch")},
			Base:   &github.PullRequestBranch{Ref: ptr("main")},
		})
	})
	mux.HandleFunc("POST /api/v3/repos/octocat/hello-world/pulls/10/requested_reviewers", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Reviews may not be requested from the following users: nonexistent"}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	_, err := s.Create(context.Background(), "octocat", "hello-world", forge.CreatePROpts{
		Title:     "New feature",
		Head:      "my-branch",
		Base:      "main",
		Reviewers: []string{"nonexistent"},
	})
	if err == nil {
		t.Fatal("expected error when reviewer request fails, got nil")
	}
	if !strings.Contains(err.Error(), "failed to add reviewers") {
		t.Errorf("error should mention failed reviewers, got: %v", err)
	}
}

func TestGitHubCreatePRWithLabelsAndAssignees(t *testing.T) {
	var editedIssue github.IssueRequest
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v3/repos/octocat/hello-world/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(github.PullRequest{
			Number: ptrInt(11),
			Title:  ptr("Feature"),
			State:  ptr("open"),
			User:   &github.User{Login: ptr("octocat")},
			Head:   &github.PullRequestBranch{Ref: ptr("feature")},
			Base:   &github.PullRequestBranch{Ref: ptr("main")},
		})
	})
	mux.HandleFunc("PATCH /api/v3/repos/octocat/hello-world/issues/11", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&editedIssue)
		_ = json.NewEncoder(w).Encode(github.Issue{Number: ptrInt(11)})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestGitHubPRService(srv)
	pr, err := s.Create(context.Background(), "octocat", "hello-world", forge.CreatePROpts{
		Title:     "Feature",
		Head:      "feature",
		Base:      "main",
		Assignees: []string{"alice"},
		Labels:    []string{"bug", "urgent"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqualInt(t, "Number", 11, pr.Number)

	// Verify assignees and labels were sent in a single edit
	if editedIssue.Assignees == nil || len(*editedIssue.Assignees) != 1 {
		t.Errorf("expected 1 assignee in edit request")
	}
	if editedIssue.Labels == nil || len(*editedIssue.Labels) != 2 {
		t.Errorf("expected 2 labels in edit request")
	}
}
