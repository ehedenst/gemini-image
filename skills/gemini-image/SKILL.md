---
name: gemini-image
description: Generates and edits images with Google Gemini ("Nano Banana") through the gemini-image CLI. Use when the user asks to create, draw, render or illustrate an image, picture, photo, logo, icon, poster or illustration, to edit, restyle or combine existing images, or to refine an image over several turns.
license: MIT
compatibility: Needs internet access and a Gemini API key. Runs on macOS, Linux and Windows (amd64, arm64); the install scripts need curl, tar and sha256sum or shasum (macOS, Linux) or PowerShell (Windows).
metadata:
  author: Erik Hedenström
  repository: https://github.com/ehedenst/gemini-image
---

# gemini-image

`gemini-image generate` sends a prompt (and optional reference images) to the Gemini API and saves
the image it gets back. **stdout is only the saved file's path**; progress, notes and errors go to
stderr.

## Setup

1. Check whether it's installed: `command -v gemini-image`. If not, install the latest release:

   ```bash
   sh scripts/install.sh                                        # macOS, Linux
   powershell -ExecutionPolicy Bypass -File scripts/install.ps1 # Windows
   ```

   The script prints the binary's path on stdout. It installs into `~/.local/bin`
   (Windows: `%LOCALAPPDATA%\Programs\gemini-image`); if that's not on `PATH`, run the binary by the
   printed path. Running it again does nothing if the latest version is already there, and it
   updates an older one. `--help` shows its options. With Go installed,
   `go install github.com/ehedenst/gemini-image@latest` works too.

2. The API key comes from `GEMINI_API_KEY`, in the environment or in a `.env` file in the current
   directory. If the command fails with `no API key`, ask the user to set it. Don't ask them to paste
   the key into the conversation, and never print it. A value starting with `!` is a command that
   outputs the key (e.g. `GEMINI_API_KEY="!op read 'op://Vault/Gemini/credential'"`).

## Generate an image

```bash
gemini-image generate -o fox.png "A watercolor fox in a snowy forest, soft morning light"
```

- Always pass `-o` with a name that fits the request. `.png`, `.jpg` and `.gif` convert the image to
  that type; other image types (such as `.webp`) are rejected. If the file exists, `-2`, `-3`, … is
  added to the name, so **use the path printed on stdout**, not the name you passed.
- Describe the image fully: subject, setting, style, composition, lighting, and any text to render
  (in quotes).
- `--aspect-ratio 16:9` (1:1, 2:3, 3:2, 3:4, 4:3, 4:5, 5:4, 9:16, 16:9, 21:9, 1:4, 4:1, 1:8, 8:1) and
  `--size 2K` (512, 1K, 2K, 4K). Leave them out to let the model choose.
- After it's saved, look at the image file to check it matches the request before you report back.

## Edit or combine images

`-i FILE` adds a reference image (repeat it, up to 14). Refer to them in the prompt by order:

```bash
gemini-image generate -i photo.jpg -o photo-watercolor.png "Turn this photo into a watercolor painting"
gemini-image generate -i man.jpg -i cafe.jpg -o scene.png "The man from the first image sitting in the cafe from the second"
```

## Refine over several turns

A session continues from the previous result, so follow-up prompts only describe the change:

```bash
gemini-image generate --session poster -o poster-1.png "A retro travel poster of Stockholm"
gemini-image generate --session poster -o poster-2.png "Make the sky orange"
gemini-image session poster                     # list the steps
gemini-image generate --session poster --step 1 -o poster-3.png "Show it at night instead"
```

Session names start with a letter or digit and may contain letters, digits, `.`, `_` and `-`.
`--step N` branches from step N instead of the last step. `gemini-image sessions` lists all sessions.

## Ground in Google Search

`--search web` lets the model look up facts first (weather, events, scores, current products);
`--search image` uses image search results as visual references; `--search all` does both. When
stderr contains `Google Search suggestions:`, Google's terms require showing them to the user:
include those lines in your reply.

## Exit codes

- `0`: saved. `130`: interrupted.
- `2`: bad arguments, such as an unknown aspect ratio or `.webp` output, or no API key. Nothing was
  generated; fix the command.
- `1`: the run failed, for example a reference image can't be read or the API returned an error
  (`error: <message> (<CODE>)`). If the model refused the prompt, rephrase it or ask the user rather
  than retrying the same prompt.

## Gotchas

- Every `generate` run is billed. Make one image per request unless the user asks for variations,
  and don't retry in a loop: the CLI already retries network errors.
- `--camera` takes a photo with the user's camera. Only use it when the user asks for it.
- Deleting sessions (`session --delete NAME`, `sessions --delete`) asks for confirmation and fails
  without a terminal unless `--force` is given. Only delete when the user asks; `--delete=all`
  deletes the session's images too.
- Terminal previews are skipped when there's no terminal, so look at the saved file instead.
- `--model` picks another model (default `gemini-nano-banana-2.1`), for example
  `gemini-3-pro-image`. Not every model supports every aspect ratio, size or search type; the API
  reports an error if not.
- Run `gemini-image generate --help` for every flag.
