# AGENTS.md

`gemini-image` is a Go CLI that generates and edits images with the Google Gemini Interactions API
(`google.golang.org/genai`). See `README.md` for what each command and flag does and for the file
layout ("Development" section).

## Build and test

```sh
go build                                  # must work from the repo root; builds ./gemini-image
gofmt -l . && go vet ./...
go test -count=1 -shuffle=on ./...        # tests share global Cobra state; keep them order-independent
GOOS=linux go build -o /dev/null . && GOOS=windows go build -o /dev/null .
```

Rebuild `./gemini-image` after every change: it's what gets run by hand.

## Layout

- `main.go` only calls `cmd.Execute()`.
- `cmd/`: one Cobra command or feature per file, each with a `_test.go`.
- `internal/kitty/`: kitty graphics protocol output (`Preview`, `Thumbnail`). `kitty_other.go` is
  the non-Unix stub; keep its exported functions in sync.
- `testdata/`: 512×512 portraits for reference-image tests; reach them from `cmd/` as
  `../testdata/<name>.jpg`. `testdata/README.md` has the prompts used to make them.

## CLI conventions

- Cobra and Viper. Config precedence: flag, then env (`GEMINI_*`), then `.env`, then defaults.
  A `GEMINI_API_KEY` value starting with `!` is run with `sh -c` (`setting` in `cmd/env.go`),
  so the key can stay in a password manager; only the key gets this, other settings are read
  from Viper as they are.
- stdout is for output only (e.g. the saved image path); progress, warnings, notes and errors go
  to stderr.
- Exit codes: 0 ok, 1 runtime error, 2 usage error (wrap in `usageError`, including from
  `Args` validators), 130 interrupted. `SilenceUsage`/`SilenceErrors` are on; `Execute` prints
  `error: …`.
- Color only via `cmd/color.go` (`useColor`, `paint`); it respects `NO_COLOR` and non-TTYs.
- Image previews go through `internal/kitty`, are gated by the `preview` setting (the global
  `--preview` flag, `GEMINI_PREVIEW`) in every command that draws, and treat
  `errors.ErrUnsupported` as "silently skip". Other preview errors are a yellow `warning:`.
- Google Search suggestions must always be printed when the API returns them (terms of
  service), after the image preview so they stay on screen.
- The API cancels an interaction when its stream connection drops, and streams can't be resumed
  (no SSE event IDs; `Interactions.Get` with `stream=true` is "not supported for this model").
  So `streamImage` keeps a complete image that arrived before the drop, and nothing else.
  The API also fails interactions by just closing the stream (no error event); the error is
  then only on the interaction (`status: failed`, `errors`), which `failure` looks up.
- Session history lives in `$XDG_STATE_HOME/gemini-image/sessions/NAME.json` and is written
  atomically (temp file + rename). Session names must match `sessionName` in `cmd/session.go`.
- Keep `README.md` in step with user-visible changes, including the file list under
  "Development".

## Testing

- Never call the real API in tests. Point the client at a fake server with
  `GOOGLE_GEMINI_BASE_URL`; helpers `fakeClient`, `sse`, `run`, `testPNG`, `imageEvents` and
  `generateCLI` are in `cmd/stream_test.go`.
- CLI-level tests drive `rootCmd` directly. Flag values, `Changed` and command contexts persist
  between tests, so any test that sets flags or runs `Execute` calls `resetCLI(t)` (in
  `cmd/stream_test.go`). pflag's `Visit` still lists flags whose `Changed` was reset; check
  `f.Changed` in `VisitAll` instead. For `generate` runs against a fake API, use `generateCLI`.
- Use `t.Setenv("XDG_STATE_HOME", t.TempDir())` for anything touching sessions.
- Camera code is tested through `cameraArgs`, `parseAVFoundation` and `listV4L2` with fixed
  inputs; don't open a real camera in tests.
- The hidden `--debug-events FILE` flag dumps the request and raw stream events as JSON lines,
  useful for writing fakes from real responses, then an `end` line with timing and the error.

## Working agreements

- Ask before running anything that spends real API generations, and before using the camera.
- Commit only when asked; never push.
- Don't commit `.env`, generated `image-*` files or the `gemini-image` binary (all gitignored).
