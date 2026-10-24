package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/FahadAsad10/snaplink-go/internal/store"
)

func newTestServer(t *testing.T, cfg Config) *httptest.Server {
	t.Helper()
	cfg.BaseURL = "http://snap.test"
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.StaticFS = fstest.MapFS{"index.html": {Data: []byte("<h1>Snaplink</h1>")}}
	ts := httptest.NewServer(New(store.NewMemory(), cfg))
	t.Cleanup(ts.Close)
	return ts
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func post(t *testing.T, ts *httptest.Server, body string) (*http.Response, map[string]any) {
	t.Helper()
	res, err := http.Post(ts.URL+"/api/links", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res, out
}

func TestCreateRedirectAndStats(t *testing.T) {
	ts := newTestServer(t, Config{DisableLimiter: true})

	res, link := post(t, ts, `{"url":"https://go.dev/doc"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status %d: %v", res.StatusCode, link)
	}
	code := link["code"].(string)
	if len(code) != 7 || link["short_url"] != "http://snap.test/"+code {
		t.Fatalf("unexpected link %v", link)
	}

	for range 2 {
		r, err := noRedirect.Get(ts.URL + "/" + code)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusFound || r.Header.Get("Location") != "https://go.dev/doc" {
			t.Fatalf("redirect: %d %s", r.StatusCode, r.Header.Get("Location"))
		}
	}

	r, _ := http.Get(ts.URL + "/api/links/" + code)
	var stats map[string]any
	_ = json.NewDecoder(r.Body).Decode(&stats)
	r.Body.Close()
	if stats["clicks"].(float64) != 2 {
		t.Fatalf("clicks = %v", stats["clicks"])
	}
}

func TestCustomCodeAndConflicts(t *testing.T) {
	ts := newTestServer(t, Config{DisableLimiter: true})
	if res, _ := post(t, ts, `{"url":"https://a.dev","custom_code":"launch"}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", res.StatusCode)
	}
	if res, _ := post(t, ts, `{"url":"https://b.dev","custom_code":"launch"}`); res.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate: %d", res.StatusCode)
	}
	if res, _ := post(t, ts, `{"url":"https://b.dev","custom_code":"metrics"}`); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("reserved: %d", res.StatusCode)
	}
}

func TestValidation(t *testing.T) {
	ts := newTestServer(t, Config{DisableLimiter: true})
	cases := map[string]int{
		`not json`:                                       http.StatusBadRequest,
		`{"url":"https://a.dev","extra":1}`:              http.StatusBadRequest,
		`{"url":""}`:                                     http.StatusUnprocessableEntity,
		`{"url":"ftp://files.dev/x"}`:                    http.StatusUnprocessableEntity,
		`{"url":"javascript:alert(1)"}`:                  http.StatusUnprocessableEntity,
		`{"url":"http://snap.test/loop"}`:                http.StatusUnprocessableEntity,
		`{"url":"https://a.dev","ttl_seconds":-5}`:       http.StatusUnprocessableEntity,
		`{"url":"https://a.dev","custom_code":"no way"}`: http.StatusUnprocessableEntity,
	}
	for body, want := range cases {
		if res, out := post(t, ts, body); res.StatusCode != want {
			t.Errorf("%s: got %d (%v), want %d", body, res.StatusCode, out, want)
		}
	}
}

func TestDeleteAndNotFound(t *testing.T) {
	ts := newTestServer(t, Config{DisableLimiter: true})
	post(t, ts, `{"url":"https://a.dev","custom_code":"gone"}`)
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/links/gone", nil)
	res, _ := http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	r, _ := noRedirect.Get(ts.URL + "/gone")
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("after delete: %d", r.StatusCode)
	}
}

func TestRateLimit(t *testing.T) {
	ts := newTestServer(t, Config{RatePerMinute: 1, RateBurst: 3})
	var codes []int
	for range 5 {
		res, _ := post(t, ts, `{"url":"https://a.dev"}`)
		codes = append(codes, res.StatusCode)
	}
	if codes[2] != http.StatusCreated || codes[3] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v", codes)
	}
}

func TestHealthMetricsAndUI(t *testing.T) {
	ts := newTestServer(t, Config{DisableLimiter: true})
	post(t, ts, `{"url":"https://a.dev"}`)

	for path, want := range map[string]string{
		"/healthz": `"status":"ok"`,
		"/metrics": "snaplink_links_created_total 1",
		"/":        "Snaplink",
	} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(string(body), want) {
			t.Errorf("%s: %d, body missing %q", path, res.StatusCode, want)
		}
	}
}
