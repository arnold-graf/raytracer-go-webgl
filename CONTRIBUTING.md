# Contributing

Thanks for considering a contribution to this project.

## Contributor License Agreement

This project uses a [Contributor License Agreement (CLA)](CLA.md). By
contributing, you grant Arnold Graf the rights needed to maintain, distribute,
and **relicense** the project — including under proprietary or commercial terms
in the future — while the public codebase remains available under Apache 2.0.

**Before your first pull request:**

1. Read [CLA.md](CLA.md) (individuals) or [CLA-corporate.md](CLA-corporate.md)
   (if your employer owns your contributions).
2. Add your name to [CLA-signatures.md](CLA-signatures.md) in the same commit
   or pull request as your contribution.

Corporate contributors should have an authorized representative contact the
maintainer per `CLA-corporate.md` instead of signing individually.

## Pull requests

- Keep changes focused; match existing code style and conventions.
- If you change scene TOML schema (`internal/sceneio/toml.go`), update the
  matching JSON Schema in `schemas/`.
- For new or edited scene objects, run preview and check the renders (see
  `AGENTS.md`).

## License

Contributions are licensed to the public under Apache 2.0. The CLA additionally
grants the project maintainer the rights described in `CLA.md`.
