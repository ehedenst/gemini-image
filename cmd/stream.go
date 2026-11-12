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
	"os"
	"strings"

	"github.com/ehedenst/gemini-image/internal/kitty"
	"google.golang.org/genai"
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

	thought      strings.Builder // thought text not yet printed
	shownPreview []byte          // last interim image drawn, to avoid repeats
	printed      map[string]bool // suggestions and citations already printed
	events       io.Writer       // --debug-events: request and raw events as JSON lines
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
	p.status.Do(func() { fmt.Fprintf(p.errOut, "%s\n\n", text) })
}

func (p *progress) thoughtImage(data string) {
	if !p.thoughts {
		return
	}
	p.flushThought()
	img, err := base64.StdEncoding.DecodeString(data)
	if err != nil || p.preview == nil {
		p.status.Do(func() { fmt.Fprintln(p.errOut, "[interim image]") })
		return
	}
	p.status.Do(func() {
		if err := kitty.Preview(p.preview, img); err == nil {
			p.shownPreview = img
		} else {
			fmt.Fprintln(p.errOut, "[interim image]")
		}
	})
}

// streamImage runs req as a streamed interaction, reporting progress as events
// arrive, and returns the final image and its MIME type.
func streamImage(ctx context.Context, client *genai.Client, req interactions.CreateModelInteraction, p *progress) ([]byte, string, error) {
	req.Stream = genai.Ptr(true)
	if p.events != nil {
		if b, err := json.Marshal(req); err == nil {
			fmt.Fprintf(p.events, "{\"request\":%s}\n", b)
		}
	}
	res, err := client.Interactions.Create(ctx, operations.CreateInteractionRequest{
		Body: operations.NewCreateInteractionRequestBody(req),
	})
	if err != nil {
		return nil, "", err
	}
	stream := res.InteractionSSEStreamEvent
	if stream == nil {
		return nil, "", errors.New("expected a streamed response")
	}
	defer stream.Close()

	c := newCollector(p)
	for stream.Next() {
		if p.events != nil {
			fmt.Fprintf(p.events, "%s\n", stream.RawValue())
		}
		if err := c.handle(stream.Value()); err != nil {
			return nil, "", err
		}
	}
	if err := stream.Err(); err != nil {
		return nil, "", err
	}
	return c.result()
}

// collector turns stream events into progress updates and gathers the output.
type collector struct {
	p         *progress
	stepTypes map[int]interactions.StepType
	images    map[int][]*interactions.ImageDelta // image deltas per model_output step
	lastImage int                                // step index of the latest image, -1 if none
	text      strings.Builder                    // model text output, e.g. a refusal
	status    string
	final     *interactions.InteractionSseEventInteraction
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
		msg := "unknown error"
		if e.Error != nil && e.Error.Message != nil {
			msg = *e.Error.Message
		}
		return fmt.Errorf("generation failed: %s", msg)
	}
	if ev.GetDataInteractionCreated() != nil {
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
		}
	}
	if done := ev.GetDataInteractionCompleted(); done != nil {
		c.final = &done.Interaction
		c.status = string(done.Interaction.Status)
		c.groundingFromSteps(done.Interaction.Steps)
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
