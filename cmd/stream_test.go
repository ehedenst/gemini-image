package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/interactions"
)

func testPNG(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range 16 {
		img.Set(i%4, i/4, c)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// resetCLI puts the command tree back in its fresh-process state, now and
// after the test: flag values, Changed and contexts outlive rootCmd.Execute.
func resetCLI(t *testing.T) {
	resetFlag := func(f *pflag.Flag) {
		if s, ok := f.Value.(pflag.SliceValue); ok {
			_ = s.Replace(nil)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	}
	reset := func() {
		rootCmd.PersistentFlags().VisitAll(resetFlag)
		for _, c := range append(rootCmd.Commands(), rootCmd) {
			c.Flags().VisitAll(resetFlag)
			// Cobra only gives a subcommand Execute's context if it has none.
			c.SetContext(nil) //nolint:staticcheck // nil, as in a fresh process
		}
	}
	reset()
	t.Cleanup(reset)
}

// imageEvents streams one model_output step with a PNG image.
func imageEvents(t *testing.T) []string {
	b64 := base64.StdEncoding.EncodeToString(testPNG(t, color.Black))
	return []string{
		`{"index":0,"step":{"type":"model_output"},"event_type":"step.start"}`,
		`{"index":0,"delta":{"type":"image","mime_type":"image/png","data":"` + b64 + `"},"event_type":"step.delta"}`,
		`{"index":0,"event_type":"step.stop"}`,
	}
}

// generateCLI runs "gemini-image generate args…" against a fake API that
// replies with body (see sse), calling onRequest (if not nil) when a request
// arrives. It returns stderr, the number of requests and the error from
// Execute.
func generateCLI(t *testing.T, body string, onRequest func(), args ...string) (string, int, error) {
	t.Helper()
	resetCLI(t)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if onRequest != nil {
			onRequest()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
	}))
	t.Setenv("GOOGLE_GEMINI_BASE_URL", srv.URL)

	var stderr bytes.Buffer
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(&stderr)
	defer rootCmd.SetErr(io.Discard)
	rootCmd.SetArgs(append([]string{"--env-file", "/dev/null", "--api-key", "k", "--model", "m", "generate",
		"--preview=false", "--size=", "--aspect-ratio=", "--search=", "--thoughts=false"}, args...))
	err := rootCmd.Execute()
	srv.Close() // waits for the handler, so requests can be read
	return stderr.String(), requests, err
}

// cutOff turns an sse body into one whose connection was lost after the
// events: no closing [DONE].
func cutOff(body string) string { return strings.TrimSuffix(body, "data: [DONE]\n\n") }

// sse renders events in the wire format from the streaming docs.
func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "data: %s\n\n", e)
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

// fakeClient returns a real SDK client talking to a server that replies to
// every request with body as an event stream, and records the body of the
// create request.
func fakeClient(t *testing.T, body string) (*genai.Client, *string) {
	t.Helper()
	var reqBody string
	client := handlerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost { // not lookups of the interaction
			b, _ := io.ReadAll(r.Body)
			reqBody = string(b)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
	})
	return client, &reqBody
}

// handlerClient returns a real SDK client talking to a server run by h.
func handlerClient(t *testing.T, h http.HandlerFunc) *genai.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:      "test",
		Backend:     genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func run(t *testing.T, body string) ([]byte, string, string, error) {
	t.Helper()
	client, reqBody := fakeClient(t, body)
	var errOut bytes.Buffer
	p := &progress{errOut: &errOut, thoughts: true}
	req := interactions.CreateModelInteraction{
		Model: "test-model",
		Input: genai.Ptr(interactions.NewInteractionsInput("a cat")),
	}
	data, mime, err := streamImage(context.Background(), client, req, p)
	p.printGrounding() // as runGenerate does after the preview
	if !strings.Contains(*reqBody, `"stream":true`) {
		t.Errorf("request did not ask for a stream: %s", *reqBody)
	}
	return data, mime, errOut.String(), err
}

