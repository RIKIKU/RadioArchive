package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"
	"time"
)

// The changes an admin makes, through the binary's admin API (ADR 0025):
// a talkgroup's state, delay, alert or LED colour (ADR 0012), and the
// retention period (ADR 0018). The fixture signs in as the fixture's admin
// first, as a browser would, through the made-up Authentik the browser
// tests run (testing/fakeauthentik), which makes INITIAL_ADMIN_EMAIL the
// first admin.

// adminSession signs in to the API at base and returns the browser, with
// its session cookie.
func adminSession(ctx context.Context, base string) (*http.Client, error) {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	// /auth/signin -> Authentik's /authorize -> /auth/callback -> the app.
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/auth/signin?return=/", nil)
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sign in: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if q := resp.Request.URL.Query().Get("signin"); q != "" {
		return nil, fmt.Errorf("sign in: the radio said %q", q)
	}
	return c, nil
}

// admin sends one admin request, as the app's page would, and fails unless
// it's answered 2xx.
func admin(ctx context.Context, c *http.Client, base, method, path string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, method, base+"/api/v1/admin"+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s = %d %s", method, path, resp.StatusCode, bytes.TrimSpace(out))
	}
	return nil
}

// changeTalkgroup sets one of system EX's talkgroups' state, delay, alert
// or LED colour ("null" for an alert or colour of none of its own), and
// prints the moment the request was sent, in Unix milliseconds: the change
// committed after it.
func changeTalkgroup(ctx context.Context, base, id, field, value string) error {
	tg, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("talkgroup %q isn't a number", id)
	}
	orNull := func(v string) any {
		if v == "null" {
			return nil
		}
		return v
	}
	var body map[string]any
	switch field {
	case "state":
		body = map[string]any{"state": value}
	case "delay":
		m, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("delay %q isn't a number of minutes", value)
		}
		body = map[string]any{"delay_minutes": m}
	case "alert":
		body = map[string]any{"alert": orNull(value)}
	case "led":
		body = map[string]any{"led_colour": orNull(value)}
	default:
		return fmt.Errorf("can't change a talkgroup's %q: state, delay, alert or led", field)
	}
	c, err := adminSession(ctx, base)
	if err != nil {
		return err
	}
	sent := time.Now().UnixMilli()
	if err := admin(ctx, c, base, "PATCH", fmt.Sprintf("/systems/1/talkgroups/%d", tg), body); err != nil {
		return err
	}
	fmt.Println(sent)
	return nil
}

// setRetention sets the retention period in days, as an admin does in
// Settings (R88).
func setRetention(ctx context.Context, base, days string) error {
	n, err := strconv.Atoi(days)
	if err != nil {
		return fmt.Errorf("%q isn't a number of days", days)
	}
	c, err := adminSession(ctx, base)
	if err != nil {
		return err
	}
	return admin(ctx, c, base, "PUT", "/retention", map[string]any{"days": n, "confirm": true})
}

// apiURL says whether s is the API's URL rather than the database's.
func apiURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
