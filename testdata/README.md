# Test data

Portraits of fictional people, generated with this CLI (`gemini-nano-banana-2.1`, 2026-11-12):

```sh
gemini-image generate --size 512 --aspect-ratio 1:1 -o testdata/<name>.jpg "<prompt> Photorealistic head-and-shoulders portrait of a fictional person, facing the camera, neutral friendly expression, plain light gray studio background, soft even lighting, no text."
```

| File        | Prompt start                                            |
| ----------- | ------------------------------------------------------- |
| `man.jpg`   | A Nigerian man in his thirties.                         |
| `woman.jpg` | A Japanese woman in her thirties.                       |
| `boy.jpg`   | An Indian boy, about ten years old.                     |
| `girl.jpg`  | A Swedish girl with blond hair, about eight years old.  |

All are 512×512 JPEGs. Go tooling ignores directories named `testdata`; tests in `cmd/` reach these
files as `../testdata/<name>.jpg`.
