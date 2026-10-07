package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/praetoriansentry/go-inoreader-mcp/internal/inoreader"
)

type fixture struct {
	t     *testing.T
	api   *httptest.Server
	calls []string
	forms []string
	sess  *mcp.ClientSession
}

func newFixture(t *testing.T, readOnly bool) *fixture {
	f := &fixture{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/reader/api/0/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Reader-Zone1-Limit", "100")
		w.Header().Set("X-Reader-Zone1-Usage", "3")
		w.Header().Set("X-Reader-Zone2-Limit", "100")
		w.Header().Set("X-Reader-Zone2-Usage", "1")
		w.Header().Set("X-Reader-Limits-Reset-After", "3600")
		_ = r.ParseForm()
		f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
		f.forms = append(f.forms, r.PostForm.Encode())
		ep := strings.TrimPrefix(r.URL.EscapedPath(), "/reader/api/0/")
		switch {
		case ep == "user-info":
			_, _ = w.Write([]byte(`{"userId":"1","userName":"john","userEmail":"j@example.com","signupTimeSec":1528205161}`))
		case ep == "subscription/list":
			_, _ = w.Write([]byte(`{"subscriptions":[
			  {"id":"feed/https://b.example/rss","title":"Bravo","categories":[],"url":"https://b.example/rss","htmlUrl":"https://b.example"},
			  {"id":"feed/https://a.example/rss","title":"Alpha","categories":[{"id":"user/1/label/Tech","label":"Tech"}],"url":"https://a.example/rss","htmlUrl":"https://a.example","feedType":"rss"}]}`))
		case ep == "tag/list":
			_, _ = w.Write([]byte(`{"tags":[{"id":"user/1/label/Tech","sortid":"X","type":"folder","unread_count":3},{"id":"user/1/state/com.google/starred","sortid":"Y"}]}`))
		case ep == "unread-count":
			_, _ = w.Write([]byte(`{"max":1000,"unreadcounts":[{"id":"user/1/state/com.google/reading-list","count":9,"newestItemTimestampUsec":"1700000000000000"},{"id":"feed/https://a.example/rss","count":3,"newestItemTimestampUsec":"1700000000000000"},{"id":"feed/https://b.example/rss","count":0,"newestItemTimestampUsec":"0"},{"id":"user/1/label/Tech","count":3,"newestItemTimestampUsec":"1700000000000000"}]}`))
		case strings.HasPrefix(ep, "stream/contents/"):
			if r.URL.Query().Get("c") == "" {
				_, _ = w.Write([]byte(`{"id":"user/1/state/com.google/reading-list","title":"Reading list","items":[
				  {"id":"tag:google.com,2005:reader/item/00000000148b9369","title":" One ","author":"A","published":1700003600,"categories":["user/1/state/com.google/read","user/1/label/Tech"],"canonical":[{"href":"https://a.example/1"}],"summary":{"content":"<p>Hello <b>there</b> this is a fairly long article body that goes on and on and on for a while.</p>"},"origin":{"streamId":"feed/https://a.example/rss","title":"Alpha","htmlUrl":"https://a.example"},"annotations":[{"text":"quote","note":"n"}]},
				  {"id":"tag:google.com,2005:reader/item/00000000148b9360","title":"Two","published":1700000000,"categories":["user/1/state/com.google/starred"],"alternate":[{"href":"https://b.example/2"}],"summary":{"content":"short"},"origin":{"streamId":"feed/https://b.example/rss","title":"Bravo"}}],
				  "continuation":"next"}`))
			} else {
				_, _ = w.Write([]byte(`{"id":"x","items":[{"id":"tag:google.com,2005:reader/item/00000000148b9361","title":"Three","published":1700002000,"categories":[],"summary":{"content":""},"origin":{"streamId":"feed/https://b.example/rss","title":"Bravo"}}]}`))
			}
		case ep == "stream/items/ids":
			_, _ = w.Write([]byte(`{"itemRefs":[{"id":"344691561","directStreamIds":[],"timestampUsec":"1"}],"continuation":"c9"}`))
		case ep == "stream/items/contents":
			_, _ = w.Write([]byte(`{"items":[{"id":"tag:google.com,2005:reader/item/00000000148b9369","title":"One","published":1700003600,"summary":{"content":"<p>Full</p>"},"origin":{"title":"Alpha"}}]}`))
		case ep == "subscription/quickadd":
			_, _ = w.Write([]byte(`{"query":"feed/https://c.example","numResults":1,"streamId":"feed/https://c.example/feed","streamName":"Charlie"}`))
		case ep == "preference/stream/list":
			_, _ = w.Write([]byte(`{"streamprefs":{"user/1/state/com.google/root":[{"id":"subscription-ordering","value":"AB"}]}}`))
		case ep == "edit-tag" && strings.Contains(r.PostForm.Get("a"), "starred") && r.PostForm.Get("i") == "403":
			w.WriteHeader(403)
			_, _ = w.Write([]byte("Error=write scope required"))
		default:
			_, _ = w.Write([]byte("OK"))
		}
	})
	f.api = httptest.NewServer(mux)
	t.Cleanup(f.api.Close)

	tok := &inoreader.Token{AccessToken: "at", RefreshToken: "rt", Scope: "read write", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	ts, err := inoreader.NewTokenSource(&inoreader.OAuthConfig{ClientID: "x", ClientSecret: "y"}, &inoreader.MemoryTokenStore{}, tok)
	if err != nil {
		t.Fatal(err)
	}
	client := inoreader.NewClient(ts, inoreader.WithBaseURL(f.api.URL), inoreader.WithHTTPClient(f.api.Client()))
	srv := New(client, Options{ReadOnly: readOnly, Version: "test"})

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	f.sess = sess
	return f
}

