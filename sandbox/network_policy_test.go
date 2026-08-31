package sandbox

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestValidateNetworkURLAccessHonorsAllowList(t *testing.T) {
	exec := NewExecutor(DefaultConfig())

	if _, err := validateNetworkURLAccess(exec, "https://github.com/openai/openai-go"); err != nil {
		t.Fatalf("validateNetworkURLAccess() github error = %v", err)
	}
	if _, err := validateNetworkURLAccess(exec, "https://docs.github.com/en"); err != nil {
		t.Fatalf("validateNetworkURLAccess() wildcard github error = %v", err)
	}
	if _, err := validateNetworkURLAccess(exec, "https://example.com/blocked"); err == nil {
		t.Fatal("expected example.com to be blocked by default allow list")
	}
}

func TestValidateNetworkURLAccessAllowsAnyHostWhenIsolationDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Network.Isolation = false

	exec := NewExecutor(cfg)
	if _, err := validateNetworkURLAccess(exec, "https://example.com/open"); err != nil {
		t.Fatalf("validateNetworkURLAccess() error = %v, want nil", err)
	}
}

func TestValidateNetworkURLAccessBlocksSensitiveIPLiterals(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Network.Isolation = false // must not matter for IP literals
	exec := NewExecutor(cfg)

	for _, rawURL := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:8080/debug",
		"http://[::1]/",
		"http://100.64.0.9/x",
		"http://[fe80::1]/",
	} {
		if _, err := validateNetworkURLAccess(exec, rawURL); err == nil {
			t.Errorf("expected %s to be blocked by sandbox policy", rawURL)
		}
	}
}

func TestValidateNetworkURLAccessAllowsExplicitIPAndCIDR(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Network.Isolation = true
	cfg.Sandbox.Network.Allow = []string{"10.0.0.0/8", "192.168.1.5"}
	exec := NewExecutor(cfg)

	if _, err := validateNetworkURLAccess(exec, "http://10.1.2.3/x"); err != nil {
		t.Fatalf("expected CIDR entry to allow 10.1.2.3, got %v", err)
	}
	if _, err := validateNetworkURLAccess(exec, "http://192.168.1.5/x"); err != nil {
		t.Fatalf("expected IP entry to allow 192.168.1.5, got %v", err)
	}
	for _, blocked := range []string{"http://10.1.2.3:8080/x", "http://192.168.1.6/x", "http://169.254.169.254/"} {
		if _, err := validateNetworkURLAccess(exec, blocked); err == nil {
			t.Errorf("expected %s to be blocked", blocked)
		}
	}
}

func TestValidateNetworkURLAccessRestrictsPortsToDefaults(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Network.Isolation = true
	cfg.Sandbox.Network.Allow = []string{"github.com", "example.com:8080"}
	exec := NewExecutor(cfg)

	if _, err := validateNetworkURLAccess(exec, "https://github.com/owner/repo"); err != nil {
		t.Fatalf("expected default port to be allowed, got %v", err)
	}
	if _, err := validateNetworkURLAccess(exec, "https://github.com:8080/owner/repo"); err == nil {
		t.Fatal("expected non-default port to be blocked without an explicit host:port entry")
	}
	if _, err := validateNetworkURLAccess(exec, "https://example.com:8080/x"); err != nil {
		t.Fatalf("expected explicit host:port entry to allow the port, got %v", err)
	}
	if _, err := validateNetworkURLAccess(exec, "https://example.com/x"); err == nil {
		t.Fatal("expected host:port-only entry to not cover the default port")
	}
}

func TestValidateNetworkAllowEntryRejectsMalformedEntries(t *testing.T) {
	for _, entry := range []string{
		"",
		"  ",
		"github.com openai.com",
		"https://github.com",
		"10.0.0.0/8/24",
		"192.168.0",
		"github.com:99999",
		"github.com:abc",
		"fe80:::1",
		"*.",
	} {
		if err := validateNetworkAllowEntry(entry); err == nil {
			t.Errorf("validateNetworkAllowEntry(%q) = nil, want error", entry)
		}
	}
}

func TestValidateNetworkAllowEntryAcceptsValidEntries(t *testing.T) {
	for _, entry := range []string{
		"github.com",
		"*.github.com",
		"10.0.0.0/8",
		"fe80::/10",
		"127.0.0.1",
		"127.0.0.1:8080",
		"[::1]:8080",
		"::1",
		"example.com:443",
	} {
		if err := validateNetworkAllowEntry(entry); err != nil {
			t.Errorf("validateNetworkAllowEntry(%q) = %v, want nil", entry, err)
		}
	}
}

func TestConfigValidateRejectsBadNetworkAllowEntry(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Network.Allow = append(cfg.Sandbox.Network.Allow, "https://github.com")

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected Validate() to reject a malformed network allow entry")
	}
}

// redirectTestServer builds two loopback HTTP servers where the first
// redirects to the second, plus the allow entries for their ports.
func redirectTestServers(t *testing.T, target http.HandlerFunc) (first, second *httptest.Server, allow []string) {
	t.Helper()
	second = httptest.NewServer(target)
	first = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL+"/img.png", http.StatusFound)
	}))
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)

	for _, s := range []*httptest.Server{first, second} {
		u, err := url.Parse(s.URL)
		if err != nil {
			t.Fatalf("url.Parse(%q) error = %v", s.URL, err)
		}
		allow = append(allow, u.Hostname()+":"+u.Port())
	}
	return first, second, allow
}

func testPNGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return buf.Bytes()
}

func TestDownloadImageBlocksRedirectToInternalTarget(t *testing.T) {
	first, _, allow := redirectTestServers(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNGBytes(t))
	})
	// Only the first host is allowed; the redirect target is not.
	allow = allow[:1]

	cfg := DefaultConfig()
	cfg.Sandbox.Network.Isolation = true
	cfg.Sandbox.Network.Allow = allow
	exec := NewExecutor(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, _, err := downloadImage(ctx, exec, first.URL+"/img.png", 5*1024*1024)
	if err == nil {
		t.Fatal("expected redirect to a non-allowed host to be blocked")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error = %v, want it to mention the blocked redirect", err)
	}
}

func TestDownloadImageAllowsRedirectBetweenAllowedHosts(t *testing.T) {
	first, _, allow := redirectTestServers(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNGBytes(t))
	})

	cfg := DefaultConfig()
	cfg.Sandbox.Network.Isolation = true
	cfg.Sandbox.Network.Allow = allow
	exec := NewExecutor(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	data, mediaType, err := downloadImage(ctx, exec, first.URL+"/img.png", 5*1024*1024)
	if err != nil {
		t.Fatalf("downloadImage() error = %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected image data")
	}
	if mediaType != "image/png" {
		t.Errorf("mediaType = %q, want image/png", mediaType)
	}
}
