package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/ehedenst/gemini-image/internal/kitty"
	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/apierrors"
	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
)

// progress reports what the model is doing while an image is generated.
type progress struct {
	status     *statusLine // nil when stderr isn't a terminal
	errOut     io.Writer   // thoughts, search suggestions and sources go here
	thoughts   bool        // print thought summaries and interim images
	preview    *os.File    // where interim images are drawn; nil disables
	hyperlinks bool        // errOut is a terminal: print links as OSC 8 hyperlinks
	color      bool        // colorize errOut

	thought        strings.Builder // thought text not yet printed
	shownPreview   []byte          // last interim image drawn, to avoid repeats
	printed        map[string]bool // suggestions and citations already collected
	grounding      []string        // suggestion and source lines, printed after the image
	gotSuggestions bool            // at least one set of search suggestions was collected
	events         io.Writer       // --debug-events: request and raw events as JSON lines
	interactionID  string          // ID of the interaction, for continuing a --session
	cutShort       error           // the connection was lost after the image arrived
}

func (p *progress) setStatus(msg string) { p.status.Set(msg) }

func (p *progress) thoughtText(s string) {
	if p.thoughts {
		p.thought.WriteString(s)
	}
}

// flushThought prints buffered thought text as one block.
func (p *progress) flushThought() {
	text := sanitize(strings.TrimSpace(p.thought.String()))
	p.thought.Reset()
	if text == "" {
		return
	}
	if p.color {
		text = colorThought(text)
	}
	p.status.Do(func() { fmt.Fprintf(p.errOut, "%s\n\n", text) })
}

func (p *progress) interimImageNote() {
	fmt.Fprintln(p.errOut, paint(p.color, sgrDim, "[interim image]"))
}

func (p *progress) thoughtImage(data string) {
	if !p.thoughts {
		return
	}
	p.flushThought()
	img, err := base64.StdEncoding.DecodeString(data)
	if err != nil || p.preview == nil {
		p.status.Do(p.interimImageNote)
		return
	}
	p.status.Do(func() {
		if err := kitty.Preview(p.preview, img, 2); err == nil {
			p.shownPreview = img
		} else {
			p.interimImageNote()
		}
	})
}

// streamImage runs req as a streamed interaction, reporting progress as events
// arrive, and returns the final image and its MIME type.
func streamImage(ctx context.Context, client *genai.Client, req interactions.CreateModelInteraction, p *progress) (img []byte, mimeType string, err error) {
	req.Stream = genai.Ptr(true)
	start, events, lastEvent := time.Now(), 0, time.Duration(0)
	if p.events != nil {
		if b, err := json.Marshal(req); err == nil {
			fmt.Fprintf(p.events, "{\"request\":%s}\n", b)
		}
		// A last line with the timing and the outcome, to tell a long silence
		// before a dropped connection from a sudden failure.
		defer func() {
			end := map[string]any{
				"elapsed_s":    time.Since(start).Round(time.Millisecond).Seconds(),
				"last_event_s": lastEvent.Round(time.Millisecond).Seconds(),
				"events":       events,
			}
			if err != nil {
				end["error"] = err.Error()
			}
			b, _ := json.Marshal(map[string]any{"end": end})
			fmt.Fprintf(p.events, "%s\n", b)
		}()
	}
	res, err := client.Interactions.Create(ctx, operations.CreateInteractionRequest{
		Body: operations.NewCreateInteractionRequestBody(req),
	})
	if err != nil {
		return nil, "", readableAPIError(err)
	}
	stream := res.InteractionSSEStreamEvent
	if stream == nil {
		return nil, "", errors.New("expected a streamed response")
	}
	defer stream.Close()
	// Thoughts are printed when their step stops; show the ones an error cut off.
	defer p.flushThought()

	c := newCollector(p)
	for stream.Next() {
		events, lastEvent = events+1, time.Since(start)
		if p.events != nil {
			fmt.Fprintf(p.events, "%s\n", stream.RawValue())
		}
		if err := c.handle(stream.Value()); err != nil {
			return nil, "", err
		}
	}
	if err := stream.Err(); err != nil {
		// When the connection is lost, the interaction ends and it can't be
		// resumed, so only what has arrived is left.
		var netErr net.Error
		lost := ctx.Err() == nil && (errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &netErr))
		switch {
		case !lost:
			return nil, "", err
		case c.final != nil && c.final.Status == interactions.InteractionSseEventInteractionStatusCompleted:
			// Lost after interaction.completed: only the closing [DONE] is missing.
			// (The SDK's automatic continuation also passes on a completed event
			// with status requires_continuation at the end of each part.)
		case c.imageDone:
			p.cutShort = err
		default:
			p.setStatus("connection lost, checking the interaction")
			if why := failure(ctx, client, p.interactionID); why != "" {
				return nil, "", fmt.Errorf("generation failed: %s", why)
			}
			return nil, "", fmt.Errorf("connection to the API lost before the image arrived, try again: %w", err)
		}
	}
	p.noteMissingSuggestions(c.searches)
	return c.result()
}