func (f *fixture) call(name string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
	f.t.Helper()
	res, err := f.sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		f.t.Fatalf("%s: protocol error: %v", name, err)
	}
	var out map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out)
	}
	return res, out
}

func (f *fixture) mustOK(name string, args map[string]any) map[string]any {
	f.t.Helper()
	res, out := f.call(name, args)
	if res.IsError {
		f.t.Fatalf("%s returned error: %v", name, res.Content)
	}
	return out
}

func (f *fixture) mustErr(name string, args map[string]any, contains string) {
	f.t.Helper()
	res, _ := f.call(name, args)
	if !res.IsError {
		f.t.Fatalf("%s: expected error result", name)
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, contains) {
		f.t.Fatalf("%s: error %q does not contain %q", name, text, contains)
	}
}

func toolNames(t *testing.T, sess *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		m[tl.Name] = tl
	}
	return m
}

func TestToolRegistrationAndReadOnly(t *testing.T) {
	full := toolNames(t, newFixture(t, false).sess)
	ro := toolNames(t, newFixture(t, true).sess)
	if len(full) != 22 {
		t.Errorf("expected 22 tools, got %d", len(full))
	}
	if len(ro) != 10 {
		t.Errorf("expected 10 read-only tools, got %d: %v", len(ro), ro)
	}
	for name, tl := range full {
		if tl.Annotations == nil {
			t.Errorf("%s has no annotations", name)
			continue
		}
		_, inRO := ro[name]
		if tl.Annotations.ReadOnlyHint != inRO {
			t.Errorf("%s: readOnlyHint=%v but present in read-only server=%v", name, tl.Annotations.ReadOnlyHint, inRO)
		}
		if tl.Description == "" {
			t.Errorf("%s has no description", name)
		}
	}
	for _, destructive := range []string{"mark_all_as_read", "unsubscribe_feed", "delete_folder_or_tag"} {
		if d := full[destructive].Annotations.DestructiveHint; d == nil || !*d {
			t.Errorf("%s should be destructive", destructive)
		}
	}
}

