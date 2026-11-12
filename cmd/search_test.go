package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/color"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/interactions"
)

// TestSearchStatusOrder: the real API reports the search steps after the
// image (captured with --debug-events), although the search ran while the
// model was thinking. Showing "searching" after "rendering" made it look as
// if the results were ignored, so late search steps must not change the status.
func TestSearchStatusOrder(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(testPNG(t, color.Black))
	chips := `<a class=\"chip\" href=\"https://www.google.com/search?q=sf+weather\">sf weather</a>`
	thought := []string{
		`{"index":0,"step":{"type":"thought"},"event_type":"step.start"}`,
		`{"index":0,"event_type":"step.stop"}`,
	}
	img := []string{
		`{"index":1,"step":{"type":"model_output"},"event_type":"step.start"}`,
		`{"index":1,"delta":{"type":"image","mime_type":"image/png","data":"` + b64 + `"},"event_type":"step.delta"}`,
		`{"index":1,"event_type":"step.stop"}`,
	}
	search := []string{
		`{"index":2,"step":{"id":"s1","type":"google_search_call","search_type":"web_search"},"event_type":"step.start"}`,
		`{"index":2,"delta":{"type":"google_search_call","arguments":{"queries":["sf weather"]}},"event_type":"step.delta"}`,
		`{"index":2,"event_type":"step.stop"}`,
		`{"index":3,"step":{"call_id":"s1","type":"google_search_result"},"event_type":"step.start"}`,
		`{"index":3,"delta":{"type":"google_search_result","result":[{"search_suggestions":"` + chips + `"}]},"event_type":"step.delta"}`,
		`{"index":3,"event_type":"step.stop"}`,
	}
	for name, c := range map[string]struct {
		events        []string
		wantSearching bool
	}{
		"search reported after image (real API order)": {slices.Concat(thought, img, search), false},
		"search reported before image":                 {slices.Concat(thought, search, img), true},
	} {
		client, _ := fakeClient(t, sse(c.events...))
		var status, errOut bytes.Buffer
		p := &progress{errOut: &errOut, status: &statusLine{w: &status, cols: func() int { return 200 }, start: time.Now()}}
		req := interactions.CreateModelInteraction{Model: "m", Input: genai.Ptr(interactions.NewInteractionsInput("p"))}
		if _, _, err := streamImage(context.Background(), client, req, p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		draws := status.String()
		if got := strings.Contains(draws, "searching: sf weather"); got != c.wantSearching {
			t.Errorf("%s: status showed searching = %v, want %v", name, got, c.wantSearching)
		}
		if last := draws[strings.LastIndex(draws, "\r"):]; !strings.Contains(last, "finishing") {
			t.Errorf("%s: final status = %q, want finishing", name, last)
		}
		if !strings.Contains(errOut.String(), "Google Search suggestions:") {
			t.Errorf("%s: suggestions not printed", name)
		}
	}
}

func TestSearchTools(t *testing.T) {
	if tools, err := searchTools(""); tools != nil || err != nil {
		t.Errorf("empty: got %v, %v", tools, err)
	}
	tools, err := searchTools("Web, image,web")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(tools)
	if want := `[{"search_types":["web_search","image_search"],"type":"google_search"}]`; string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
	if _, err := searchTools("web,maps"); err == nil {
		t.Error("expected error for unknown search type")
	}
}

func TestParseSuggestions(t *testing.T) {
	// Shape of the snippet Google returns: styles plus a row of <a class="chip">.
	snippet := `<style>.chip{color:red}</style><div class="container"><div class="carousel">` +
		`<a class="chip" href="https://www.google.com/search?q=weather+sf&amp;client=app">weather   sf</a>` +
		`<a class="chip" href="https://www.google.com/search?q=sf+forecast"><span>sf</span> forecast</a>` +
		`<a class="logo">no href</a></div></div>`
	got := parseSuggestions(snippet)
	want := []link{
		{text: "weather sf", url: "https://www.google.com/search?q=weather+sf&client=app"},
		{text: "sf forecast", url: "https://www.google.com/search?q=sf+forecast"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestSanitize(t *testing.T) {
	if got := sanitize("a\x1b]8;;evil\x07b\n\tc\u009bd"); got != "a]8;;evilb\n\tcd" {
		t.Errorf("got %q", got)
	}
}

func TestStreamImageWithSearch(t *testing.T) {
	final := testPNG(t, color.Black)
	b64 := base64.StdEncoding.EncodeToString(final)
	chips := `<a class=\"chip\" href=\"https://www.google.com/search?q=sf+weather\">sf weather</a>`
	data, _, out, err := run(t, sse(
		`{"index":0,"step":{"type":"google_search_call","id":"s1","arguments":{"queries":["sf weather"]}},"event_type":"step.start"}`,
		`{"index":0,"event_type":"step.stop"}`,
		`{"index":1,"step":{"type":"google_search_result","call_id":"s1","result":[{"search_suggestions":"`+chips+`"}]},"event_type":"step.start"}`,
		`{"index":1,"event_type":"step.stop"}`,
		`{"index":2,"step":{"type":"model_output"},"event_type":"step.start"}`,
		`{"index":2,"delta":{"type":"text_annotation_delta","annotations":[{"type":"url_citation","url":"https://weather.example/sf","title":"weather.example"}]},"event_type":"step.delta"}`,
		`{"index":2,"delta":{"type":"image","mime_type":"image/png","data":"`+b64+`"},"event_type":"step.delta"}`,
		`{"index":2,"event_type":"step.stop"}`,
		// The completed event repeats the result; it must not be printed twice.
		`{"interaction":{"id":"v1_1","status":"completed","steps":[{"type":"google_search_result","call_id":"s1","result":[{"search_suggestions":"`+chips+`"}]}]},"event_type":"interaction.completed"}`,
	))
	if err != nil || !bytes.Equal(data, final) {
		t.Fatalf("got %d bytes, err %v", len(data), err)
	}
	want := "Google Search suggestions:\n  sf weather <https://www.google.com/search?q=sf+weather>\n" +
		"Source: weather.example <https://weather.example/sf>\n"
	if out != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
	if strings.Count(out, "suggestions") != 1 {
		t.Error("suggestions printed more than once")
	}
}
