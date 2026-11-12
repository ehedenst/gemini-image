# gemini-image

Command-line tool for generating images with the Google Gemini API ("Nano Banana" models).

## Install

```sh
go build                # builds ./gemini-image
# or
go install github.com/ehedenst/gemini-image@latest
```

Embed a version string with:

```sh
go build -ldflags "-X github.com/ehedenst/gemini-image/cmd.version=$(git describe --tags --always)"
```

## Configuration

| Setting      | Flag             | Environment variable  | Default                  |
| ------------ | ---------------- | --------------------- | ------------------------ |
| API key      | `--api-key`      | `GEMINI_API_KEY`      | none (required)          |
| Model        | `--model`        | `GEMINI_MODEL`        | `gemini-nano-banana-2.1` |
| Aspect ratio | `--aspect-ratio` | `GEMINI_ASPECT_RATIO` | model's choice           |
| Image size   | `--size`         | `GEMINI_SIZE`         | model's choice           |
| Preview      | `--preview`      | `GEMINI_PREVIEW`      | `true`                   |
| Thoughts     | `--thoughts`     | `GEMINI_THOUGHTS`     | `false`                  |
| Search       | `--search`       | `GEMINI_SEARCH`       | off                      |

Flags take precedence over environment variables. You can get an API key at
<https://aistudio.google.com/apikey>.

### .env file

On startup the CLI loads `.env` from the current directory if it exists. To use another
file, pass `--env-file path`; in that case a missing file is an error. Variables that are already
set in your shell take precedence over values in the file. See [.env.example](.env.example).

### Running a command for a value

If an environment variable's value starts with `!`, the rest of the value is run as a shell command (`sh -c`).
The command's output, with surrounding whitespace trimmed, becomes the value. This lets you keep the key
in a password manager:

```sh
GEMINI_API_KEY="!op read 'op://Vault/Gemini API Key/credential'"
```

The command runs only when the value is needed and no flag overrides it.

> **Security:** anyone who can write the `.env` file in your working directory can run commands as you.
> Don't run `gemini-image` in directories you don't trust.

## Usage

```sh
gemini-image generate "A watercolor fox in a snowy forest"
gemini-image generate -o fox.png "A watercolor fox in a snowy forest"
gemini-image generate --aspect-ratio 16:9 --size 2K "A mountain panorama at dawn"
gemini-image generate --thoughts "An infographic explaining photosynthesis"
gemini-image generate --search web --aspect-ratio 16:9 "This week's weather in Stockholm as a chart"
```

| Flag             | Description                                                                         |
| ---------------- | ----------------------------------------------------------------------------------- |
| `-o, --output`   | Output file. Default: `image-<timestamp>.<ext>`, extension from the returned type. |
| `--aspect-ratio` | `1:1`, `2:3`, `3:2`, `3:4`, `4:3`, `4:5`, `5:4`, `9:16`, `16:9`, `21:9`, `1:4`, `4:1`, `1:8`, `8:1` |
| `--size`         | `512`, `1K`, `2K`, `4K`                                                             |
| `--preview`      | Show the image in the terminal (default `true`; disable with `--preview=false`).   |
| `--thoughts`     | Print the model's thought summaries and preview its interim images.                |
| `--search`       | Ground the image in Google Search: `web`, `image` or `web,image`.                   |

Not every model supports every aspect ratio or size. If you leave a flag out, the model's default applies.

### Progress and thoughts

The response is streamed. While the image is generated, a status line on stderr shows the current
stage (`thinking`, `rendering`, …) and the elapsed time. The status line only appears when stderr
is a terminal.

Gemini 3 image models think before rendering and may draw up to two interim images to try out the
composition. With `--thoughts`, the CLI asks for thought summaries and prints them to stderr as they
arrive. It also previews the interim images in the terminal; where previews aren't available, it
prints `[interim image]` instead. If the last interim image is the same as the final image, it isn't
drawn twice. Older models such as `gemini-2.5-flash-image` don't think, so they only report status.

On a terminal, stderr output is colored: thought headings stand out and the thought text is dimmed,
search suggestions and sources get colored labels and links, and notes, warnings and errors are
highlighted. Set `NO_COLOR` to any non-empty value to turn colors off. Output that isn't going to a
terminal is never colored.

### Grounding with Google Search

With `--search`, the model can look things up before it draws. This is useful for current events,
weather, scores and other real-world facts:

- `web` searches the web for facts.
- `image` uses Google Image Search results as visual references. Only `gemini-nano-banana-2.1` and
  `gemini-3.1-flash-image` support it.
- `web,image` enables both.

`gemini-3.1-flash-lite-image` doesn't support grounding at all.

While it searches, the status line shows the queries. Google's
[terms](https://ai.google.dev/gemini-api/terms#grounding-with-google-search) require grounded results
to be shown together with the Google Search suggestions that come with them, so the CLI always prints
the suggestions to stderr. It prints them after the image and the terminal preview, just before the
file path, and leaves room for them below the preview so they stay on screen. On a terminal they're
clickable links; otherwise they're printed as `text <url>`. Any sources the model cites are printed
too.

The model decides for itself whether to search. If it can draw the prompt from what it already knows,
it may not search at all, and then there are no suggestions to show. Image search mostly happens when
the prompt needs visual references, or when it asks for them ("Search Google Images for reference
photos of …"). Image search alone can't look up facts such as a weather forecast; use `web` for that.

Now and then the API reports that the model searched but sends no search suggestions. In that case
the CLI prints `note: the model used Google Search (N searches), but the API sent no search
suggestions`, so it's clear the suggestions are missing from the response rather than dropped by the CLI.

### Terminal preview

If your terminal supports the [kitty graphics protocol](https://sw.kovidgoyal.net/kitty/graphics-protocol/),
the CLI also shows the generated image in the terminal, scaled down to fit the window. Terminals with support
include kitty, Ghostty, WezTerm and Rex. Before drawing anything, the CLI asks the terminal whether it supports
the protocol. If it doesn't, or if stdout isn't a terminal (for example when output is piped), it skips the preview.
JPEG images are converted to PNG for display. Inside tmux, the preview usually doesn't show, because tmux doesn't
pass the protocol through by default.

The path of the saved image is printed to stdout, and errors go to stderr.
This makes the tool easy to use in scripts:

```sh
open "$(gemini-image generate 'A red bicycle')"
```

Exit codes: `0` success, `1` runtime or API error, `2` invalid arguments, flags or settings (including a
missing API key), `130` interrupted.

Shell completion (including values for `--aspect-ratio`, `--size` and `--search`) is available through
`gemini-image completion <bash|zsh|fish|powershell>`.

## Development

```text
main.go          entry point, only calls cmd.Execute()
cmd/
  root.go        root command, flags, Viper/env setup, exit codes
  generate.go    generate subcommand
  stream.go      streamed generation: events -> progress, final image
  status.go      animated status line on stderr
  color.go       ANSI colors for terminal output
  search.go      Google Search grounding: tool, suggestions, citations
  env.go         .env loading and "!command" values
internal/
  kitty/         terminal support detection and kitty graphics output
```

```sh
go test ./...
```

To see exactly what the API sends, use the hidden `--debug-events FILE` flag. It writes the request and
every raw stream event to the file as JSON lines. Image data is included, so the file can get large.
The file also contains search results, so treat it like the generated output.
