# gemini-image

Command-line tool for generating images with the Google Gemini API ("Nano Banana" models).

## Install

Download the archive for your system from the
[releases](https://github.com/ehedenst/gemini-image/releases) and put `gemini-image` (`gemini-image.exe`
on Windows) somewhere on your `PATH`; `checksums.txt` has the SHA-256 of each archive. The macOS
binaries aren't signed, so if macOS blocks one you downloaded with a browser, run
`xattr -d com.apple.quarantine gemini-image`.

To build it yourself, you need Go 1.26 or later:

```sh
go build                # builds ./gemini-image
# or
go install github.com/ehedenst/gemini-image@latest
```

`gemini-image --version` shows the version Go records in the binary: the release with `go install`,
or a pseudo-version from the git commit (`v0.0.0-<date>-<commit>`, `+dirty` with uncommitted changes)
with `go build`. To set your own, use:

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

### Reading the API key from a command

If the value of `GEMINI_API_KEY` starts with `!`, the rest of the value is run as a shell command
(`sh -c`). The command's output, with surrounding whitespace trimmed, becomes the key. This lets you keep
the key in a password manager. Other settings aren't secret and are used as they are:

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
gemini-image generate -i man.jpg -i woman.jpg "The two of them having coffee in a Paris café"
```

| Flag             | Description                                                                         |
| ---------------- | ----------------------------------------------------------------------------------- |
| `-o, --output`   | Output file; `.png`, `.jpg`/`.jpeg` and `.gif` convert the image to that type. Default: `image-<timestamp>.<ext>` (`-2`, `-3`, … if taken), extension from the returned type. |
| `--aspect-ratio` | `1:1`, `2:3`, `3:2`, `3:4`, `4:3`, `4:5`, `5:4`, `9:16`, `16:9`, `21:9`, `1:4`, `4:1`, `1:8`, `8:1` |
| `--size`         | `512`, `1K`, `2K`, `4K`                                                             |
| `--preview`      | Show images in the terminal (default `true`; disable with `--preview=false`). Applies to all commands. |
| `--thoughts`     | Print the model's thought summaries and preview its interim images.                |
| `--search`       | Ground the image in Google Search: `web`, `image` or `all`.                        |
| `-i, --image`    | Reference image to include with the prompt. Repeat for more, at most 14.           |
| `--camera`       | Take a photo with the camera and add it as a reference image.                       |
| `--session`      | Continue (or start) a named multi-turn editing session.                             |
| `--step`         | With `--session`: continue from this step (1 = first) instead of the last one.      |

Not every model supports every aspect ratio or size. Per the docs, `1:4`, `4:1`, `1:8` and `8:1` are for
`gemini-nano-banana-2.1` and `gemini-3.1-flash-image`, and `512` is for `gemini-3.1-flash-image` (though
`gemini-nano-banana-2.1` accepts it too). If you leave a flag out, the model's default applies.

The API decides the image type (usually JPEG). If the extension given with `-o` is `.png`, `.jpg`,
`.jpeg` or `.gif` and the image is another type, it's converted to that type: `-o fox.png` always gives a
PNG. JPEGs are written at quality 95, and GIFs are reduced to 256 colors. Converting writes only the
pixels, so any metadata in the API's file is not kept. Other image extensions, such as `.webp`, are an
error before anything is generated, as is an `-o` in a directory that doesn't exist. Any other
extension, or none, gets the image as the API sent it, with a warning if the extension suggests another
type.

### Reference images

With `-i FILE` (repeatable), the prompt is sent together with up to 14 images: to edit a photo, to
keep people looking the same across images, to include specific objects, or as style references.
Refer to them in the prompt, for example "the man from the first image". Images are sent in the
order given, after the prompt.

PNG, JPEG, WebP, HEIC, HEIF, GIF, BMP and TIFF are accepted. The type is detected from the file
content, or from the extension for HEIC, HEIF and TIFF. The 14-image limit is the most any model
accepts. Per the docs, `gemini-nano-banana-2.1` and `gemini-3.1-flash-image` keep up to 4
characters and 10 objects faithful, `gemini-3-pro-image` 5 characters, 6 objects and 3 style references, and
`gemini-2.5-flash-image` works best with up to 3 images. The API reports an error if a model doesn't
accept what you send.

With `--camera`, the CLI takes a photo with your camera and adds it as the last reference image:

```sh
gemini-image generate --camera "Turn me into a watercolor painting"
gemini-image generate --camera="iPhone Camera" -i hat.jpg "Put this hat on me"
```

It uses [ffmpeg](https://ffmpeg.org) (`brew install ffmpeg`), on macOS and Linux. Without a value
it uses the first camera: device 0 on macOS, normally the built-in camera, and `/dev/video0` on
Linux. To pick another, give its name or index on macOS, or its device file on Linux, with `=`
(`--camera=1`). `gemini-image cameras` lists them and marks the default (on Linux it leaves out
the metadata devices that cameras also have):

```console
$ gemini-image cameras
0  MacBook Pro Camera (default)
1  MacBook Pro Desk View Camera
2  iPhone Camera
```

The photo is taken after a second, once the exposure has settled, and only after all other flags have
been checked. On macOS it's landscape: 1920×1080, or 1280×720 if the camera can't do that, else the
camera's default. With `--preview` (the default), the photo is shown in the terminal before the
image is generated, so you can see what was sent. On macOS the terminal app needs camera access (System Settings → Privacy & Security
→ Camera). The photo isn't saved; only `camera` in the session flags shows it was used.

In a `--session`, reference images can be added to any turn; the session history records their full
paths.

### Multi-turn editing

With `--session NAME`, each run continues the conversation of the previous run with the same name,
so you can refine an image step by step:

```sh
gemini-image generate --session fox "A watercolor fox in a snowy forest"
gemini-image generate --session fox "Give the fox a red scarf"
gemini-image generate --session fox "Now make it night, with northern lights"
```

The first run with a new name starts the session. Every successful run is added to the session's
history in `$XDG_STATE_HOME/gemini-image/sessions/NAME.json` (by default
`~/.local/state/gemini-image/sessions/NAME.json`). The next run sends the ID of the latest
interaction as `previous_interaction_id`, so the model sees the earlier prompts and images. A failed
run doesn't advance the session. To start over, use a new name or delete the session with
`gemini-image session --delete NAME`.

To go back to an earlier version, pass `--step N` to continue from step N instead of the latest one.
Step 1 is the first run of the session. The new run is still added at the end of the history, and
later runs without `--step` continue from it:

```sh
gemini-image session fox                          # list the steps
gemini-image generate --session fox --step 1 "Give the fox a blue hat instead"
```

The history entry records `--step` in its flags, and `gemini-image session` marks such steps with
`(from step N)`, so you can see where each one branched off.

To drop the branches you didn't continue, run `gemini-image session --compact NAME`. It follows the
last step back to the first, through the step each one continued from, and keeps only those steps.
Their `--step` flags are removed, since each kept step now continues from the one before it, and the
steps are numbered again from 1. For steps 1, 2, 3, 4 (from step 2), 5 and 6 (from step 4), it keeps
1, 2, 4 and 6, which become steps 1 to 4. The images of the dropped steps are kept.

Each entry records the interaction ID, the prompt, the full paths of any reference images (`images`),
the full path of the generated image, the effective generation parameters (from flags, environment or
`.env`) and the flags given on the command line. `--api-key` is never stored.

```json
{
  "interactions": [
    {
      "id": "v1_ChdfQ1gy…",
      "prompt": "A watercolor fox in a snowy forest",
      "file": "/Users/me/images/image-20261112-213015.jpeg",
      "parameters": { "model": "gemini-nano-banana-2.1", "aspect_ratio": "16:9" },
      "flags": { "aspect-ratio": "16:9", "session": "fox" }
    }
  ]
}
```

To look back at a session, run `gemini-image session NAME`. For each turn it prints the prompt,
shows the image in the terminal (when the terminal supports it and `--preview` is on) and prints the image path. Images
that were moved or deleted get a warning on stderr.

```sh
gemini-image session fox
```

`gemini-image sessions` lists all sessions by name with their number of steps and, on a terminal
that supports it and with `--preview` on, a small thumbnail of each session's latest image:

```console
$ gemini-image sessions
coffee  2 steps
[thumbnail]