// failureRetryDelay is the wait between failure's lookups; tests shorten it.
var failureRetryDelay = time.Second

// failure returns why the interaction id failed, or "" if it didn't.
// The API sometimes fails an interaction by just closing the stream, without
// an error event; the error is only on the interaction itself. Right after
// the drop the API may not have recorded the outcome yet, so while the
// interaction still looks like it's running, it's looked up again, three
// times in all. (The SDK already retries lookups that fail.)
func failure(ctx context.Context, client *genai.Client, id string) string {
	if id == "" {
		return ""
	}
	for attempt := 1; ; attempt++ {
		res, err := client.Interactions.Get(ctx, operations.GetInteractionByIDRequest{ID: id})
		if err != nil || res.Interaction == nil {
			return ""
		}
		switch res.Interaction.Status {
		case interactions.InteractionStatusFailed:
			var msgs []string
			for _, e := range res.Interaction.Errors {
				msgs = append(msgs, errorText(&e))
			}
			if len(msgs) == 0 {
				return "unknown error"
			}
			return strings.Join(msgs, "; ")
		case interactions.InteractionStatusInProgress, interactions.InteractionStatusQueued:
			if attempt == 3 {
				return ""
			}
		default:
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(failureRetryDelay):
		}
	}
}

// errorText renders an API error as "message (code)".
func errorText(e *interactions.Error) string {
	msg := "unknown error"
	if e != nil && e.Message != nil && *e.Message != "" {
		msg = oneLine(*e.Message)
	}
	if e != nil && e.Code != nil && *e.Code != "" {
		msg += " (" + oneLine(*e.Code) + ")"
	}
	return msg
}

// readableAPIError turns the SDK's errors for API error responses, which
// print as raw JSON, into "message (code)".
func readableAPIError(err error) error {
	var (
		clientErr *apierrors.CreateInteractionClientError
		serverErr *apierrors.CreateInteractionServerError
	)
	switch {
	case errors.As(err, &clientErr):
		return errors.New(errorText(&clientErr.Error_))
	case errors.As(err, &serverErr):
		return errors.New(errorText(&serverErr.Error_))
	}
	return err
}

// collector turns stream events into progress updates and gathers the output.
type collector struct {
	p         *progress
	stepTypes map[int]interactions.StepType
	images    map[int][]*interactions.ImageDelta // image deltas per model_output step
	lastImage int                                // step index of the latest image, -1 if none
	imageDone bool                               // the latest image's step has stopped
	text      strings.Builder                    // model text output, e.g. a refusal
	status    string
	final     *interactions.InteractionSseEventInteraction
	searches  int // Google searches the completed events report
}

func newCollector(p *progress) *collector {
	return &collector{
		p:         p,
		stepTypes: map[int]interactions.StepType{},
		images:    map[int][]*interactions.ImageDelta{},
		lastImage: -1,
	}
}

func (c *collector) handle(ev *interactions.InteractionSSEStreamEvent) error {
	if ev == nil {
		return nil
	}
	if e := ev.GetDataError(); e != nil {
		msg := errorText(e.Error)
		return fmt.Errorf("generation failed: %s", msg)
	}
	if cr := ev.GetDataInteractionCreated(); cr != nil {
		c.p.interactionID = cr.Interaction.ID
		c.p.setStatus("starting")
	}
	if u := ev.GetDataInteractionStatusUpdate(); u != nil {
		c.status = string(u.Status)
		c.p.setStatus(strings.ReplaceAll(c.status, "_", " "))
	}
	if s := ev.GetDataStepStart(); s != nil {
		c.stepTypes[s.Index] = s.Step.Type
		switch s.Step.Type {
		case interactions.StepTypeThought:
			c.p.setStatus("thinking")
		case interactions.StepTypeModelOutput:
			c.p.setStatus("rendering")
		case interactions.StepTypeGoogleSearchCall:
			var queries []string
			if call := s.Step.GoogleSearchCallStep; call != nil {
				queries = call.Arguments.Queries
			}
			c.searching(queries)
		case interactions.StepTypeGoogleSearchResult:
			if res := s.Step.GoogleSearchResultStep; res != nil {
				c.p.searchResult(res.Result)
			}
		default:
			c.p.setStatus(strings.ReplaceAll(string(s.Step.Type), "_", " "))
		}
	}
	if d := ev.GetDataStepDelta(); d != nil {
		c.delta(d)
	}
	if s := ev.GetDataStepStop(); s != nil {
		switch c.stepTypes[s.Index] {
		case interactions.StepTypeThought:
			c.p.flushThought()
		case interactions.StepTypeModelOutput:
			if c.lastImage >= 0 {
				c.p.setStatus("finishing")
			}
			if s.Index == c.lastImage {
				c.imageDone = true
			}
		}
	}
	if done := ev.GetDataInteractionCompleted(); done != nil {
		c.final = &done.Interaction
		if done.Interaction.ID != "" {
			c.p.interactionID = done.Interaction.ID
		}
		c.status = string(done.Interaction.Status)
		c.groundingFromSteps(done.Interaction.Steps)
		c.searches += searchCount(done.Interaction.Usage) // each part reports its own
	}
	return nil
}