func TestReadTools(t *testing.T) {
	f := newFixture(t, false)

	st := f.mustOK("get_server_status", nil)
	if st["authenticated"] != true || st["scope"] != "read write" || st["rate_limit"] != nil {
		t.Errorf("status: %v", st)
	}
	if len(f.calls) != 0 {
		t.Errorf("status should not call the API")
	}

	ui := f.mustOK("get_user_info", nil)
	if ui["user_name"] != "john" || ui["rate_limit"].(map[string]any)["zone1_reads"] != "3/100" {
		t.Errorf("user info: %v", ui)
	}

	subs := f.mustOK("list_subscriptions", nil)
	list := subs["subscriptions"].([]any)
	if subs["count"].(float64) != 2 || list[0].(map[string]any)["title"] != "Alpha" {
		t.Errorf("subscriptions not sorted: %v", subs)
	}
	subs = f.mustOK("list_subscriptions", map[string]any{"folder": "tech"})
	if subs["count"].(float64) != 1 {
		t.Errorf("folder filter: %v", subs)
	}
	subs = f.mustOK("list_subscriptions", map[string]any{"query": "b.example"})
	if subs["count"].(float64) != 1 || subs["subscriptions"].([]any)[0].(map[string]any)["title"] != "Bravo" {
		t.Errorf("query filter: %v", subs)
	}

	tags := f.mustOK("list_folders_and_tags", nil)
	tl := tags["tags"].([]any)[0].(map[string]any)
	if tl["name"] != "Tech" || tl["unread_count"].(float64) != 3 {
		t.Errorf("tags: %v", tags)
	}
	if st := tags["tags"].([]any)[1].(map[string]any); st["name"] != "starred" || st["type"] != "state" {
		t.Errorf("state tag: %v", st)
	}

	uc := f.mustOK("get_unread_counts", nil)
	if uc["total_unread"].(float64) != 9 || len(uc["feeds"].([]any)) != 1 || len(uc["folders"].([]any)) != 1 {
		t.Errorf("unread: %v", uc)
	}
	uc = f.mustOK("get_unread_counts", map[string]any{"include_zero": true})
	if len(uc["feeds"].([]any)) != 2 {
		t.Errorf("unread include_zero: %v", uc)
	}

	sc := f.mustOK("get_stream_contents", map[string]any{"limit": 50, "unread_only": true, "hours": 2, "summary_chars": 20})
	items := sc["items"].([]any)
	first := items[0].(map[string]any)
	if sc["continuation"] != "next" || len(items) != 2 || first["title"] != "One" || first["short_id"] != "344691561" {
		t.Errorf("stream: %v", sc)
	}
	if sum := first["summary"].(string); !strings.HasSuffix(sum, "…") || len([]rune(sum)) > 21 || strings.Contains(sum, "<") {
		t.Errorf("summary not truncated plain text: %q", sum)
	}
	if first["read"] != true || first["folders"].([]any)[0] != "Tech" || first["annotations"].([]any)[0] != "quote — note: n" {
		t.Errorf("article fields: %v", first)
	}
	if last := f.calls[len(f.calls)-1]; !strings.Contains(last, "n=50") || !strings.Contains(last, "xt=user%2F-%2Fstate%2Fcom.google%2Fread") || !strings.Contains(last, "ot=") {
		t.Errorf("stream query: %s", last)
	}
	f.mustErr("get_stream_contents", map[string]any{"since": "yesterday"}, "RFC 3339")

	sc = f.mustOK("get_stream_contents", map[string]any{"summary_chars": -1, "include_html": true, "starred_only": true})
	first = sc["items"].([]any)[0].(map[string]any)
	if _, has := first["summary"]; has {
		t.Errorf("summary should be omitted: %v", first)
	}
	if !strings.Contains(first["html"].(string), "<b>") {
		t.Errorf("html missing: %v", first)
	}
	if last := f.calls[len(f.calls)-1]; !strings.Contains(last, "it=user%2F-%2Fstate%2Fcom.google%2Fstarred") {
		t.Errorf("starred query: %s", last)
	}

	ids := f.mustOK("get_item_ids", map[string]any{"stream_id": "user/-/label/Tech", "limit": 500})
	if ids["count"].(float64) != 1 || ids["continuation"] != "c9" {
		t.Errorf("ids: %v", ids)
	}

	arts := f.mustOK("get_articles", map[string]any{"ids": []string{"344691561"}, "summary_chars": 1000})
	if arts["count"].(float64) != 1 || arts["items"].([]any)[0].(map[string]any)["summary"] != "Full" {
		t.Errorf("articles: %v", arts)
	}
	f.mustErr("get_articles", map[string]any{"ids": []string{}}, "ids is required")

	prefs := f.mustOK("get_stream_preferences", nil)
	if prefs["preferences"] == nil {
		t.Errorf("prefs: %v", prefs)
	}
}

