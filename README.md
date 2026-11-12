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
```

| Flag             | Description                                                                         |
| ---------------- | ----------------------------------------------------------------------------------- |
| `-o, --output`   | Output file. Default: `image-<timestamp>.<ext>`, extension from the returned type. |
| `--aspect-ratio` | `1:1`, `2:3`, `3:2`, `3:4`, `4:3`, `4:5`, `5:4`, `9:16`, `16:9`, `21:9`, `1:4`, `4:1`, `1:8`, `8:1` |
| `--size`         | `512`, `1K`, `2K`, `4K`                                                             |

Not every model supports every aspect ratio or size. If you leave a flag out, the model's default applies.

The path of the saved image is printed to stdout, and errors go to stderr.
This makes the tool easy to use in scripts:

```sh
open "$(gemini-image generate 'A red bicycle')"
```

Exit codes: `0` success, `1` runtime or API error, `2` invalid arguments, flags or settings (including a
missing API key), `130` interrupted.

Shell completion (including values for `--aspect-ratio` and `--size`) is available through
`gemini-image completion <bash|zsh|fish|powershell>`.

## Development

```text
main.go          entry point, only calls cmd.Execute()
cmd/
  root.go        root command, flags, Viper/env setup, exit codes
  generate.go    generate subcommand
  env.go         .env loading and "!command" values
```

```sh
go test ./...
```