// searching shows a search in the status line, unless the image has already
// arrived: the API reports the search steps after the image even though the
// search ran while the model was thinking, and "searching" after "rendering"
// would suggest the results came too late to be used.
func (c *collector) searching(queries []string) {
	if c.lastImage >= 0 {
		return
	}
	if len(queries) == 0 {
		c.p.setStatus("searching")
		return
	}
	c.p.searching(queries)
}

// groundingFromSteps shows search suggestions and sources included in the
// completed interaction, in case they weren't streamed as deltas. Anything
// already printed is skipped.
func (c *collector) groundingFromSteps(steps []interactions.Step) {
	for _, s := range steps {
		if res := s.GoogleSearchResultStep; res != nil {
			c.p.searchResult(res.Result)
		}
		if out := s.ModelOutputStep; out != nil {
			for _, content := range out.Content {
				if t := content.TextContent; t != nil {
					c.p.citations(t.Annotations)
				}
			}
		}
	}
}

func (c *collector) delta(d *interactions.StepDelta) {
	if img := d.GetDeltaImage(); img != nil && img.Data != nil {
		c.images[d.Index] = append(c.images[d.Index], img)
		c.lastImage = d.Index
		c.imageDone = false
	}
	if t := d.GetDeltaText(); t != nil {
		c.text.WriteString(t.Text)
	}
	if call := d.GetDeltaGoogleSearchCall(); call != nil {
		c.searching(call.Arguments.Queries)
	}
	if res := d.GetDeltaGoogleSearchResult(); res != nil {
		c.p.searchResult(res.Result)
	}
	if a := d.GetDeltaTextAnnotationDelta(); a != nil {
		c.p.citations(a.Annotations)
	}
	if ts := d.GetDeltaThoughtSummary(); ts != nil && ts.Content != nil {
		if t := ts.Content.TextContent; t != nil {
			c.p.thoughtText(t.Text)
		}
		if img := ts.Content.ImageContent; img != nil && img.Data != nil {
			c.p.thoughtImage(*img.Data)
		}
	}
}

// result returns the final image: the latest image streamed in a model_output
// step, falling back to the steps in the interaction.completed event.
func (c *collector) result() ([]byte, string, error) {
	if parts := c.images[c.lastImage]; len(parts) > 0 {
		return joinImage(parts)
	}
	if c.final != nil {
		for i := len(c.final.Steps) - 1; i >= 0; i-- {
			out := c.final.Steps[i].ModelOutputStep
			if out == nil {
				continue
			}
			for j := len(out.Content) - 1; j >= 0; j-- {
				if img := out.Content[j].ImageContent; img != nil && img.Data != nil {
					data, err := base64.StdEncoding.DecodeString(*img.Data)
					if err != nil {
						return nil, "", fmt.Errorf("decoding image: %w", err)
					}
					mime := ""
					if img.MimeType != nil {
						mime = string(*img.MimeType)
					}
					return data, mime, nil
				}
			}
		}
	}
	msg := "no image in response"
	if c.status != "" {
		msg += " (status " + c.status + ")"
	}
	if t := strings.TrimSpace(c.text.String()); t != "" {
		msg += ": " + t
	}
	return nil, "", errors.New(msg)
}

// joinImage decodes the image deltas of one step. The docs show each delta
// carrying a whole image but also call them chunks, so if the last delta isn't
// a valid image on its own, the deltas are joined as parts of one image.
func joinImage(parts []*interactions.ImageDelta) ([]byte, string, error) {
	last := parts[len(parts)-1]
	mime := ""
	if last.MimeType != nil {
		mime = string(*last.MimeType)
	}
	data, err := base64.StdEncoding.DecodeString(*last.Data)
	if err == nil && (len(parts) == 1 || isImage(data)) {
		return data, mime, nil
	}
	var all strings.Builder
	for _, p := range parts {
		all.WriteString(*p.Data)
	}
	data, err = base64.StdEncoding.DecodeString(all.String())
	if err != nil {
		return nil, "", fmt.Errorf("decoding image: %w", err)
	}
	return data, mime, nil
}

func isImage(b []byte) bool {
	_, _, err := image.DecodeConfig(bytes.NewReader(b))
	return err == nil
}
