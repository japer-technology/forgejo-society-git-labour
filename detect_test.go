package forges

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type errTransport struct{ err error }

func (t errTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

func TestDetectForgeTypeSurfacesTransportError(t *testing.T) {
	netErr := errors.New("dial tcp: lookup forge.invalid: no such host")
	hc := &http.Client{Transport: errTransport{err: netErr}}

	_, err := DetectForgeType(context.Background(), "forge.invalid", hc)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), netErr.Error()) {
		t.Fatalf("expected transport error to be surfaced, got: %v", err)
	}
}

func TestProbeGiteaAPIRequiresVersion(t *testing.T) {
	for _, body := range []string{`{}`, `{"version":""}`, `{"version":" "}`, `{"message":"not found"}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			ft, err := probeGiteaAPI(context.Background(), srv.Client(), srv.URL)
			if err == nil || ft != Unknown {
				t.Fatalf("expected unknown forge and error, got %q, %v", ft, err)
			}
		})
	}
}

func TestRegisterDomainMissingBuilder(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-GitHub-Request-Id", "test")
	}))
	defer srv.Close()
	domain := strings.TrimPrefix(srv.URL, "https://")
	for _, builders := range []ForgeBuilders{
		{},
		{GitHub: func(string, string, *http.Client) Forge { return nil }},
	} {
		client := NewClient(WithHTTPClient(srv.Client()))
		if err := client.RegisterDomain(context.Background(), domain, "test-token", builders); err == nil {
			t.Fatal("expected builder error")
		}

		if _, err := client.ForgeFor(domain); err == nil {
			t.Fatal("failed registration installed a forge")
		}
		if _, ok := client.Tokens()[domain]; ok {
			t.Fatal("failed registration changed tokens")
		}
	}
}

func TestDetectFromAPIRejectsCatchAllResponses(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `<html>Sign in</html>`, `{"message":"not found"}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			ft, err := detectFromAPI(context.Background(), srv.Client(), srv.URL)
			if err == nil || ft != Unknown {
				t.Fatalf("catch-all response detected as %q, error = %v", ft, err)
			}
		})
	}
}
