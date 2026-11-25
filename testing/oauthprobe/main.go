// Command oauthprobe checks what the PowerShell module's sign-in relies on
// in the Authentik the compose stack runs (ADR 0028), before anything is
// built on it: PKCE with a loopback redirect on any port, the access
// token's claims, refresh tokens replaced at each use, revocation, and the
// device code. It signs in through Authentik's flow executor API, as a
// browser's pages would, with an account made beforehand
// (scripts/oauth-probe.sh). It prints claim names and checks, never a
// token.
//
//	go run ./testing/oauthprobe -authentik http://localhost:9000 -email a@example.test -password ... [-device-email b@example.test -device-password ...]
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

const (
	client = "radioarchive-powershell"
	scope  = "openid email profile offline_access"
)

var (
	ak     string
	failed int
)

func say(format string, a ...any) { fmt.Printf(format+"\n", a...) }

func check(name string, ok bool, detail string) {
	mark := "PASS"
	if !ok {
		mark = "FAIL"
		failed++
	}
	if detail != "" {
		say("%s %s  (%s)", mark, name, detail)
	} else {
		say("%s %s", mark, name)
	}
}

// browser drives Authentik's flows through the flow executor API, as its
// pages do, with its own cookies.
type browser struct {
	c               *http.Client
	email, password string
}

func newBrowser(email, password string) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{c: &http.Client{Jar: jar, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, email: email, password: password}
}

func (b *browser) executor(slug, query string, body map[string]any) (map[string]any, error) {
	u := ak + "/api/v3/flows/executor/" + slug + "/?query=" + url.QueryEscape(query)
	for range 5 {
		var req *http.Request
		if body == nil {
			req, _ = http.NewRequest("GET", u, nil)
		} else {
			raw, _ := json.Marshal(body)
			req, _ = http.NewRequest("POST", u, strings.NewReader(string(raw)))
			req.Header.Set("Content-Type", "application/json")
			au, _ := url.Parse(ak)
			for _, c := range b.c.Jar.Cookies(au) {
				if c.Name == "authentik_csrf" {
					req.Header.Set("X-authentik-CSRF", c.Value)
				}
			}
		}
		req.Header.Set("Accept", "application/json")
		resp, err := b.c.Do(req)
		if err != nil {
			return nil, err
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusFound {
			body = nil
			continue
		}
		var c map[string]any
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("executor %s: %d", slug, resp.StatusCode)
		}
		return c, nil
	}
	return nil, errors.New("executor loop")
}

func (b *browser) answer(c map[string]any, extra map[string]map[string]any) map[string]any {
	comp, _ := c["component"].(string)
	switch {
	case comp == "ak-stage-identification":
		return map[string]any{"uid_field": b.email}
	case comp == "ak-stage-password":
		return map[string]any{"password": b.password}
	case extra[comp] != nil:
		return extra[comp]
	}
	return nil
}

// walk follows redirects and runs flows from start, and returns the first
// URL on stopHost, or where it stopped ("AT <component>" or "STOPPED
// <status>").
func (b *browser) walk(start, stopHost string, extra map[string]map[string]any) (string, error) {
	next := start
	for range 40 {
		u, err := url.Parse(next)
		if err != nil {
			return "", err
		}
		if u.Host == stopHost {
			return next, nil
		}
		if strings.HasPrefix(u.Path, "/if/flow/") {
			slug := strings.Split(u.Path, "/")[3]
			c, err := b.executor(slug, u.RawQuery, nil)
			if err != nil {
				return "", err
			}
			for c["component"] != "xak-flow-redirect" {
				body := b.answer(c, extra)
				if body == nil {
					return fmt.Sprintf("AT %v", c["component"]), nil
				}
				body["component"] = c["component"]
				if c, err = b.executor(slug, u.RawQuery, body); err != nil {
					return "", err
				}
			}
			to, _ := c["to"].(string)
			ref, _ := url.Parse(to)
			next = u.ResolveReference(ref).String()
			continue
		}
		resp, err := b.c.Get(next)
		if err != nil {
			return "", err
		}
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || loc == "" {
			return fmt.Sprintf("STOPPED %d", resp.StatusCode), nil
		}
		ref, _ := url.Parse(loc)
		next = u.ResolveReference(ref).String()
	}
	return "", errors.New("too many redirects")
}

