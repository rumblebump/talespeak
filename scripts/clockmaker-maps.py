#!/usr/bin/env python3
"""Draws the torn harbour maps of the clockmaker pack (content/clockmaker).

The warehouse floor has four rows and six columns of crates. Rows and
columns carry labels that differ per variant, and an X marks one crate in
the last column. The map is torn into 1 to 4 pieces by columns: only the
first piece shows the row letters and only the last one the X, so the
players have to put their pieces together to name the crate.

    python3 scripts/clockmaker-maps.py content/clockmaker/assets

writes map-<variant>-<pieces>-<piece>.png. Python's standard library only.
"""
import os
import struct
import sys
import zlib

# variant: row letters top to bottom, column numbers left to right, X row.
# The answer is the X row's letter and the last column's number.
VARIANTS = {
    "a": ("RFKT", "371948", 2),  # K8
    "b": ("HMBW", "625813", 0),  # H3
    "c": ("PDLS", "958274", 3),  # S4
}

# Columns of each piece, by number of pieces.
PIECES = {
    1: [range(0, 6)],
    2: [range(0, 3), range(3, 6)],
    3: [range(0, 2), range(2, 4), range(4, 6)],
    4: [range(0, 2), range(2, 3), range(3, 4), range(4, 6)],
}

FONT = {  # 3x5 glyphs, rows top to bottom
    "1": ["010", "110", "010", "010", "111"],
    "2": ["110", "001", "010", "100", "111"],
    "3": ["110", "001", "010", "001", "110"],
    "4": ["101", "101", "111", "001", "001"],
    "5": ["111", "100", "110", "001", "110"],
    "6": ["011", "100", "110", "101", "010"],
    "7": ["111", "001", "010", "010", "010"],
    "8": ["010", "101", "010", "101", "010"],
    "9": ["010", "101", "011", "001", "110"],
    "B": ["110", "101", "110", "101", "110"],
    "D": ["110", "101", "101", "101", "110"],
    "F": ["111", "100", "110", "100", "100"],
    "H": ["101", "101", "111", "101", "101"],
    "K": ["101", "101", "110", "101", "101"],
    "L": ["100", "100", "100", "100", "111"],
    "M": ["101", "111", "111", "101", "101"],
    "P": ["110", "101", "110", "100", "100"],
    "R": ["110", "101", "110", "101", "101"],
    "S": ["011", "100", "010", "001", "110"],
    "T": ["111", "010", "010", "010", "010"],
    "W": ["101", "101", "111", "111", "101"],
}

PAPER = (231, 215, 172, 255)
INK = (74, 52, 32, 255)
CRATE = (150, 104, 58, 255)
PLANK = (112, 76, 40, 255)
RED = (190, 30, 30, 255)
CLEAR = (0, 0, 0, 0)

LEFT, TOP, CELL = 10, 10, 12
W, H = LEFT + 6 * CELL + 4, TOP + 4 * CELL + 4


def draw(variant):
    rows, cols, xrow = VARIANTS[variant]
    img = [[PAPER] * W for _ in range(H)]

    def put(x, y, c):
        if 0 <= x < W and 0 <= y < H:
            img[y][x] = c

    def text(ch, x, y):
        for dy, line in enumerate(FONT[ch]):
            for dx, bit in enumerate(line):
                if bit == "1":
                    put(x + dx, y + dy, INK)

    for x in range(W):  # a frame
        put(x, 0, INK)
        put(x, H - 1, INK)
    for y in range(H):
        put(0, y, INK)
        put(W - 1, y, INK)
    for r in range(4):
        text(rows[r], 4, TOP + r * CELL + 3)
    for c in range(6):
        text(cols[c], LEFT + c * CELL + 4, 3)
        for r in range(4):
            x0, y0 = LEFT + c * CELL + 1, TOP + r * CELL + 1
            for y in range(10):
                for x in range(10):
                    edge = x in (0, 9) or y in (0, 9)
                    put(x0 + x, y0 + y, PLANK if edge or x == y else CRATE)
    x0, y0 = LEFT + 5 * CELL + 1, TOP + xrow * CELL + 1
    for i in range(10):  # the X, two pixels wide
        for d in (0, 1):
            put(x0 + i, y0 + min(9, i + d), RED)
            put(x0 + 9 - i, y0 + min(9, i + d), RED)
    return img


def jag(y):
    return (y * 7 + 3) % 3  # 0..2: a torn edge


def piece(img, cols, first, last):
    x0 = 0 if first else LEFT + cols[0] * CELL - 1
    x1 = W if last else LEFT + (cols[-1] + 1) * CELL - 1
    out = []
    for y in range(H):
        row = list(img[y][x0:x1])
        if not first:
            for i in range(jag(y)):
                row[i] = CLEAR
        if not last:
            for i in range(2 - jag(y)):
                row[len(row) - 1 - i] = CLEAR
        out.append(row)
    return out


def png(rows):
    h, w = len(rows), len(rows[0])
    raw = b"".join(b"\0" + b"".join(struct.pack("4B", *p) for p in r) for r in rows)

    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))

    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 6, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b""))


def main(out):
    for v in VARIANTS:
        img = draw(v)
        for n, parts in PIECES.items():
            for k, cols in enumerate(parts, 1):
                p = piece(img, list(cols), k == 1, k == n)
                with open(os.path.join(out, "map-%s-%d-%d.png" % (v, n, k)), "wb") as f:
                    f.write(png(p))


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "content/clockmaker/assets")
