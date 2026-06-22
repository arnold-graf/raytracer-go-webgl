#!/usr/bin/env python3
"""Convert box min/max or cx/cy/cz + size to pos_x/pos_y/pos_z + size."""

from __future__ import annotations

import ast
import re
import sys
from pathlib import Path

EXTENT_KEYS = {"cx", "cy", "cz", "pos_x", "pos_y", "pos_z", "width", "height", "depth", "min", "max"}
PRIMITIVE_HEADERS = re.compile(
    r"^\s*\[\[(sphere|plane|box|cylinder|cone|torus|terrain|water|light|campfire|sound|include)(\..+)?\]\]"
)


def fmt(v: float) -> str:
    if abs(v - round(v)) < 1e-9:
        return str(int(round(v)))
    return f"{v:.6g}"


def parse_vec_line(line: str) -> tuple[str, list[float], str] | None:
    m = re.match(r"^\s*(min|max)\s*=\s*\[(.*)\]\s*(#.*)?$", line.rstrip("\n"))
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


def parse_scalar(line: str) -> tuple[str, float, str] | None:
    m = re.match(r"^(\s*)(\w+)\s*=\s*([-\d.]+)\s*(#.*)?$", line.rstrip("\n"))
    if not m:
        return None
    key = m.group(2)
    if key not in EXTENT_KEYS:
        return None
    if "{{" in line:
        return None
    try:
        val = float(m.group(3))
    except ValueError:
        return None
    comment = m.group(4) or ""
    return key, val, comment


def extent_lines(px: float, py: float, pz: float, w: float, h: float, d: float, indent: str, comment: str) -> list[str]:
    lines = [
        f"{indent}pos_x = {fmt(px)}",
        f"{indent}pos_y = {fmt(py)}",
        f"{indent}pos_z = {fmt(pz)}",
        f"{indent}width = {fmt(w)}",
        f"{indent}height = {fmt(h)}",
        f"{indent}depth = {fmt(d)}",
    ]
    if comment:
        lines[-1] += " " + comment
    return [ln + "\n" for ln in lines]


def convert_min_max_block(lines: list[str], start: int) -> tuple[list[str], int, bool]:
    indent = re.match(r"^(\s*)", lines[start]).group(1)
    parsed = parse_vec_line(lines[start].rstrip("\n"))
    if parsed is None:
        return [], start, False

    first_key, first_vals, first_comment = parsed
    mn = mx = None
    min_idx = max_idx = start
    comment = first_comment
    if first_key == "min":
        mn, min_idx = first_vals, start
    else:
        mx, max_idx = first_vals, start

    j = start + 1
    while j < len(lines) and j <= start + 4:
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

    if mn is None or mx is None:
        return [], start, False

    w = abs(mx[0] - mn[0])
    h = abs(mx[1] - mn[1])
    d = abs(mx[2] - mn[2])
    px = min(mn[0], mx[0])
    py = min(mn[1], mx[1])
    pz = min(mn[2], mx[2])
    end = max(min_idx, max_idx) + 1
    return extent_lines(px, py, pz, w, h, d, indent, comment), end, True


def convert_center_block(lines: list[str], start: int) -> tuple[list[str], int, bool]:
    indent = re.match(r"^(\s*)", lines[start]).group(1)
    first = parse_scalar(lines[start])
    if first is None or first[0] != "cx":
        return [], start, False

    vals: dict[str, float] = {}
    comments: dict[str, str] = {}
    i = start
    while i < len(lines):
        parsed = parse_scalar(lines[i])
        if parsed is None:
            break
        key, val, comment = parsed
        if key not in EXTENT_KEYS:
            break
        vals[key] = val
        if comment:
            comments[key] = comment
        i += 1

    if not {"cx", "cy", "cz", "width", "height", "depth"} <= set(vals):
        return [], start, False
    if "pos_x" in vals or "pos_y" in vals or "pos_z" in vals:
        return [], start, False

    w, h, d = vals["width"], vals["height"], vals["depth"]
    px = vals["cx"] - w / 2
    py = vals["cy"] - h / 2
    pz = vals["cz"] - d / 2
    comment = comments.get("depth") or comments.get("cz", "")
    return extent_lines(px, py, pz, w, h, d, indent, comment), i, True


def convert_file(path: Path) -> bool:
    lines = path.read_text().splitlines(keepends=True)
    out: list[str] = []
    i = 0
    changed = False
    in_box = False
    while i < len(lines):
        hdr = PRIMITIVE_HEADERS.match(lines[i])
        if hdr:
            in_box = hdr.group(1) == "box"
            out.append(lines[i])
            i += 1
            continue

        if in_box:
            block, end, did = convert_min_max_block(lines, i)
            if not did:
                block, end, did = convert_center_block(lines, i)
            if did:
                out.extend(block)
                changed = True
                i = end
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
