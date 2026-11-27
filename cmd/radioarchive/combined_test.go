package main

import (
	"strings"
	"testing"

	"github.com/RIKIKU/RadioArchive/web"
)

// R102, R104 (ADR 0054): the combined deployment's settings. All unset is
// standalone; PAGERARCHIVE_URL turns on the switcher, named Pager unless
// PAGERARCHIVE_NAME says otherwise; LOOK_COOKIE_DOMAIN is lower-cased and
// loses a leading dot, and needs APP_URL in it. Anything else stops the
// server, naming the setting.
func TestCombinedSettings(t *testing.T) {
	if c, err := combinedFromEnv(environ{"APP_URL": "https://radio.example.test"}.get); err != nil || c != (web.Combined{}) {
		t.Errorf("unset: %+v, %v; want standalone", c, err)
	}

	for in, want := range map[string]string{
		"https://pager.example.test":          "https://pager.example.test",
		"https://pager.example.test/":         "https://pager.example.test",
		" http://pager.example.test:8080/ ":   "http://pager.example.test:8080",
		"https://example.test/pagerarchive/":  "https://example.test/pagerarchive",
		"https://PAGER.example.test/messages": "https://pager.example.test/messages",
	} {
		c, err := combinedFromEnv(environ{"PAGERARCHIVE_URL": in}.get)
		if err != nil || c.PagerArchiveURL != want || c.PagerArchiveName != "Pager" || c.LookDomain != "" {
			t.Errorf("PAGERARCHIVE_URL %q: %+v, %v; want %s, named Pager", in, c, err, want)
		}
	}
	for _, in := range []string{"pager.example.test", "/pager", "ftp://pager.example.test", "https://", "https://u:p@pager.example.test",
		"https://pager.example.test/?x=1", "https://pager.example.test/#top", "https://pager.example.test/?", "http://[::1"} {
		if _, err := combinedFromEnv(environ{"PAGERARCHIVE_URL": in}.get); err == nil || !strings.Contains(err.Error(), "PAGERARCHIVE_URL") {
			t.Errorf("PAGERARCHIVE_URL %q: %v, want an error naming the setting", in, err)
		}
	}
	if c, err := combinedFromEnv(environ{"PAGERARCHIVE_URL": "https://pager.example.test", "PAGERARCHIVE_NAME": " Pages "}.get); err != nil || c.PagerArchiveName != "Pages" {
		t.Errorf("PAGERARCHIVE_NAME: %+v, %v", c, err)
	}
	for _, in := range []string{strings.Repeat("P", 41), "Pa\nger"} {
		if _, err := combinedFromEnv(environ{"PAGERARCHIVE_URL": "https://pager.example.test", "PAGERARCHIVE_NAME": in}.get); err == nil || !strings.Contains(err.Error(), "PAGERARCHIVE_NAME") {
			t.Errorf("PAGERARCHIVE_NAME %q: %v, want an error naming the setting", in, err)
		}
	}

	for _, c := range []struct {
		domain, app, want string
		secure            bool
	}{
		{"example.test", "https://radio.example.test", "example.test", true},
		{".Example.Test", "https://radio.example.test/", "example.test", true},
		{"kyledoestech.com", "https://radio.kyledoestech.com", "kyledoestech.com", true},
		{"radio.example.test", "https://radio.example.test", "radio.example.test", true},
		{"example.test", "http://radio.example.test:18094", "example.test", false},
	} {
		got, err := combinedFromEnv(environ{"LOOK_COOKIE_DOMAIN": c.domain, "APP_URL": c.app}.get)
		if err != nil || got.LookDomain != c.want || got.Secure != c.secure {
			t.Errorf("LOOK_COOKIE_DOMAIN %q, APP_URL %q: %+v, %v; want %s, Secure %v", c.domain, c.app, got, err, c.want, c.secure)
		}
	}
	for _, c := range []struct{ domain, app, names string }{
		{"example.test", "", "APP_URL"},
		{"example.test", "https://radio.example.org", "LOOK_COOKIE_DOMAIN"},
		{"example.test", "https://notexample.test", "LOOK_COOKIE_DOMAIN"},
		{"test", "https://radio.test", "LOOK_COOKIE_DOMAIN"},
		{"example.test; Path=/x", "https://radio.example.test", "LOOK_COOKIE_DOMAIN"},
		{"exa_mple.test", "https://radio.exa_mple.test", "LOOK_COOKIE_DOMAIN"},
		{"-example.test", "https://radio.-example.test", "LOOK_COOKIE_DOMAIN"},
		{"example..test", "https://radio.example..test", "LOOK_COOKIE_DOMAIN"},
		{"example.test", "radio.example.test", "APP_URL"},
	} {
		if _, err := combinedFromEnv(environ{"LOOK_COOKIE_DOMAIN": c.domain, "APP_URL": c.app}.get); err == nil || !strings.Contains(err.Error(), c.names) {
			t.Errorf("LOOK_COOKIE_DOMAIN %q, APP_URL %q: %v, want an error naming %s", c.domain, c.app, err, c.names)
		}
	}

	// The settings reach the server through the operations' settings.
	ops, err := operationsFromEnv(environ{"PAGERARCHIVE_URL": "https://pager.example.test/", "LOOK_COOKIE_DOMAIN": "example.test", "APP_URL": "https://radio.example.test"}.get)
	if want := (web.Combined{PagerArchiveName: "Pager", PagerArchiveURL: "https://pager.example.test", LookDomain: "example.test", Secure: true}); err != nil || ops.combined != want {
		t.Errorf("operations: %+v, %v; want %+v", ops.combined, err, want)
	}
	if _, err := operationsFromEnv(environ{"PAGERARCHIVE_URL": "pager"}.get); err == nil {
		t.Error("operations took a PAGERARCHIVE_URL that isn't one")
	}
}