func TestScanRecent(t *testing.T) {
	f := newFixture(t, false)
	out := f.mustOK("scan_recent_articles", map[string]any{"since": "2023-11-14T22:00:00Z"})
	if out["fetched"].(float64) != 3 || out["count"].(float64) != 3 || out["truncated"] != false {
		t.Errorf("scan: %v", out)
	}
	fc := out["feed_counts"].([]any)
	if fc[0].(map[string]any)["feed"] != "Bravo" || fc[0].(map[string]any)["count"].(float64) != 2 {
		t.Errorf("feed counts: %v", fc)
	}
	// Two pages were requested with the continuation.
	if len(f.calls) != 2 || !strings.Contains(f.calls[1], "c=next") {
		t.Errorf("calls: %v", f.calls)
	}

	out = f.mustOK("scan_recent_articles", map[string]any{"since": "2023-11-14T22:00:00Z", "folders": []string{"tech"}})
	if out["count"].(float64) != 1 || out["fetched"].(float64) != 3 {
		t.Errorf("folder filter: %v", out)
	}
	out = f.mustOK("scan_recent_articles", map[string]any{"since": "2023-11-14T22:00:00Z", "exclude_feeds": []string{"bravo"}})
	if out["count"].(float64) != 1 {
		t.Errorf("exclude filter: %v", out)
	}
	out = f.mustOK("scan_recent_articles", map[string]any{"since": "2023-11-14T22:00:00Z", "max_items": 1})
	if out["count"].(float64) != 1 || out["truncated"] != true {
		t.Errorf("max_items: %v", out)
	}
	// Default window is 24h: items from 2023 are dropped client-side.
	out = f.mustOK("scan_recent_articles", nil)
	if out["count"].(float64) != 0 {
		t.Errorf("default window should exclude old items: %v", out)
	}
}

