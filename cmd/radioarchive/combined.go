package main

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/RIKIKU/RadioArchive/web"
)

// combinedFromEnv reads the settings for running beside PagerArchive
// (SPEC.md R102, R104; ADR 0054):
//
//   - PAGERARCHIVE_URL, PagerArchive's address, which turns on the app
//     switcher, and PAGERARCHIVE_NAME, its name there ("Pager" unless set);
//   - LOOK_COOKIE_DOMAIN, the parent domain the apps share the look cookie
//     on. It needs APP_URL, whose host must be in it, and whose scheme says
//     whether the cookie is Secure.
//
// All unset is standalone, the zero web.Combined. A value that isn't one
// stops the server at start, naming the setting.
func combinedFromEnv(getenv func(string) string) (web.Combined, error) {
	var c web.Combined
	var err error
	if c.PagerArchiveURL, err = pagerArchiveURL(getenv("PAGERARCHIVE_URL")); err != nil {
		return web.Combined{}, err
	}
	if c.PagerArchiveURL != "" {
		c.PagerArchiveName = strings.TrimSpace(getenv("PAGERARCHIVE_NAME"))
		if c.PagerArchiveName == "" {
			c.PagerArchiveName = "Pager"
		}
		if utf8.RuneCountInString(c.PagerArchiveName) > 40 || strings.ContainsAny(c.PagerArchiveName, "\r\n\t") {
			return web.Combined{}, errors.New("PAGERARCHIVE_NAME must be one short line, 40 characters at most, such as Pager")
		}
	}
	domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(getenv("LOOK_COOKIE_DOMAIN"))), ".")
	if domain == "" {
		return c, nil
	}
	if !domainName.MatchString(domain) || !strings.Contains(domain, ".") {
		return web.Combined{}, errors.New("LOOK_COOKIE_DOMAIN must be a domain name, such as example.org")
	}
	app := strings.TrimSpace(getenv("APP_URL"))
	if app == "" {
		return web.Combined{}, errors.New("LOOK_COOKIE_DOMAIN needs APP_URL, the radio's own address, which must be in that domain")
	}
	u, err := url.Parse(app)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return web.Combined{}, errors.New("APP_URL must be an http(s) URL, like https://radio.example.org")
	}
	host := strings.ToLower(u.Hostname())
	if host != domain && !strings.HasSuffix(host, "."+domain) {
		return web.Combined{}, fmt.Errorf("LOOK_COOKIE_DOMAIN (%s) must be APP_URL's host or a parent domain of it (%s)", domain, host)
	}
	c.LookDomain, c.Secure = domain, u.Scheme == "https"
	return c, nil
}

// domainName is a domain name's letters, digits, hyphens and dots, each
// label starting and ending with a letter or digit.
var domainName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// pagerArchiveURL reads PAGERARCHIVE_URL (R102): an absolute http(s) URL
// with a host and no user name, query or fragment, its trailing slash
// dropped; "" when unset.
func pagerArchiveURL(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(v, "?") || strings.HasSuffix(v, "#") {
		return "", errors.New("PAGERARCHIVE_URL must be an absolute http(s) URL, such as https://pager.example.org")
	}
	u.Host = strings.ToLower(u.Host)
	return strings.TrimSuffix(u.String(), "/"), nil
}