func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func challenge(v string) string {
	s := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(s[:])
}

// authorize asks for a code for redirect, with a PKCE challenge unless
// method is "", and returns the code, or where the sign-in stopped.
func (b *browser) authorize(redirect, verifier, method string) (code, stopped string) {
	q := url.Values{"client_id": {client}, "response_type": {"code"}, "redirect_uri": {redirect}, "scope": {scope}, "state": {"probe-state"}}
	switch method {
	case "S256":
		q.Set("code_challenge", challenge(verifier))
		q.Set("code_challenge_method", "S256")
	case "plain":
		q.Set("code_challenge", verifier)
		q.Set("code_challenge_method", "plain")
	}
	ru, _ := url.Parse(redirect)
	end, err := b.walk(ak+"/application/o/authorize/?"+q.Encode(), ru.Host, nil)
	if err != nil {
		return "", err.Error()
	}
	if !strings.HasPrefix(end, "http") {
		return "", end
	}
	eu, _ := url.Parse(end)
	if c := eu.Query().Get("code"); c != "" {
		return c, ""
	}
	return "", "back without a code: " + eu.Query().Get("error")
}

// token posts to the token endpoint: the status, the reply's fields.
func token(form url.Values) (int, map[string]any) {
	form.Set("client_id", client)
	resp, err := http.PostForm(ak+"/application/o/token/", form)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func claims(jwt string) map[string]any {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c map[string]any
	_ = json.Unmarshal(raw, &c)
	return c
}

func keys(m map[string]any) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

func main() {
	flag.StringVar(&ak, "authentik", "http://localhost:9000", "Authentik's URL")
	email := flag.String("email", "", "an active user's email")
	password := flag.String("password", "", "their password")
	devEmail := flag.String("device-email", "", "another user, for the device code")
	devPassword := flag.String("device-password", "", "their password")
	expectPKCE := flag.Bool("expect-pkce-policy", true, "the blueprint's PKCE policy is bound (false: show what Authentik does without it)")
	flag.Parse()
	if *email == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "oauthprobe: -email and -password are needed")
		os.Exit(2)
	}
	b := newBrowser(*email, *password)

	say("== E1 PKCE with a loopback redirect on a free port (RFC 8252)")
	redirect := "http://127.0.0.1:43226/"
	v := random()
	code, stopped := b.authorize(redirect, v, "S256")
	check("a code is issued to "+redirect, code != "", stopped)
	st, out := token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {random()}})
	check("the exchange with the wrong verifier is refused", st == 400, fmt.Sprintf("%d %s", st, str(out, "error")))
	code, _ = b.authorize(redirect, v, "S256")
	st, out = token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {v}})
	tokens := out
	check("the exchange with the right verifier gives tokens", st == 200 && str(out, "refresh_token") != "",
		fmt.Sprintf("%d, expires_in=%v, a refresh token: %v", st, out["expires_in"], str(out, "refresh_token") != ""))
	st, out = token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {v}})
	check("the same code a second time is refused", st == 400, fmt.Sprintf("%d %s", st, str(out, "error")))
	code, stopped = b.authorize("http://127.0.0.1:53480/", v, "S256")
	check("another port (53480) is accepted too", code != "", stopped)
	for _, bad := range []string{"http://localhost:43226/", "http://127.0.0.1.example.test:43226/", "https://example.test/", "http://127.0.0.1:43226/x"} {
		code, stopped = b.authorize(bad, v, "S256")
		check("redirect "+bad+" is refused", code == "", stopped)
	}
	code, stopped = b.authorize(redirect, v, "")
	if *expectPKCE {
		check("authorize without a code_challenge is refused (the blueprint's policy)", code == "", stopped)
	} else {
		st, out = token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}})
		say("WITHOUT THE POLICY: authorize without a code_challenge: code issued %v (%s); exchange without a verifier: %d %s, tokens %v",
			code != "", stopped, st, str(out, "error"), str(out, "access_token") != "")
	}
	code, stopped = b.authorize(redirect, v, "plain")
	if *expectPKCE {
		check("authorize with the plain method is refused", code == "", stopped)
	}

	say("\n== E2 the access token and the ID token")
	at, idt := claims(str(tokens, "access_token")), claims(str(tokens, "id_token"))
	say("  access token claims: %v", keys(at))
	say("  id token claims:     %v", keys(idt))
	say("  access token: iss=%v aud=%v azp=%v scope=%q exp-iat=%v", at["iss"], at["aud"], at["azp"], at["scope"],
		toInt(at["exp"])-toInt(at["iat"]))
	say("  id token:     iss=%v aud=%v azp=%v scope=%v", idt["iss"], idt["aud"], idt["azp"], idt["scope"])
	check("the access token's issuer is the module's application", at["iss"] == ak+"/application/o/"+client+"/", "")
	check("its audience is the module's client", at["aud"] == client, "")
	check("its azp is the module's client, and it has a scope", at["azp"] == client && at["scope"] != nil, "")
	_, idScope := idt["scope"]
	check("the ID token has no scope (how the API tells them apart)", !idScope, "")
	check("its lifetime is 5 minutes", toInt(at["exp"])-toInt(at["iat"]) == 300, "")

	say("\n== E3 a refresh replaces the refresh token (threshold 0)")
	old := str(tokens, "refresh_token")
	st, out = token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old}})
	check("refresh gives a new access token and a new refresh token", st == 200 && str(out, "refresh_token") != "" && str(out, "refresh_token") != old, fmt.Sprint(st))
	renewed := str(out, "refresh_token")
	st, out = token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old}})
	check("the old refresh token is refused after it was replaced", st == 400, fmt.Sprintf("%d %s", st, str(out, "error")))

	say("\n== E6 revocation (RFC 7009), a public client with no secret")
	resp, err := http.PostForm(ak+"/application/o/revoke/", url.Values{"token": {renewed}, "token_type_hint": {"refresh_token"}, "client_id": {client}})
	if err == nil {
		resp.Body.Close()
		check("revoke answers 200", resp.StatusCode == 200, fmt.Sprint(resp.StatusCode))
	} else {
		check("revoke answers", false, err.Error())
	}
	st, out = token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {renewed}})
	check("a revoked refresh token is refused", st == 400, fmt.Sprintf("%d %s", st, str(out, "error")))

	if *devEmail != "" {
		say("\n== E7 device code (RFC 8628)")
		resp, err := http.PostForm(ak+"/application/o/device/", url.Values{"client_id": {client}, "scope": {scope}})
		var d map[string]any
		if err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&d)
			resp.Body.Close()
		}
		check("device authorization gives a user code and a verification URI", str(d, "user_code") != "",
			fmt.Sprintf("verification_uri=%v, expires_in=%v, interval=%v", d["verification_uri"], d["expires_in"], d["interval"]))
		st, out = token(url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {str(d, "device_code")}})
		check("polling before the code is entered: authorization_pending", st == 400 && str(out, "error") == "authorization_pending", fmt.Sprintf("%d %s", st, str(out, "error")))
		other := newBrowser(*devEmail, *devPassword)
		end, err := other.walk(str(d, "verification_uri"), "never.invalid", map[string]map[string]any{
			"ak-provider-oauth2-device-code": {"code": str(d, "user_code")}})
		say("  the other device's browser ended at: %s %v", end, err)
		st, out = token(url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {str(d, "device_code")}})
		check("after the sign-in and the code: tokens, with a refresh token", st == 200 && str(out, "refresh_token") != "", fmt.Sprint(st))
		check("the device sign-in's subject is that user's ID", claims(str(out, "access_token"))["sub"] != at["sub"], "")
		st, out = token(url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {str(d, "device_code")}})
		check("the device code can't be used twice", st != 200, fmt.Sprintf("%d %s", st, str(out, "error")))
	}

	say("\n%d failed", failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func toInt(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}
