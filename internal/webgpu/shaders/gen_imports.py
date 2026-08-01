#!/usr/bin/env python3
"""Add WESL import statements to shader modules based on cross-module symbol use."""

from __future__ import annotations

import re
from pathlib import Path

MODULES = [
    "types",
    "profile",
    "math",
    "texture",
    "sky",
    "intersect",
    "terrain",
    "flame",
    "instance",
    "bvh",
    "shade",
    "trace",
]

ROOT = Path(__file__).parent / "modules"

DEF_PATTERNS = [
    re.compile(r"^fn\s+(\w+)", re.M),
    re.compile(r"^const\s+(\w+)", re.M),
    re.compile(r"^struct\s+(\w+)", re.M),
    re.compile(r"^var<[^>]+>\s+(\w+)\s*:", re.M),
    re.compile(r"@group\([^)]*\)\s*@binding\([^)]*\)\s*var<[^>]+>\s+(\w+)\s*:", re.M),
]

# WGSL / WESL keywords and common builtins — never imported.
SKIP = {
    "fn",
    "const",
    "struct",
    "var",
    "if",
    "else",
    "for",
    "while",
    "loop",
    "break",
    "continue",
    "return",
    "let",
    "true",
    "false",
    "select",
    "abs",
    "min",
    "max",
    "clamp",
    "pow",
    "sqrt",
    "floor",
    "ceil",
    "sin",
    "cos",
    "tan",
    "atan2",
    "length",
    "normalize",
    "dot",
    "cross",
    "mix",
    "step",
    "smoothstep",
    "atomicAdd",
    "array",
    "vec2",
    "vec3",
    "vec4",
    "mat4x4",
    "f32",
    "u32",
    "i32",
    "bool",
    "textureDimensions",
    "import",
    "package",
}


def strip_header(text: str) -> tuple[str, str]:
    """Return (// comment header, body). Blank lines after comments are not header."""
    lines = text.splitlines(keepends=True)
    i = 0
    while i < len(lines) and lines[i].startswith("//"):
        i += 1
    return "".join(lines[:i]), "".join(lines[i:])


def defs_in(text: str) -> set[str]:
    found: set[str] = set()
    for pat in DEF_PATTERNS:
        found.update(pat.findall(text))
    return found


NO_IMPORTS = {"types"}


def uses_from(text: str, dep_syms: set[str], local_syms: set[str]) -> set[str]:
    needed: set[str] = set()
    for line in text.splitlines():
        stripped = line.strip()
        if stripped.startswith("//"):
            continue
        for sym in dep_syms:
            if sym in local_syms or sym in SKIP:
                continue
            if re.match(rf"^\s*{re.escape(sym)}\s*:", line):
                continue
            if re.search(r"\b" + re.escape(sym) + r"\b", line):
                needed.add(sym)
    return needed


def format_imports(needs: dict[str, set[str]]) -> str:
    if not needs:
        return ""
    lines: list[str] = []
    for mod in MODULES:
        syms = needs.get(mod)
        if not syms:
            continue
        items = ", ".join(sorted(syms))
        lines.append(f"import package::{mod}::{{{items}}};")
    return "\n".join(lines)


def assemble_module(header: str, imports: str, body: str) -> str:
    parts: list[str] = []
    h = header.rstrip("\n")
    if h:
        parts.append(h)
    imp = imports.strip()
    if imp:
        parts.append(imp)
    b = body.lstrip("\n")
    if b:
        parts.append(b)
    if not parts:
        return ""
    return "\n\n".join(parts) + "\n"


def strip_imports(text: str) -> str:
    lines = text.splitlines(keepends=True)
    out: list[str] = []
    for line in lines:
        if line.strip().startswith("import package::"):
            continue
        out.append(line)
    return "".join(out)


def main() -> None:
    ext = ".wesl"

    bodies: dict[str, str] = {}
    headers: dict[str, str] = {}
    symbols: dict[str, set[str]] = {}

    for name in MODULES:
        path = ROOT / f"{name}{ext}"
        text = strip_imports(path.read_text())
        header, body = strip_header(text)
        headers[name] = header
        bodies[name] = body
        symbols[name] = defs_in(body)

    for name in MODULES:
        body = bodies[name]
        local = symbols[name]
        needs: dict[str, set[str]] = {}
        if name not in NO_IMPORTS:
            for dep, dep_syms in symbols.items():
                if dep == name:
                    continue
                used = uses_from(body, dep_syms, local)
                if used:
                    needs[dep] = used

        imports = format_imports(needs)
        out = assemble_module(headers[name], imports, body)
        (ROOT / f"{name}{ext}").write_text(out)
        print(f"{name}: {sum(len(v) for v in needs.values())} imports from {len(needs)} modules")


if __name__ == "__main__":
    main()