func TestStreamImage(t *testing.T) {
	interim := base64.StdEncoding.EncodeToString(testPNG(t, color.White))
	final := testPNG(t, color.Black)
	finalB64 := base64.StdEncoding.EncodeToString(final)

	data, mime, thoughts, err := run(t, sse(
		`{"interaction":{"id":"v1_1","status":"in_progress"},"event_type":"interaction.created"}`,
		`{"interaction_id":"v1_1","status":"in_progress","event_type":"interaction.status_update"}`,
		`{"index":0,"step":{"type":"thought"},"event_type":"step.start"}`,
		`{"index":0,"delta":{"type":"thought_summary","content":{"type":"text","text":"**Planning the scene**\n"}},"event_type":"step.delta"}`,
		`{"index":0,"delta":{"type":"thought_summary","content":{"type":"text","text":"A cat on a sofa."}},"event_type":"step.delta"}`,
		`{"index":0,"delta":{"type":"thought_summary","content":{"type":"image","mime_type":"image/png","data":"`+interim+`"}},"event_type":"step.delta"}`,
		`{"index":0,"event_type":"step.stop"}`,
		`{"index":1,"step":{"type":"model_output"},"event_type":"step.start"}`,
		`{"index":1,"delta":{"type":"image","mime_type":"image/png","data":"`+finalB64+`"},"event_type":"step.delta"}`,
		`{"index":1,"event_type":"step.stop"}`,
		`{"interaction":{"id":"v1_1","status":"completed"},"event_type":"interaction.completed"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, final) || mime != "image/png" {
		t.Errorf("got %d bytes (%s), want the final image", len(data), mime)
	}
	want := "**Planning the scene**\nA cat on a sofa.\n\n[interim image]\n"
	if thoughts != want {
		t.Errorf("thoughts output = %q, want %q", thoughts, want)
	}
}

func TestStreamImageChunked(t *testing.T) {
	final := testPNG(t, color.Black)
	b64 := base64.StdEncoding.EncodeToString(final)
	cut := len(b64)/2 + 1 // not on a 4-byte boundary, so neither half decodes alone
	data, _, _, err := run(t, sse(
		`{"index":0,"step":{"type":"model_output"},"event_type":"step.start"}`,
		`{"index":0,"delta":{"type":"image","mime_type":"image/png","data":"`+b64[:cut]+`"},"event_type":"step.delta"}`,
		`{"index":0,"delta":{"type":"image","mime_type":"image/png","data":"`+b64[cut:]+`"},"event_type":"step.delta"}`,
		`{"index":0,"event_type":"step.stop"}`,
	))
	if err != nil || !bytes.Equal(data, final) {
		t.Errorf("chunked image: got %d bytes, err %v", len(data), err)
	}
}

func TestStreamImageFromCompletedSteps(t *testing.T) {
	final := testPNG(t, color.Black)
	b64 := base64.StdEncoding.EncodeToString(final)
	data, mime, _, err := run(t, sse(
		`{"interaction":{"id":"v1_1","status":"completed","steps":[{"type":"model_output","content":[{"type":"image","mime_type":"image/png","data":"`+b64+`"}]}]},"event_type":"interaction.completed"}`,
	))
	if err != nil || !bytes.Equal(data, final) || mime != "image/png" {
		t.Errorf("fallback: got %d bytes (%s), err %v", len(data), mime, err)
	}
}

func TestStreamImageErrors(t *testing.T) {
	for name, c := range map[string]struct {
		body, want string
	}{
		"refusal": {sse(
			`{"index":0,"step":{"type":"model_output"},"event_type":"step.start"}`,
			`{"index":0,"delta":{"type":"text","text":"I can't draw that."},"event_type":"step.delta"}`,
			`{"interaction":{"id":"v1_1","status":"completed"},"event_type":"interaction.completed"}`,
		), "no image in response (status completed): I can't draw that."},
		"error event": {sse(
			`{"error":{"code":"quota","message":"quota exceeded"},"event_type":"error"}`,
		), "generation failed: quota exceeded (quota)"},
	} {
		_, _, _, err := run(t, c.body)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

// TestStreamCutOff: the API cancels an interaction whose connection drops, so
// whatever arrived is all there is. A complete image is kept; without one the
// error says what happened.
func TestStreamCutOff(t *testing.T) {
	img := imageEvents(t) // step.start, image delta, step.stop
	completed := `{"interaction":{"id":"v1_1","status":"completed"},"event_type":"interaction.completed"}`
	for name, c := range map[string]struct {
		events    []string
		wantImage bool
	}{
		"after interaction.completed": {append(slices.Clone(img), completed), true},
		"after the image step":        {img, true},
		"during the image step":       {img[:2], false},
		"before the image":            {img[:1], false},
	} {
		data, _, _, err := run(t, cutOff(sse(c.events...)))
		if c.wantImage {
			if err != nil || len(data) == 0 {
				t.Errorf("%s: got %d bytes, err %v; want the image", name, len(data), err)
			}
			continue
		}
		if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) || !strings.Contains(err.Error(), "before the image arrived") {
			t.Errorf("%s: err = %v, want a lost-connection error", name, err)
		}
	}
}

// TestAPIErrorReadable: API error responses read as "message (code)", not as
// the raw JSON the SDK prints.
func TestAPIErrorReadable(t *testing.T) {
	client := handlerClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"code":"image_recitation","message":"Image generation blocked due to copyright/recitation."}}`)
	})
	req := interactions.CreateModelInteraction{Model: "m", Input: genai.Ptr(interactions.NewInteractionsInput("p"))}
	_, _, err := streamImage(context.Background(), client, req, &progress{errOut: io.Discard})
	if want := "Image generation blocked due to copyright/recitation. (image_recitation)"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// TestFailedWithoutErrorEvent: the real API sometimes fails an interaction by
