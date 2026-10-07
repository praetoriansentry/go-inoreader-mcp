package inoreader

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAPI is a minimal stand-in for the Inoreader API.
type fakeAPI struct {
	t          *testing.T
	srv        *httptest.Server
	refreshes  atomic.Int32
	rejectOnce atomic.Bool // next API call returns 401
	calls      []string
	forms      []string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.refreshes.Add(1)
		if r.Form.Get("grant_type") == "refresh_token" && r.Form.Get("refresh_token") != "rt-old" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"bad refresh"}`))
			return
		}
		if r.Form.Get("grant_type") == "authorization_code" && r.Form.Get("code") != "code123" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-new", "refresh_token": "rt-new", "token_type": "Bearer",
			"scope": "read write", "expires_in": 3600,
		})
	})
	mux.HandleFunc("/reader/api/0/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Reader-Zone1-Limit", "100")
		w.Header().Set("X-Reader-Zone1-Usage", "7")
		w.Header().Set("X-Reader-Zone2-Limit", "100")
		w.Header().Set("X-Reader-Zone2-Usage", "2")
		w.Header().Set("X-Reader-Limits-Reset-After", "1234")
		if ua := r.Header.Get("User-Agent"); ua != "test-agent" {
			f.t.Errorf("unexpected user agent %q", ua)
		}
		if f.rejectOnce.CompareAndSwap(true, false) {
			w.WriteHeader(401)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer at-old" && auth != "Bearer at-new" {
			w.WriteHeader(401)
			return
		}
		_ = r.ParseForm()
		f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
		f.forms = append(f.forms, r.PostForm.Encode())
		ep := strings.TrimPrefix(r.URL.EscapedPath(), "/reader/api/0/")
		switch {
		case ep == "user-info":
			_, _ = w.Write([]byte(`{"userId":"1","userName":"john","userEmail":"j@example.com"}`))
		case ep == "subscription/list":
			_, _ = w.Write([]byte(`{"subscriptions":[{"id":"feed/https://a.example/rss","title":"A","categories":[{"id":"user/1/label/Tech","label":"Tech"}],"sortid":"ABCD","firstitemmsec":1234,"url":"https://a.example/rss","htmlUrl":"https://a.example","iconUrl":""}]}`))
		case ep == "tag/list":
			_, _ = w.Write([]byte(`{"tags":[{"id":"user/1/label/Tech","sortid":"X","type":"folder","unread_count":3}]}`))
		case ep == "unread-count":
			_, _ = w.Write([]byte(`{"max":1000,"unreadcounts":[{"id":"feed/https://a.example/rss","count":3,"newestItemTimestampUsec":"1"}]}`))
		case strings.HasPrefix(ep, "stream/contents/"):
			if ep != "stream/contents/user%2F-%2Fstate%2Fcom.google%2Freading-list" {
				f.t.Errorf("unexpected stream path %q", ep)
			}
			if r.URL.Query().Get("c") == "" {
				_, _ = w.Write([]byte(`{"id":"x","items":[{"id":"tag:google.com,2005:reader/item/00000000148b9369","title":"One","published":2000,"categories":["user/1/state/com.google/read","user/1/label/Tech"],"canonical":[{"href":"https://a.example/1"}],"summary":{"content":"<p>Hi <b>there</b></p>"},"origin":{"streamId":"feed/https://a.example/rss","title":"A"}},{"id":"tag:google.com,2005:reader/item/00000000148b9360","title":"Old","published":10,"categories":[],"summary":{"content":""},"origin":{}}],"continuation":"c2"}`))
			} else {
				_, _ = w.Write([]byte(`{"id":"x","items":[{"id":"tag:google.com,2005:reader/item/00000000148b9361","title":"Two","published":3000,"categories":["user/1/state/com.google/starred"],"summary":{"content":"x"},"origin":{}}]}`))
			}
		case ep == "stream/items/ids":
			_, _ = w.Write([]byte(`{"itemRefs":[{"id":"344691561","directStreamIds":["user/1/label/Tech"],"timestampUsec":"5"}]}`))
		case ep == "stream/items/contents":
			_, _ = w.Write([]byte(`{"items":[{"id":"tag:google.com,2005:reader/item/00000000148b9369","title":"One","summary":{"content":""},"origin":{}}]}`))
		case ep == "subscription/quickadd":
			if r.PostForm.Get("quickadd") == "feed/https://bad.example" {
				_, _ = w.Write([]byte(`{"query":"feed/https://bad.example","numResults":0}`))
				return
			}
			_, _ = w.Write([]byte(`{"query":"feed/https://a.example/rss","numResults":1,"streamId":"feed/https://a.example/rss","streamName":"A"}`))
		case ep == "preference/stream/list":
			_, _ = w.Write([]byte(`{"streamprefs":{"user/1/state/com.google/root":[{"id":"subscription-ordering","value":"ABCD"}]}}`))
		case ep == "mark-all-as-read" && r.PostForm.Get("s") == "feed/https://denied.example":
			w.WriteHeader(403)
			_, _ = w.Write([]byte("Error=Insufficient scope"))
		case ep == "edit-tag" && r.PostForm.Get("i") == "429":
			w.WriteHeader(429)
			_, _ = w.Write([]byte("Daily limit reached"))
		default:
			_, _ = w.Write([]byte("OK"))
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) client(t *testing.T, tok *Token) (*Client, *TokenSource, string) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	cfg := &OAuthConfig{
		ClientID: "id", ClientSecret: "secret", RedirectURI: "http://localhost/cb",
		AuthURL: f.srv.URL + "/oauth2/auth", TokenURL: f.srv.URL + "/oauth2/token",
		HTTPClient: f.srv.Client(),
	}
	ts, err := NewTokenSource(cfg, FileTokenStore{Path: path}, tok)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(ts, WithBaseURL(f.srv.URL), WithHTTPClient(f.srv.Client()), WithUserAgent("test-agent"))
	return c, ts, path
}

func validToken() *Token {
	return &Token{AccessToken: "at-old", RefreshToken: "rt-old", Scope: "read", ExpiresAt: time.Now().Add(time.Hour).Unix()}
}

func TestReadEndpoints(t *testing.T) {
	f := newFakeAPI(t)
	c, _, _ := f.client(t, validToken())
	ctx := context.Background()

	ui, err := c.UserInfo(ctx)
	if err != nil || ui.UserName != "john" {
		t.Fatalf("UserInfo: %v %+v", err, ui)
	}
	rl := c.RateLimit()
	if !rl.HasObservations || rl.Zone1Usage != 7 || rl.Zone2Limit != 100 || rl.ResetAfterSec != 1234 {
		t.Errorf("rate limit not parsed: %+v", rl)
	}
	subs, err := c.Subscriptions(ctx)
	if err != nil || len(subs) != 1 || subs[0].Categories[0].Label != "Tech" {
		t.Fatalf("Subscriptions: %v %+v", err, subs)
	}
	tags, err := c.Tags(ctx)
	if err != nil || len(tags) != 1 || *tags[0].UnreadCount != 3 {
		t.Fatalf("Tags: %v %+v", err, tags)
	}
	uc, err := c.UnreadCounts(ctx)
	if err != nil || uc.Max != 1000 {
		t.Fatalf("UnreadCounts: %v", err)
	}
	sc, err := c.StreamContents(ctx, "", StreamOptions{Count: 500, ExcludeRead: true, NewerThan: time.Unix(100, 0)})
	if err != nil || len(sc.Items) != 2 || sc.Continuation != "c2" {
		t.Fatalf("StreamContents: %v", err)
	}
	last := f.calls[len(f.calls)-1]
	for _, want := range []string{"n=100", "xt=user%2F-%2Fstate%2Fcom.google%2Fread", "ot=100", "output=json"} {
		if !strings.Contains(last, want) {
			t.Errorf("query %q missing %q", last, want)
		}
	}
	it := sc.Items[0]
	if !it.IsRead() || it.IsStarred() || it.URL() != "https://a.example/1" || it.Labels()[0] != "Tech" {
		t.Errorf("item helpers wrong: %+v", it)
	}
	ids, err := c.StreamItemIDs(ctx, "user/-/label/Tech", StreamOptions{Count: 5000})
	if err != nil || len(ids.ItemRefs) != 1 {
		t.Fatalf("StreamItemIDs: %v", err)
	}
	if last := f.calls[len(f.calls)-1]; !strings.Contains(last, "n=1000") || !strings.Contains(last, "s=user%2F-%2Flabel%2FTech") {
		t.Errorf("ids query: %s", last)
	}
	items, err := c.ItemsByID(ctx, []string{"344691561"})
	if err != nil || len(items) != 1 {
		t.Fatalf("ItemsByID: %v", err)
	}
	if form := f.forms[len(f.forms)-1]; form != "i=tag%3Agoogle.com%2C2005%3Areader%2Fitem%2F00000000148b9369" {
		t.Errorf("ItemsByID form: %s", form)
	}
	prefs, err := c.StreamPreferences(ctx)
	if err != nil || len(prefs.StreamPrefs) != 1 {
		t.Fatalf("StreamPreferences: %v", err)
	}
}

func TestFetchRecent(t *testing.T) {
	f := newFakeAPI(t)
	c, _, _ := f.client(t, validToken())
	items, truncated, err := c.FetchRecent(context.Background(), "", time.Unix(100, 0), StreamOptions{}, 500, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || truncated {
		t.Fatalf("got %d items truncated=%v", len(items), truncated)
	}
	// Second page used the continuation.
	if !strings.Contains(f.calls[1], "c=c2") {
		t.Errorf("continuation not sent: %v", f.calls)
	}
	items, truncated, err = c.FetchRecent(context.Background(), "", time.Unix(100, 0), StreamOptions{}, 1, 10)
	if err != nil || len(items) != 1 || !truncated {
		t.Fatalf("maxItems: %d %v %v", len(items), truncated, err)
	}
}

func TestWriteEndpoints(t *testing.T) {
	f := newFakeAPI(t)
	c, _, _ := f.client(t, validToken())
	ctx := context.Background()
	lastForm := func() string { return f.forms[len(f.forms)-1] }

	if err := c.MarkRead(ctx, []string{"tag:google.com,2005:reader/item/00000000148b9369", "5"}); err != nil {
		t.Fatal(err)
	}
	if got := lastForm(); got != "a=user%2F-%2Fstate%2Fcom.google%2Fread&i=344691561&i=5" {
		t.Errorf("MarkRead form: %s", got)
	}
	if err := c.Unstar(ctx, []string{"5"}); err != nil {
		t.Fatal(err)
	}
	if got := lastForm(); got != "i=5&r=user%2F-%2Fstate%2Fcom.google%2Fstarred" {
		t.Errorf("Unstar form: %s", got)
	}
	if err := c.EditTags(ctx, []string{"5"}, nil, nil); err == nil {
		t.Error("EditTags with nothing to do should fail")
	}
	if err := c.MarkAllAsRead(ctx, "user/-/label/Tech", time.Unix(42, 0)); err != nil {
		t.Fatal(err)
	}
	if got := lastForm(); got != "s=user%2F-%2Flabel%2FTech&ts=42" {
		t.Errorf("MarkAllAsRead form: %s", got)
	}
	res, err := c.QuickAdd(ctx, "https://a.example/rss")
	if err != nil || res.StreamName != "A" {
		t.Fatalf("QuickAdd: %v", err)
	}
	if _, err := c.QuickAdd(ctx, "https://bad.example"); err == nil {
		t.Error("QuickAdd of bad feed should fail")
	}
	if err := c.EditSubscription(ctx, SubscriptionEdit{StreamID: "https://a.example/rss", Title: "New", AddFolder: "Tech", RemoveFolder: "Old"}); err != nil {
		t.Fatal(err)
	}
	if got := lastForm(); got != "a=user%2F-%2Flabel%2FTech&ac=edit&r=user%2F-%2Flabel%2FOld&s=feed%2Fhttps%3A%2F%2Fa.example%2Frss&t=New" {
		t.Errorf("EditSubscription form: %s", got)
	}
	if err := c.Subscribe(ctx, "https://a.example/rss", "", "Tech"); err != nil {
		t.Fatal(err)
	}
	if err := c.Unsubscribe(ctx, "feed/https://a.example/rss"); err != nil {
		t.Fatal(err)
	}
	if got := lastForm(); got != "ac=unsubscribe&s=feed%2Fhttps%3A%2F%2Fa.example%2Frss" {
		t.Errorf("Unsubscribe form: %s", got)
	}
	if err := c.RenameTag(ctx, "Tech", "Technology"); err != nil {
		t.Fatal(err)
	}
	if err := c.RenameTag(ctx, "Tech", "a/b"); err == nil {
		t.Error("RenameTag with slash should fail")
	}
	if err := c.DeleteTag(ctx, "Tech"); err != nil {
		t.Fatal(err)
	}
	if got := lastForm(); got != "s=user%2F-%2Flabel%2FTech" {
		t.Errorf("DeleteTag form: %s", got)
	}
	if err := c.SetStreamOrdering(ctx, StreamRoot, "ABCDEFGH"); err != nil {
		t.Fatal(err)
	}

	// Error mapping.
	err = c.MarkAllAsRead(ctx, "feed/https://denied.example", time.Time{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !errors.Is(err, ErrForbidden) || apiErr.Message != "Insufficient scope" {
		t.Errorf("403 mapping: %v", err)
	}
	err = c.MarkRead(ctx, []string{"429"})
	if !errors.Is(err, ErrRateLimited) || !strings.Contains(err.Error(), "resets in") {
		t.Errorf("429 mapping: %v", err)
	}
}

func TestTokenRefreshAndPersist(t *testing.T) {
	f := newFakeAPI(t)
	expired := validToken()
	expired.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	c, ts, path := f.client(t, expired)

	if _, err := c.UserInfo(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.refreshes.Load() != 1 {
		t.Errorf("expected 1 refresh, got %d", f.refreshes.Load())
	}
	cur := ts.Current()
	if cur.AccessToken != "at-new" || cur.RefreshToken != "rt-new" || !cur.HasScope("write") {
		t.Errorf("token not updated: %+v", cur)
	}
	// Persisted with restrictive permissions.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("token file mode %o, want 600", st.Mode().Perm())
	}
	saved, err := FileTokenStore{Path: path}.Load()
	if err != nil || saved.RefreshToken != "rt-new" {
		t.Errorf("saved token: %+v %v", saved, err)
	}
	// Not refreshed again while valid.
	if _, err := c.UserInfo(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.refreshes.Load() != 1 {
		t.Errorf("unexpected extra refresh")
	}
}

func TestRetryOn401(t *testing.T) {
	f := newFakeAPI(t)
	c, _, _ := f.client(t, validToken())
	f.rejectOnce.Store(true)
	if _, err := c.UserInfo(context.Background()); err != nil {
		t.Fatalf("expected retry after 401 to succeed: %v", err)
	}
	if f.refreshes.Load() != 1 {
		t.Errorf("expected forced refresh, got %d", f.refreshes.Load())
	}
}

func TestRefreshFailure(t *testing.T) {
	f := newFakeAPI(t)
	tok := validToken()
	tok.RefreshToken = "rt-bad"
	tok.ExpiresAt = 0
	c, _, _ := f.client(t, tok)
	_, err := c.UserInfo(context.Background())
	var oe *OAuthError
	if !errors.As(err, &oe) || !strings.Contains(err.Error(), "bad refresh") {
		t.Errorf("expected OAuthError, got %v", err)
	}
}

func TestNoTokenAtAll(t *testing.T) {
	f := newFakeAPI(t)
	c, _, _ := f.client(t, nil)
	if _, err := c.UserInfo(context.Background()); err == nil || !strings.Contains(err.Error(), "auth login") {
		t.Errorf("expected not-authenticated error, got %v", err)
	}
}

func TestExchangeAndAuthURL(t *testing.T) {
	f := newFakeAPI(t)
	cfg := &OAuthConfig{ClientID: "id", ClientSecret: "s", RedirectURI: "http://localhost:8080/callback",
		AuthURL: f.srv.URL + "/oauth2/auth", TokenURL: f.srv.URL + "/oauth2/token", HTTPClient: f.srv.Client()}
	u := cfg.AuthCodeURL("st", ScopeReadWrite)
	for _, want := range []string{"client_id=id", "response_type=code", "scope=read+write", "state=st", "redirect_uri=http%3A%2F%2Flocalhost%3A8080%2Fcallback"} {
		if !strings.Contains(u, want) {
			t.Errorf("auth url %q missing %q", u, want)
		}
	}
	tok, err := cfg.Exchange(context.Background(), "code123")
	if err != nil || tok.AccessToken != "at-new" || tok.ExpiresAt <= time.Now().Unix() {
		t.Fatalf("Exchange: %v %+v", err, tok)
	}
	if _, err := cfg.Exchange(context.Background(), "nope"); err == nil {
		t.Error("bad code should fail")
	}
	st1, _ := NewState()
	st2, _ := NewState()
	if st1 == st2 || len(st1) < 32 {
		t.Error("NewState not random")
	}
}

func TestFileTokenStoreCheckWritable(t *testing.T) {
	dir := t.TempDir()
	if err := (FileTokenStore{Path: filepath.Join(dir, "sub", "tokens.json")}).CheckWritable(); err != nil {
		t.Errorf("writable dir: %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := (FileTokenStore{Path: filepath.Join(ro, "tokens.json")}).CheckWritable(); err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Errorf("read-only dir should fail: %v", err)
	}
}

func TestFileTokenStoreMissing(t *testing.T) {
	_, err := FileTokenStore{Path: filepath.Join(t.TempDir(), "nope.json")}.Load()
	if !errors.Is(err, ErrNoToken) {
		t.Errorf("want ErrNoToken, got %v", err)
	}
}
