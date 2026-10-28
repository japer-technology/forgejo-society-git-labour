package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-pkgs/forge/internal/config"
	"github.com/spf13/cobra"
)

type apiTestTransport func(*http.Request) (*http.Response, error)

func (f apiTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAPIUsesConfiguredForgeType(t *testing.T) {
	oldRepo, oldType, oldMethod, oldSilent := flagRepo, flagForgeType, flagAPIMethod, flagAPISilent
	oldTransport := http.DefaultTransport
	t.Cleanup(func() {
		flagRepo, flagForgeType, flagAPIMethod, flagAPISilent = oldRepo, oldType, oldMethod, oldSilent
		http.DefaultTransport = oldTransport
		config.ResetCache()
	})
	flagRepo, flagForgeType, flagAPIMethod, flagAPISilent = "owner/repo", "", http.MethodGet, true
	t.Chdir(t.TempDir())

	for _, tt := range []struct {
		host, forgeType, want string
	}{
		{"code.example", "gitlab", "https://code.example/api/v4/repos/owner/repo"},
		{"code.example", "github", "https://code.example/api/v3/repos/owner/repo"},
		{"github.example", "gitea", "https://github.example/api/v1/repos/owner/repo"},
	} {
		t.Run(tt.forgeType, func(t *testing.T) {
			config.ResetCache()
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			t.Setenv("FORGE_HOST", tt.host)
			if err := os.Mkdir(filepath.Join(dir, "forge"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "forge", "config"), []byte(fmt.Sprintf("[%s]\ntype = %s\n", tt.host, tt.forgeType)), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			http.DefaultTransport = apiTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != tt.want {
					return nil, fmt.Errorf("wrong API URL: %s", r.URL)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{}`)),
				}, nil
			})
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			if err := apiCmd.RunE(cmd, []string{"repos/{owner}/{repo}"}); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("expected one API request, got %d", calls)
			}
		})
	}
}