// closing the stream mid-thought (seen with prompts about real people); the
// error is then only on the interaction, which the CLI looks up, asking again
// while the interaction still looks like it's running.
func TestFailedWithoutErrorEvent(t *testing.T) {
	defer func(d time.Duration) { failureRetryDelay = d }(failureRetryDelay)
	failureRetryDelay = time.Millisecond
	const failed = "generation failed: There was a problem processing your request. (INVALID_ARGUMENT)"
	const lost = "connection to the API lost before the image arrived"
	for name, c := range map[string]struct {
		statuses []string // what each lookup finds; "" is "not found"
		want     string
		lookups  int
	}{
		"failed":        {[]string{"failed"}, failed, 1},
		"recorded late": {[]string{"in_progress", "failed"}, failed, 2},
		"still running": {[]string{"in_progress"}, lost, 3},
		"cancelled":     {[]string{"cancelled"}, lost, 1},
		"lookup fails":  {[]string{""}, lost, 1},
	} {
		lookups := 0
		client := handlerClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				status := c.statuses[min(lookups, len(c.statuses)-1)]
				lookups++
				w.Header().Set("Content-Type", "application/json")
				if status == "" {
					w.WriteHeader(http.StatusNotFound)
					io.WriteString(w, `{"error":{"code":"not_found","message":"Interaction not found."}}`)
					return
				}
				io.WriteString(w, `{"id":"v1_x","status":"`+status+`","errors":[{"code":"INVALID_ARGUMENT","message":"There was a problem processing your request."}]}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, cutOff(sse(
				`{"interaction":{"id":"v1_x"},"event_type":"interaction.created"}`,
				`{"index":0,"step":{"type":"thought"},"event_type":"step.start"}`,
				`{"index":0,"delta":{"type":"thought_summary","content":{"type":"text","text":"**Planning**\nA caricature."}},"event_type":"step.delta"}`,
			)))
		})
		var errOut bytes.Buffer
		req := interactions.CreateModelInteraction{Model: "m", Input: genai.Ptr(interactions.NewInteractionsInput("p"))}
		_, _, err := streamImage(context.Background(), client, req, &progress{errOut: &errOut, thoughts: true})
		if err == nil || !strings.HasPrefix(err.Error(), c.want) || lookups != c.lookups {
			t.Errorf("%s: err = %v after %d lookups, want %q after %d", name, err, lookups, c.want, c.lookups)
		}
		// The thought the stream broke off in is still shown.
		if !strings.Contains(errOut.String(), "A caricature.") {
			t.Errorf("%s: thoughts lost: %q", name, errOut.String())
		}
	}
}

// TestContinuationParts: with automatic continuation, the SDK passes on each
// part's events, ending a part with a completed event whose status is
// requires_continuation. That isn't the end: a drop after it is an error, and
// the search count adds up over the parts.
func TestContinuationParts(t *testing.T) {
	part1 := []string{
		`{"interaction":{"id":"v1_a"},"event_type":"interaction.created"}`,
		`{"interaction":{"id":"v1_a","status":"requires_continuation","usage":{"grounding_tool_count":[{"type":"google_search","count":1}]}},"event_type":"interaction.completed"}`,
	}
	if _, _, _, err := run(t, cutOff(sse(part1...))); err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("drop after part 1: err = %v, want a lost-connection error", err)
	}

	chips := `<a class=\"chip\" href=\"https://www.google.com/search?q=x\">x</a>`
	part2 := append([]string{
		`{"interaction":{"id":"v1_b"},"event_type":"interaction.created"}`,
		`{"index":1,"step":{"type":"google_search_result","call_id":"s1","result":[{"search_suggestions":"` + chips + `"}]},"event_type":"step.start"}`,
		`{"index":1,"event_type":"step.stop"}`,
	}, imageEvents(t)...)
	part2 = append(part2, `{"interaction":{"id":"v1_b","status":"completed","usage":{"grounding_tool_count":[{"type":"google_search","count":1}]}},"event_type":"interaction.completed"}`)
	_, _, out, err := run(t, sse(slices.Concat(part1, part2)...))
	if err != nil || strings.Contains(out, "note:") || !strings.Contains(out, "Google Search suggestions:") {
		t.Errorf("two parts: err = %v, output %q; want the suggestions and no note", err, out)
	}
}

// TestDebugEventsEnd: --debug-events ends with a line saying how long the
// stream took, when its last event came and how it ended.
func TestDebugEventsEnd(t *testing.T) {
	client, _ := fakeClient(t, cutOff(sse(imageEvents(t)[:1]...)))
	var events bytes.Buffer
	req := interactions.CreateModelInteraction{Model: "m", Input: genai.Ptr(interactions.NewInteractionsInput("p"))}
	_, _, err := streamImage(context.Background(), client, req, &progress{errOut: io.Discard, events: &events})
	lines := strings.Split(strings.TrimSpace(events.String()), "\n")
	var last struct {
		End struct {
			Elapsed   *float64 `json:"elapsed_s"`
			LastEvent *float64 `json:"last_event_s"`
			Events    int      `json:"events"`
			Error     string   `json:"error"`
		} `json:"end"`
	}
	if jerr := json.Unmarshal([]byte(lines[len(lines)-1]), &last); jerr != nil || len(lines) != 3 ||
		last.End.Elapsed == nil || last.End.LastEvent == nil || last.End.Events != 1 || err == nil || last.End.Error != err.Error() {
		t.Errorf("debug events:\n%s\n(err %v)", events.String(), err)
	}
}
