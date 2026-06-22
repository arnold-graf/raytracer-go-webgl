#!/usr/bin/env python3
"""Convert [[box]] and [[box.hole]] min/max to cx/cy/cz + width/height/depth."""

from __future__ import annotations

import ast
import re
import sys
from pathlib import Path


def fmt(v: float) -> str:
    if abs(v - round(v)) < 1e-9:
        return str(int(round(v)))
    return f"{v:.6g}"


def parse_vec_line(line: str) -> tuple[str, list[float], str] | None:
    m = re.match(r"^\s*(min|max)\s*=\s*\[(.*)\]\s*(#.*)?$", line)
    if not m:
        return None
    inner, comment = m.group(2).strip(), m.group(3) or ""
    if "{{" in inner:
        return None
    try:
        vals = ast.literal_eval(f"[{inner}]")
    except (SyntaxError, ValueError):
        return None
    if len(vals) != 3 or not all(isinstance(x, (int, float)) for x in vals):
        return None
    return m.group(1), [float(x) for x in vals], comment


def extent_lines(mn: list[float], mx: list[float], indent: str, comment: str) -> list[str]:
    cx = (mn[0] + mx[0]) / 2
    cy = (mn[1] + mx[1]) / 2
    cz = (mn[2] + mx[2]) / 2
    w = abs(mx[0] - mn[0])
    h = abs(mx[1] - mn[1])
    d = abs(mx[2] - mn[2])
    lines = [
        f"{indent}cx = {fmt(cx)}",
        f"{indent}cy = {fmt(cy)}",
        f"{indent}cz = {fmt(cz)}",
        f"{indent}width = {fmt(w)}",
        f"{indent}height = {fmt(h)}",
        f"{indent}depth = {fmt(d)}",
    ]
    if comment:
        lines[-1] += " " + comment
    return [ln + "\n" for ln in lines]


def convert_file(path: Path) -> bool:
    lines = path.read_text().splitlines(keepends=True)
    out: list[str] = []
    i = 0
    changed = False
    while i < len(lines):
        parsed = parse_vec_line(lines[i].rstrip("\n"))
        if parsed is None:
            out.append(lines[i])
            i += 1
            continue

        first_key, first_vals, first_comment = parsed
        mn = mx = None
        min_idx = max_idx = i
        comment = first_comment
        if first_key == "min":
            mn, min_idx = first_vals, i
        else:
            mx, max_idx = first_vals, i

        j = i + 1
        while j < len(lines) and j <= i + 4:
            p2 = parse_vec_line(lines[j].rstrip("\n"))
            if p2:
                key2, vals2, c2 = p2
                if key2 == "min" and mn is None:
                    mn, min_idx = vals2, j
                    if not comment:
                        comment = c2
                elif key2 == "max" and mx is None:
                    mx, max_idx = vals2, j
                    if not comment:
                        comment = c2
                if mn is not None and mx is not None:
                    break
            elif lines[j].strip() and not lines[j].strip().startswith("#"):
                break
            j += 1

        if mn is not None and mx is not None:
            indent = re.match(r"^(\s*)", lines[i]).group(1)
            out.extend(extent_lines(mn, mx, indent, comment))
            changed = True
            i = max(min_idx, max_idx) + 1
            continue

        out.append(lines[i])
        i += 1

    if changed:
        path.write_text("".join(out))
    return changed


def main() -> int:
    root = Path(__file__).resolve().parents[1] / "scenes"
    n = 0
    for path in sorted(root.rglob("*.toml")):
        if convert_file(path):
            print("converted", path.relative_to(root.parents[0]))
            n += 1
    print(f"done: {n} files updated")
    return 0


if __name__ == "__main__":
    sys.exit(main())
