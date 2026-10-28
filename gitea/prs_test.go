package gitea

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	forge "github.com/git-pkgs/forge"
)

func TestGiteaListMergedPRsFiltersBeforeLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "closed" {
			t.Errorf("expected API state=closed, got %s", r.URL.RawQuery)
		}
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`[{"number":1,"state":"closed","merged":false}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"number":2,"state":"closed","merged":true}]`))
		default:
			t.Error("requested an unexpected page")
			_, _ = w.Write([]byte(`[]`))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	prs, err := New(srv.URL, "", srv.Client()).PullRequests().List(context.Background(), "owner", "repo", forge.ListPROpts{
		State: "merged", PerPage: 1, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 2 || !prs[0].Merged {
		t.Fatalf("expected merged PR 2, got %+v", prs)
	}
}

func TestGiteaMergeRequiresSuccess(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusAccepted} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/v1/repos/owner/repo/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()
			err := New(srv.URL, "", srv.Client()).PullRequests().Merge(context.Background(), "owner", "repo", 1, forge.MergePROpts{})
			if (err != nil) != (status == http.StatusAccepted) {
				t.Fatalf("status %d: unexpected merge error %v", status, err)
			}
		})
	}
}
