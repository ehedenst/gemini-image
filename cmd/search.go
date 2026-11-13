package cmd

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"google.golang.org/genai/interactions/models/interactions"
)

// searchTypes maps --search values to Google Search grounding types.
var searchTypes = map[string]interactions.GoogleSearchSearchType{
	"web":   interactions.GoogleSearchSearchTypeWebSearch,
	"image": interactions.GoogleSearchSearchTypeImageSearch,
}

// searchTools builds the Google Search tool from a comma-separated list of
// search types ("web", "image"). An empty spec disables grounding.
func searchTools(spec string) ([]interactions.Tool, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	var g interactions.GoogleSearch
	for s := range strings.SplitSeq(spec, ",") {
		t, ok := searchTypes[strings.ToLower(strings.TrimSpace(s))]
		if !ok {
			return nil, fmt.Errorf("invalid search type %q, valid: web, image", s)
		}
		if !slices.Contains(g.SearchTypes, t) {
			g.SearchTypes = append(g.SearchTypes, t)
		}
	}
	return []interactions.Tool{interactions.NewTool(g)}, nil
}

type link struct{ text, url string }

// sanitize drops control characters (except newline and tab) from text that
// comes from the API, so it can't inject terminal escape sequences.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}

// parseSuggestions extracts the links from the search_suggestions HTML
// snippet, which renders as a row of Google Search "chips".
func parseSuggestions(snippet string) []link {
	var (
		links []link
		cur   *link
		text  strings.Builder
	)
	z := html.NewTokenizer(strings.NewReader(snippet))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return links
		case html.StartTagToken:
			t := z.Token()
			if t.Data != "a" {
				continue
			}
			for _, a := range t.Attr {
				if a.Key == "href" && a.Val != "" {
					cur = &link{url: a.Val}
					text.Reset()
				}
			}
		case html.TextToken:
			if cur != nil {
				text.Write(z.Text())
			}
		case html.EndTagToken:
			if cur != nil && z.Token().Data == "a" {
				cur.text = strings.Join(strings.Fields(text.String()), " ")
				if cur.text == "" {
					cur.text = cur.url
				}
				links = append(links, *cur)
				cur = nil
			}
		}
	}
}

// searching shows the queries the model is running in the status line.
func (p *progress) searching(queries []string) {
	if len(queries) > 0 {
		p.setStatus("searching: " + strings.Join(queries, "; "))
	}
}

// searchResult collects the search suggestions that Google requires to be
// shown with grounded results. Each snippet is collected once.
func (p *progress) searchResult(results []interactions.GoogleSearchResult) {
	for _, r := range results {
		if r.SearchSuggestions == nil || p.seen(*r.SearchSuggestions) {
			continue
		}
		links := parseSuggestions(*r.SearchSuggestions)
		if len(links) == 0 {
			continue
		}
		p.grounding = append(p.grounding, paint(p.color, sgrLabel, "Google Search suggestions:"))
		for _, l := range links {
			p.grounding = append(p.grounding, "  "+p.link(l))
		}
		p.gotSuggestions = true
	}
}

// searchCount returns how many Google searches the usage reports.
func searchCount(u *interactions.Usage) int {
	n := 0
	for _, g := range u.GetGroundingToolCount() {
		if g.Type != nil && *g.Type == interactions.GroundingToolCountTypeGoogleSearch && g.Count != nil {
			n += *g.Count
		}
	}
	return n
}

// noteMissingSuggestions adds a note when the model searched but the API sent
// no search suggestions, which happens now and then. Without it the missing
// suggestions would look like a bug in the CLI.
func (p *progress) noteMissingSuggestions(searches int) {
	if searches == 0 || p.gotSuggestions {
		return
	}
	plural := "es"
	if searches == 1 {
		plural = ""
	}
	p.grounding = append(p.grounding, paint(p.color, sgrYellow, fmt.Sprintf(
		"note: the model used Google Search (%d search%s), but the API sent no search suggestions", searches, plural)))
}

// printGrounding prints the collected search suggestions and sources. It runs
// after the image preview, so they appear right below the image instead of
// being scrolled off the screen by it.
func (p *progress) printGrounding() {
	for _, line := range p.grounding {
		fmt.Fprintln(p.errOut, line)
	}
	p.grounding = nil
}

// citations collects the sources cited for the generated content, once each.
func (p *progress) citations(annotations []interactions.Annotation) {
	for _, a := range annotations {
		c := a.URLCitation
		if c == nil || c.URL == nil || p.seen(*c.URL) {
			continue
		}
		l := link{url: *c.URL, text: *c.URL}
		if c.Title != nil && *c.Title != "" {
			l.text = *c.Title
		}
		p.grounding = append(p.grounding, paint(p.color, sgrLabel, "Source:")+" "+p.link(l))
	}
}

// seen reports whether key was already printed, and records it.
func (p *progress) seen(key string) bool {
	if p.printed == nil {
		p.printed = map[string]bool{}
	}
	if p.printed[key] {
		return true
	}
	p.printed[key] = true
	return false
}

// link renders l as a clickable OSC 8 hyperlink on terminals, otherwise as
// "text <url>". A line break would cut an OSC 8 sequence, so both are made
// one line.
func (p *progress) link(l link) string {
	l.text, l.url = oneLine(l.text), oneLine(l.url)
	if p.hyperlinks {
		return "\x1b]8;;" + l.url + "\x1b\\" + paint(p.color, sgrLink, l.text) + "\x1b]8;;\x1b\\"
	}
	if l.text == l.url {
		return l.url
	}
	return l.text + " <" + l.url + ">"
}
