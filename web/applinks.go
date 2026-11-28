package web

// Android App Links (ADR 0058): /.well-known/assetlinks.json tells Android
// which apps may open this host's links, so a radio link PagerArchive opens
// goes to the RadioArchive app when it's installed. Its contents come from
// ANDROID_APP_LINKS; unset, the path answers 404.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// AssetLinksPath is where Android looks for a host's app links.
const AssetLinksPath = "/.well-known/assetlinks.json"

// AppLink is one Android app allowed to open this host's links: its package
// name and the SHA-256 fingerprints of the certificates it's signed with.
type AppLink struct {
	Package      string
	Fingerprints []string
}

var (
	// A package name: two or more dot-separated parts, each starting with
	// a letter (Android's rule).
	packageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)
	// A SHA-256 certificate fingerprint as keytool and Play show it: 32
	// bytes in hex, separated by colons.
	fingerprint = regexp.MustCompile(`^[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){31}$`)
)

// ParseAppLinks reads ANDROID_APP_LINKS: items separated by commas or
// semicolons, each `package:fingerprint`, or a fingerprint alone, which is
// another for the package before it. So
//
//	io.example.app:AA:…:01;BB:…:02
//
// is one app with two signing certificates (an upload key and Play App
// Signing's). Fingerprints are written in upper case, as Android shows
// them. "" is none.
func ParseAppLinks(s string) ([]AppLink, error) {
	var out []AppLink
	for _, item := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		pkg, fp := "", item
		if !fingerprint.MatchString(item) {
			var ok bool
			pkg, fp, ok = strings.Cut(item, ":")
			pkg, fp = strings.TrimSpace(pkg), strings.TrimSpace(fp)
			if !ok || !packageName.MatchString(pkg) {
				return nil, fmt.Errorf("ANDROID_APP_LINKS: %q isn't package:fingerprint, such as io.example.app:AB:CD:…", item)
			}
			if !fingerprint.MatchString(fp) {
				return nil, fmt.Errorf("ANDROID_APP_LINKS: %q isn't a SHA-256 fingerprint: 32 hex pairs separated by colons", fp)
			}
		}
		fp = strings.ToUpper(fp)
		switch {
		case pkg != "":
			i := indexOfPackage(out, pkg)
			if i < 0 {
				out = append(out, AppLink{Package: pkg})
				i = len(out) - 1
			}
			out[i].Fingerprints = appendNew(out[i].Fingerprints, fp)
		case len(out) == 0:
			return nil, errors.New("ANDROID_APP_LINKS must start with the app's package name, such as io.example.app:AB:CD:…")
		default:
			out[len(out)-1].Fingerprints = appendNew(out[len(out)-1].Fingerprints, fp)
		}
	}
	return out, nil
}

func indexOfPackage(ls []AppLink, pkg string) int {
	for i, l := range ls {
		if l.Package == pkg {
			return i
		}
	}
	return -1
}

func appendNew(fps []string, fp string) []string {
	for _, f := range fps {
		if f == fp {
			return fps
		}
	}
	return append(fps, fp)
}

// AppLinks sets the apps /.well-known/assetlinks.json names, and returns
// the app. None leaves it answering 404.
func (a *App) AppLinks(ls []AppLink) *App {
	a.assetLinks = nil
	if len(ls) == 0 {
		return a
	}
	type target struct {
		Namespace    string   `json:"namespace"`
		PackageName  string   `json:"package_name"`
		Fingerprints []string `json:"sha256_cert_fingerprints"`
	}
	type statement struct {
		Relation []string `json:"relation"`
		Target   target   `json:"target"`
	}
	st := make([]statement, 0, len(ls))
	for _, l := range ls {
		st = append(st, statement{Relation: []string{"delegate_permission/common.handle_all_urls"},
			Target: target{Namespace: "android_app", PackageName: l.Package, Fingerprints: l.Fingerprints}})
	}
	b, err := json.Marshal(st)
	if err != nil {
		panic(err) // strings only
	}
	a.assetLinks = append(b, '\n')
	return a
}

// assetLinksFile serves the app links: JSON, cacheable by anyone for an
// hour, never a redirect (Android refuses one), and 404 when there are none.
func (a *App) assetLinksFile(w http.ResponseWriter, r *http.Request) {
	if a.assetLinks == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(a.assetLinks)
}
