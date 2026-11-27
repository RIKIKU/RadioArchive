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
	// moduleClientID is MODULE_CLIENT_ID: the PowerShell module's own
	// public client (ADR 0028), radioarchive-powershell unless set.
	moduleClientID string
	// appClientID is APP_CLIENT_ID: the Android app's own public client
	// (ADR 0052), radioarchive-android unless set.
	appClientID string
	// initialAdmin is INITIAL_ADMIN_EMAIL: the first admin's address
	// (ADR 0025), "" for none.
	initialAdmin string
	// apiToken is AUTHENTIK_API_TOKEN, or the file AUTHENTIK_API_TOKEN_FILE
	// names: the radio's accounts service account's token (ADR 0033), for
	// switching users off and on and deleting them. "" for none. Sent only
	// to Authentik, never logged.
	apiToken string
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
	c.moduleClientID = strings.TrimSpace(getenv("MODULE_CLIENT_ID"))
	if c.moduleClientID == "" {
		c.moduleClientID = "radioarchive-powershell"
	}
	if c.moduleClientID == c.clientID {
		return signInConfig{}, errors.New("MODULE_CLIENT_ID must be the PowerShell module's own client, not OIDC_CLIENT_ID's")
	}
	c.appClientID = strings.TrimSpace(getenv("APP_CLIENT_ID"))
	if c.appClientID == "" {
		c.appClientID = "radioarchive-android"
	}
	if c.appClientID == c.clientID || c.appClientID == c.moduleClientID {
		return signInConfig{}, errors.New("APP_CLIENT_ID must be the Android app's own client, not OIDC_CLIENT_ID's or MODULE_CLIENT_ID's")
	}
	if c.clientSecret, err = secret(getenv, "OIDC_CLIENT_SECRET"); err != nil {
		return signInConfig{}, err
	}
	c.initialAdmin = strings.TrimSpace(getenv("INITIAL_ADMIN_EMAIL"))
	if c.initialAdmin != "" && (!strings.Contains(c.initialAdmin, "@") || strings.ContainsAny(c.initialAdmin, " \t\r\n")) {
		return signInConfig{}, errors.New("INITIAL_ADMIN_EMAIL must be one email address, like owner@example.org")
	}
	if getenv("AUTHENTIK_API_TOKEN") != "" || getenv("AUTHENTIK_API_TOKEN_FILE") != "" {
		if c.apiToken, err = secret(getenv, "AUTHENTIK_API_TOKEN"); err != nil {
			return signInConfig{}, err
		}
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
	// Disabling, enabling and deleting reach Authentik with the accounts
	// service account's token (ADR 0033). Without one they change the
	// radio alone, and say so.
	var dir account.Directory
	if c.apiToken != "" {
		dir = &authentik.Users{URL: c.authentikURL, Token: c.apiToken, HTTP: hc}
	} else {
		log.Warn("AUTHENTIK_API_TOKEN isn't set: disabling, enabling and deleting an account change the radio only, and the admin is told Authentik wasn't")
	}
	return account.New(account.Config{
		AppURL: c.appURL,
		OIDC: &oidcclient.Client{
			Issuer:       c.authentikURL.JoinPath("application", "o", c.clientID).String() + "/",
			ClientID:     c.clientID,
			ClientSecret: c.clientSecret,
			RedirectURL:  c.appURL.JoinPath("auth", "callback").String(),
			HTTP:         hc,
		},
		Sessions:  st,
		Profiles:  st,
		Accounts:  st,
		Catalogue: g,
		// The module's own client (ADR 0028), on the same Authentik, so
		// its issuer is the module application's.
		Module: &oidcclient.AccessVerifier{
			Issuer:   c.authentikURL.JoinPath("application", "o", c.moduleClientID).String() + "/",
			ClientID: c.moduleClientID,
			HTTP:     hc,
		},
		// The Android app's own client (ADR 0052), on the same Authentik.
		App: &oidcclient.AccessVerifier{
			Issuer:   c.authentikURL.JoinPath("application", "o", c.appClientID).String() + "/",
			ClientID: c.appClientID,
			HTTP:     hc,
		},
		InitialAdmin: c.initialAdmin,
		Secret:       []byte(c.clientSecret),
		Log:          log,
		Directory:    dir,
		// The standalone deployment's Authentik is the radio's own, so
		// deleting an account deletes its user there too. (The combined
		// deployment, M7, leaves PagerArchive's users.)
		DeleteAuthentikUsers: true,
	})
}
