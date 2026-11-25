package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// environ is a made-up environment.
type environ map[string]string

func (e environ) get(k string) string { return e[k] }

func TestSignInSettings(t *testing.T) {
	full := environ{
		"APP_URL": "https://radio.example.test/", "AUTHENTIK_URL": "https://auth.example.test",
		"AUTHENTIK_INTERNAL_URL": "http://authentik-server:9000",
		"OIDC_CLIENT_SECRET":     "made-up-client-secret-0123456789",
	}
	c, err := signInFromEnv(full.get)
	if err != nil || !c.on() || c.clientID != "radioarchive" || c.appURL.String() != "https://radio.example.test" {
		t.Fatalf("full settings: %+v, %v", c, err)
	}
	// Printing the settings never shows the secret.
	if s := c.String(); strings.Contains(s, "made-up-client-secret") {
		t.Errorf("String() shows a secret: %s", s)
	}

	// No AUTHENTIK_URL: off, whatever else is set.
	if c, err := signInFromEnv(environ{"APP_URL": "https://radio.example.test"}.get); err != nil || c.on() {
		t.Errorf("without AUTHENTIK_URL: on %v, %v", c.on(), err)
	}

	// Half set: refused, naming what's missing, never a value.
	for _, missing := range []string{"APP_URL", "OIDC_CLIENT_SECRET"} {
		e := environ{}
		for k, v := range full {
			if k != missing {
				e[k] = v
			}
		}
		_, err := signInFromEnv(e.get)
		if err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("without %s: %v", missing, err)
		}
		if err != nil && strings.Contains(err.Error(), "made-up-client-secret") {
			t.Errorf("an error shows a secret: %v", err)
		}
	}
	for _, bad := range []string{"radio.example.test", "ftp://radio.example.test", "https://radio.example.test/radio", "https://radio.example.test/?x=1"} {
		e := environ{}
		for k, v := range full {
			e[k] = v
		}
		e["APP_URL"] = bad
		if _, err := signInFromEnv(e.get); err == nil {
			t.Errorf("APP_URL %q was taken", bad)
		}
	}

	// The secret from a file (a Docker or systemd secret).
	secretFile := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretFile, []byte("secret-from-a-file-0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := environ{"APP_URL": full["APP_URL"], "AUTHENTIK_URL": full["AUTHENTIK_URL"], "OIDC_CLIENT_SECRET_FILE": secretFile}
	c, err = signInFromEnv(files.get)
	if err != nil || c.clientSecret != "secret-from-a-file-0123456789" {
		t.Fatalf("from a file: %v", err)
	}
	files["OIDC_CLIENT_SECRET"] = "and-in-the-environment-0123456789"
	if _, err := signInFromEnv(files.get); err == nil {
		t.Error("the secret both set and in a file was taken")
	}

	// The accounts service account's Authentik token (ADR 0033): optional,
	// from the environment or a file, never both, never shown.
	if c.apiToken != "" {
		t.Errorf("a token with none set: %q", c.apiToken)
	}
	tok := environ{}
	for k, v := range full {
		tok[k] = v
	}
	tok["AUTHENTIK_API_TOKEN"] = "made-up-api-token-0123456789"
	if c, err := signInFromEnv(tok.get); err != nil || c.apiToken != "made-up-api-token-0123456789" || strings.Contains(c.String(), "made-up-api-token") {
		t.Errorf("the token: %v, shown %q", err, c.String())
	}
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("token-from-a-file-0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok["AUTHENTIK_API_TOKEN_FILE"] = tokenFile
	if _, err := signInFromEnv(tok.get); err == nil || strings.Contains(err.Error(), "made-up-api-token") {
		t.Errorf("the token both set and in a file: %v", err)
	}
	delete(tok, "AUTHENTIK_API_TOKEN")
	if c, err := signInFromEnv(tok.get); err != nil || c.apiToken != "token-from-a-file-0123456789" {
		t.Errorf("the token from a file: %v", err)
	}
}