func TestWriteTools(t *testing.T) {
	f := newFixture(t, false)
	lastForm := func() string { return f.forms[len(f.forms)-1] }

	out := f.mustOK("mark_items_read", map[string]any{"ids": []string{"tag:google.com,2005:reader/item/00000000148b9369"}})
	if out["ok"] != true || out["affected"].(float64) != 1 || lastForm() != "a=user%2F-%2Fstate%2Fcom.google%2Fread&i=344691561" {
		t.Errorf("mark read: %v %s", out, lastForm())
	}
	f.mustOK("mark_items_unread", map[string]any{"ids": []string{"1"}})
	f.mustOK("star_items", map[string]any{"ids": []string{"1"}})
	f.mustOK("unstar_items", map[string]any{"ids": []string{"1"}})
	f.mustErr("star_items", map[string]any{"ids": []string{}}, "ids is required")
	f.mustErr("star_items", map[string]any{"ids": []string{"403"}}, "write scope required")

	f.mustOK("edit_item_tags", map[string]any{"ids": []string{"1"}, "add_tags": []string{"digest"}, "remove_tags": []string{"user/-/label/old"}})
	if lastForm() != "a=user%2F-%2Flabel%2Fdigest&i=1&r=user%2F-%2Flabel%2Fold" {
		t.Errorf("edit tags form: %s", lastForm())
	}
	f.mustErr("edit_item_tags", map[string]any{"ids": []string{"1"}}, "add_tags or remove_tags")

	f.mustErr("mark_all_as_read", map[string]any{"stream_id": "user/-/label/Tech"}, "confirm=true")
	f.mustOK("mark_all_as_read", map[string]any{"stream_id": "user/-/label/Tech", "confirm": true, "older_than": "2023-11-14T22:00:00Z"})
	if lastForm() != "s=user%2F-%2Flabel%2FTech&ts=1699999200" {
		t.Errorf("mark all form: %s", lastForm())
	}

	sub := f.mustOK("subscribe_feed", map[string]any{"url": "https://c.example", "folder": "News", "title": "C"})
	if sub["stream_id"] != "feed/https://c.example/feed" || sub["title"] != "C" {
		t.Errorf("subscribe: %v", sub)
	}
	if lastForm() != "a=user%2F-%2Flabel%2FNews&ac=edit&s=feed%2Fhttps%3A%2F%2Fc.example%2Ffeed&t=C" {
		t.Errorf("subscribe edit form: %s", lastForm())
	}
	f.mustErr("unsubscribe_feed", map[string]any{"stream_id": "feed/https://c.example/feed"}, "confirm=true")
	f.mustOK("unsubscribe_feed", map[string]any{"stream_id": "feed/https://c.example/feed", "confirm": true})
	f.mustErr("edit_subscription", map[string]any{"stream_id": "feed/x"}, "nothing to change")
	f.mustOK("edit_subscription", map[string]any{"stream_id": "feed/https://a.example/rss", "remove_folder": "Tech"})
	f.mustOK("rename_folder_or_tag", map[string]any{"name": "Tech", "new_name": "Technology"})
	f.mustErr("rename_folder_or_tag", map[string]any{"name": "Tech", "new_name": "a/b"}, "must not contain")
	f.mustErr("delete_folder_or_tag", map[string]any{"name": "Tech"}, "confirm=true")
	f.mustOK("delete_folder_or_tag", map[string]any{"name": "Tech", "confirm": true})
	f.mustOK("set_subscription_ordering", map[string]any{"sort_ids": "ABCDEFGH"})
	if lastForm() != "k=subscription-ordering&s=user%2F-%2Fstate%2Fcom.google%2Froot&v=ABCDEFGH" {
		t.Errorf("ordering form: %s", lastForm())
	}
}

func TestPromptsAndResources(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	ps, err := f.sess.ListPrompts(ctx, nil)
	if err != nil || len(ps.Prompts) != 2 {
		t.Fatalf("prompts: %v %v", err, ps)
	}
	p, err := f.sess.GetPrompt(ctx, &mcp.GetPromptParams{Name: "daily_briefing", Arguments: map[string]string{"hours": "48", "interests": "Go, eBPF", "stream_id": "user/-/label/Tech", "unread_only": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	text := p.Messages[0].Content.(*mcp.TextContent).Text
	for _, want := range []string{"hours=48", `stream_id="user/-/label/Tech"`, "unread_only=true", "Go, eBPF", "scan_recent_articles"} {
		if !strings.Contains(text, want) {
			t.Errorf("briefing prompt missing %q", want)
		}
	}
	p, err = f.sess.GetPrompt(ctx, &mcp.GetPromptParams{Name: "feed_alert", Arguments: map[string]string{"topics": "eBPF"}})
	if err != nil || !strings.Contains(p.Messages[0].Content.(*mcp.TextContent).Text, "hours=6") {
		t.Errorf("feed_alert: %v", err)
	}
	if _, err := f.sess.GetPrompt(ctx, &mcp.GetPromptParams{Name: "feed_alert"}); err == nil {
		t.Error("feed_alert without topics should fail")
	}
	rs, err := f.sess.ListResources(ctx, nil)
	if err != nil || len(rs.Resources) != 1 {
		t.Fatalf("resources: %v", err)
	}
	rr, err := f.sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: "inoreader://docs/stream-ids"})
	if err != nil || !strings.Contains(rr.Contents[0].Text, "reading-list") {
		t.Errorf("read resource: %v", err)
	}
}