fox  3 steps
[thumbnail]
```

`gemini-image session --delete NAME` deletes a session's history, and `gemini-image session --compact
NAME` drops the steps the last one wasn't built on (see above). `gemini-image sessions --delete` and
`gemini-image sessions --compact` do the same for every session; a session that fails (for example a
damaged file) is reported at the end, and the others are still done. The generated images are kept.

To delete the generated images along with the history, use `--delete=all` (with the `=`; a bare
`--delete` is `--delete=history`). It deletes the image of every step, but not the reference images
(the ones given with `-i`). Images that are already gone are skipped. If an image can't be deleted,
the session's history is kept, so it still lists the images that are left.

Deleting asks for confirmation first, naming the sessions; only `y` or `yes` deletes. Any other
answer deletes nothing, which isn't an error (exit code 0). Without an answer, for example in a script
with no input, nothing is deleted and the command fails. Add `-f`/`--force` to delete without asking:

```console
$ gemini-image session --delete fox
delete session "fox"? The images are kept. [y/N] y
deleted session "fox"
$ gemini-image session --delete=all cat
delete session "cat" and its images? Reference images are kept. [y/N] y
deleted session "cat" and 3 images
$ gemini-image sessions --delete --force
```

The conversation itself is stored by the API, not locally. If the API no longer has the previous
interaction, continuing fails with an API error; start a new session then. Names may contain
letters, digits, `.`, `_` and `-`, and must start with a letter or digit.

### Progress and thoughts

The response is streamed. While the image is generated, a status line on stderr shows the current
stage (`thinking`, `rendering`, …) and the elapsed time. The status line only appears when stderr
is a terminal.

Gemini 3 image models think before rendering and may draw up to two interim images to try out the
composition. With `--thoughts`, the CLI asks for thought summaries and prints each one to stderr as soon
as it's complete. It also previews the interim images in the terminal; where previews aren't available, it
prints `[interim image]` instead. If the last interim image is the same as the final image, it isn't
drawn twice. Older models such as `gemini-2.5-flash-image` don't think, so they only report status.

On a terminal, stderr output is colored: thought headings stand out and the thought text is dimmed,
search suggestions and sources get colored labels and links, and notes, warnings and errors are
highlighted. Set `NO_COLOR` to any non-empty value to turn colors off. Output that isn't going to a
terminal is never colored.

If the connection to the API drops while the image is generated, the API cancels the generation, and it
can't be fetched or resumed afterwards. If the image had already arrived, the CLI saves it with a warning,
but doesn't add it to a `--session`; search suggestions and sources that hadn't arrived yet are missing.
Otherwise it fails with `connection to the API lost before the image arrived, try again`.

The API sometimes rejects a request by just closing the connection, often while the model is still
thinking. Then the CLI asks the API what happened and shows its error instead, for example
`generation failed: There was a problem processing your request. … (INVALID_ARGUMENT)`. Thoughts that
had arrived are still printed with `--thoughts`. In the cases seen so far, the model was planning to draw
real, named people (a cartoon about current news); trying again may lead to a different idea that works.
API errors are shown as `message (code)`.

### Grounding with Google Search

With `--search`, the model can look things up before it draws. This is useful for current events,
weather, scores and other real-world facts:

- `web` searches the web for facts.
- `image` uses Google Image Search results as visual references. Only `gemini-nano-banana-2.1` and
  `gemini-3.1-flash-image` support it.
- `all` enables both.

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

`--preview` is a global flag: `--preview=false` (or `GEMINI_PREVIEW=false`) turns off every image the CLI draws,
in `generate` (the image, interim images and the camera photo) as well as in `session` and `sessions`.

The path of the saved image is printed to stdout, and errors go to stderr.
This makes the tool easy to use in scripts:

```sh
open "$(gemini-image generate 'A red bicycle')"
```

Exit codes: `0` success, `1` runtime or API error, `2` invalid arguments, flags or settings (including a
missing API key), `130` interrupted with Ctrl-C (reported as `interrupted`, not as an error).

Shell completion (including values for `--aspect-ratio`, `--size` and `--search`) is available through
`gemini-image completion <bash|zsh|fish|powershell>`.

## Development

```text
main.go          entry point, only calls cmd.Execute()
.github/workflows/release.yml  builds and publishes a release for a vX.Y.Z tag
cmd/
  root.go        root command, flags, Viper/env setup, exit codes
  generate.go    generate subcommand
  stream.go      streamed generation: events -> progress, final image
  status.go      animated status line on stderr
  color.go       ANSI colors for terminal output
  session.go     --session: the session history file
  show.go        session and sessions subcommands: show, compact or delete one session or all
  search.go      Google Search grounding: tool, suggestions, citations
  images.go      reference images (-i): type detection, request input
  camera.go      --camera: photo via ffmpeg; cameras subcommand
  env.go         .env loading and "!command" values
internal/
  kitty/         terminal support detection and kitty graphics output
testdata/        portraits for the reference-image tests
```

```sh
go test -count=1 -shuffle=on ./...
```

To release, tag a commit on `main` and push the tag:

```sh
git tag v1.0.0 && git push origin v1.0.0
```

The release workflow checks that the tag is on `main` and runs the tests. It then builds macOS, Linux and
Windows binaries (amd64 and arm64) with the tag as their `--version`, and publishes them as a GitHub
release with checksums and notes generated from the commits.

To see exactly what the API sends, use the hidden `--debug-events FILE` flag. It writes the request and
every raw stream event to the file as JSON lines. Image data is included, so the file can get large.
The file also contains search results, so treat it like the generated output. The last line is
`{"end": {…}}` with the seconds the stream took (`elapsed_s`), when its last event arrived
(`last_event_s`), the number of events and any error, which shows whether a failed run went quiet first
or broke off suddenly.

## License

MIT, see [LICENSE](LICENSE).
