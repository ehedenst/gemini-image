package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
// every request with body as an event stream, and records the request body.
func fakeClient(t *testing.T, body string) (*genai.Client, *string) {
	t.Helper()
	var reqBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:      "test",
		Backend:     genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, &reqBody
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
		), "generation failed: quota exceeded"},
	} {
		_, _, _, err := run(t, c.body)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}
