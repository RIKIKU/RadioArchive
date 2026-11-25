package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/account"
	"github.com/RIKIKU/RadioArchive/internal/authentik"
	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/oidcclient"
	"github.com/RIKIKU/RadioArchive/internal/store"
)

// signInConfig is listeners' sign-in as the environment sets it (ADR 0009).
// The zero value is sign-in off.
type signInConfig struct {
	appURL, authentikURL, internalURL *url.URL
	clientID, clientSecret            string
	// initialAdmin is INITIAL_ADMIN_EMAIL: the first admin's address
	// (ADR 0025), "" for none.
	initialAdmin string
}

func (c signInConfig) on() bool { return c.authentikURL != nil }

// String never shows the secret: a config printed by mistake
// leaks nothing.
func (c signInConfig) String() string {
	if !c.on() {
		return "sign-in off"
	}
	return fmt.Sprintf("sign-in via %s as %s", c.authentikURL, c.clientID)
}

// signInFromEnv reads the settings, refusing a half-set one: with
// AUTHENTIK_URL set, the others are needed too.
func signInFromEnv(getenv func(string) string) (signInConfig, error) {
	var c signInConfig
	parse := func(name string) (*url.URL, error) {
		v := strings.TrimSpace(getenv(name))
		if v == "" {
			return nil, nil
		}
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			return nil, fmt.Errorf("%s must be an http(s) URL with no path, like https://radio.example.org", name)
		}
		u.Path = ""
		return u, nil
	}
	var err error
	if c.authentikURL, err = parse("AUTHENTIK_URL"); err != nil || c.authentikURL == nil {
		return signInConfig{}, err
	}
	if c.appURL, err = parse("APP_URL"); err != nil {
		return signInConfig{}, err
	}
	if c.appURL == nil {
		return signInConfig{}, errors.New("APP_URL is not set, and listeners' sign-in needs it (AUTHENTIK_URL is set)")
	}
	if c.internalURL, err = parse("AUTHENTIK_INTERNAL_URL"); err != nil {
		return signInConfig{}, err
	}
	c.clientID = strings.TrimSpace(getenv("OIDC_CLIENT_ID"))
	if c.clientID == "" {
		c.clientID = "radioarchive"
	}
	if c.clientSecret, err = secret(getenv, "OIDC_CLIENT_SECRET"); err != nil {
		return signInConfig{}, err
	}
	c.initialAdmin = strings.TrimSpace(getenv("INITIAL_ADMIN_EMAIL"))
	if c.initialAdmin != "" && (!strings.Contains(c.initialAdmin, "@") || strings.ContainsAny(c.initialAdmin, " \t\r\n")) {
		return signInConfig{}, errors.New("INITIAL_ADMIN_EMAIL must be one email address, like owner@example.org")
	}
	return c, nil
}

// secret is NAME's value, or the contents of the file NAME_FILE names
// (a Docker or systemd secret), trimmed. Never both, and never empty.
func secret(getenv func(string) string, name string) (string, error) {
	v, file := getenv(name), getenv(name+"_FILE")
	switch {
	case v != "" && file != "":
		return "", fmt.Errorf("set %s or %s_FILE, not both", name, name)
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			// The path is the operator's; the contents are never shown.
			return "", fmt.Errorf("%s_FILE: can't read %s", name, file)
		}
		v = string(b)
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return "", fmt.Errorf("%s is not set, and listeners' sign-in needs it (AUTHENTIK_URL is set)", name)
	}
	return v, nil
}

// service is the account the settings describe, keeping signed-in
// listeners' sessions, profiles and accounts in the store (ADR 0022, ADR
// 0021, ADR 0025).
func (c signInConfig) service(g *gate.Gate, st *store.Store, log *slog.Logger) (*account.Service, error) {
	if !c.on() {
		return account.New(account.Config{Log: log})
	}
	hc := &http.Client{Transport: authentik.Transport(c.authentikURL, c.internalURL, nil), Timeout: 30 * time.Second}
	return account.New(account.Config{
		AppURL: c.appURL,
		OIDC: &oidcclient.Client{
			Issuer:       c.authentikURL.JoinPath("application", "o", c.clientID).String() + "/",
			ClientID:     c.clientID,
			ClientSecret: c.clientSecret,
			RedirectURL:  c.appURL.JoinPath("auth", "callback").String(),
			HTTP:         hc,
		},
		Sessions:     st,
		Profiles:     st,
		Accounts:     st,
		Catalogue:    g,
		InitialAdmin: c.initialAdmin,
		Secret:       []byte(c.clientSecret),
		Log:          log,
	})
}
