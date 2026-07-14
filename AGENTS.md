# Agent notes

## Visual verification

When creating or editing scene objects (`scenes/objects/*.toml`, included props,
furniture, etc.), **always verify the result visually** with the preview command
before considering the work done.

```bash
go run ./cmd/preview -scene scenes/preview/<name>.toml -o /tmp/<name>
```

Preview auto-centers the subject and writes twelve orbit screenshots
(`<name>-00.png` … `<name>-11.png`). Add or reuse a small preview scene under
`scenes/preview/` that includes the object on a simple floor with basic lighting
if one does not exist yet.

Do not rely on geometry math or unit tests alone for object authoring — run
preview and inspect the renders (especially front, side, and top views).
