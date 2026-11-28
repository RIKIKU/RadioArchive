package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

const (
	upload = "15:75:26:74:5E:C7:11:23:68:1E:1D:2C:2A:2D:BF:49:C3:1E:83:4B:C7:8B:0B:BF:95:7D:56:0A:60:FA:A8:CB"
	play   = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
)

// ADR 0058: ANDROID_APP_LINKS is package:fingerprint, then more
// fingerprints after commas or semicolons, for that package or another.
func TestParseAppLinks(t *testing.T) {
	const pkg = "io.github.rikiku.radioarchive"
	for _, c := range []struct {
		in   string
		want []AppLink
	}{
		{"", nil},
		{" ; , ", nil},
		{pkg + ":" + upload, []AppLink{{pkg, []string{upload}}}},
		{pkg + ":" + strings.ToLower(upload), []AppLink{{pkg, []string{upload}}}},
		{" " + pkg + ":" + upload + " ; " + play + " ", []AppLink{{pkg, []string{upload, play}}}},
		{pkg + ":" + upload + "," + play, []AppLink{{pkg, []string{upload, play}}}},
		{pkg + ":" + upload + ";" + pkg + ":" + play + ";" + upload, []AppLink{{pkg, []string{upload, play}}}},
		{pkg + ":" + upload + ";io.example.other:" + play, []AppLink{{pkg, []string{upload}}, {"io.example.other", []string{play}}}},
	} {
		got, err := ParseAppLinks(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseAppLinks(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{
		upload,                             // no package
		pkg,                                // no fingerprint
		pkg + ":",                          // an empty one
		pkg + ":" + upload[:len(upload)-3], // too short
		pkg + ":" + upload + ":00",         // too long
		pkg + ":" + strings.ReplaceAll(upload, ":", ""),
		"radioarchive:" + upload, // one part
		"1io.example:" + upload,  // a part starting with a digit
		pkg + ":" + upload + ";junk",
	} {
		if _, err := ParseAppLinks(in); err == nil || !strings.Contains(err.Error(), "ANDROID_APP_LINKS") {
			t.Errorf("ParseAppLinks(%q): %v, want an error naming ANDROID_APP_LINKS", in, err)
		}
	}
}

// A50, ADR 0058: /.well-known/assetlinks.json is Android's statement list,
// JSON, publicly cacheable and never a redirect; with no apps, 404.
func TestAssetLinks(t *testing.T) {
	root := fstest.MapFS{"index.html": {Data: []byte(page)}}
	res, body := get(t, serveMux(Serve(root)), AssetLinksPath)
	if res.StatusCode != 404 || strings.Contains(body, "<html") {
		t.Errorf("unset: %d %q", res.StatusCode, body)
	}
	if res, _ := get(t, serveMux(Serve(root).AppLinks(nil)), AssetLinksPath); res.StatusCode != 404 {
		t.Errorf("none: %d", res.StatusCode)
	}

	ls, err := ParseAppLinks("io.github.rikiku.radioarchive:" + upload + ";" + play)
	if err != nil {
		t.Fatal(err)
	}
	h := serveMux(Serve(root).AppLinks(ls))
	res, body = get(t, h, AssetLinksPath)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/json" ||
		res.Header.Get("Cache-Control") != "public, max-age=3600" {
		t.Fatalf("set: %d %v %q", res.StatusCode, res.Header, body)
	}
	want := `[{"relation":["delegate_permission/common.handle_all_urls"],"target":{"namespace":"android_app",` +
		`"package_name":"io.github.rikiku.radioarchive","sha256_cert_fingerprints":["` + upload + `","` + play + `"]}}]` + "\n"
	if body != want {
		t.Errorf("body\n%s\nwant\n%s", body, want)
	}
	var v any
	if json.Unmarshal([]byte(body), &v) != nil {
		t.Errorf("not JSON: %q", body)
	}
	// HEAD answers too, and nothing near the path redirects to it or
	// serves the app's page in its place.
	r := httptest.NewRequest(http.MethodHead, AssetLinksPath, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("HEAD: %d %v", w.Code, w.Header())
	}
	if res, body := get(t, h, "/.well-known/assetlinks.json/"); res.StatusCode/100 == 3 || res.StatusCode == 200 && body == want {
		t.Errorf("a trailing slash: %d %q", res.StatusCode, body)
	}
}
