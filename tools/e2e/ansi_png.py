#!/usr/bin/env python3
"""Render an ANSI screen capture (tmux capture-pane -e) to a PNG, and put a
render beside a mockup for comparison.

  ansi_png.py render CAPTURE.ans OUT.png [--cols N] [--title TEXT]
  ansi_png.py compare MOCKUP.png RENDER.png OUT.png [--title TEXT]

The cell is 9x17 px in DejaVu Sans Mono, the grid the TUI v3 mockups
(design mockups) are drawn on, so a render of the same
size lines up with its mockup column for column. Default colours are the
mockups' terminal background and foreground. Needs Pillow.
"""
import argparse
import os
import re
import sys

from PIL import Image, ImageDraw, ImageFont

CELL_W, CELL_H = 9, 17


def find_font(bold=False):
    candidates = [
        "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf" if bold else "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
        "/usr/share/fonts/TTF/DejaVuSansMono-Bold.ttf" if bold else "/usr/share/fonts/TTF/DejaVuSansMono.ttf",
        "/usr/share/fonts/dejavu/DejaVuSansMono-Bold.ttf" if bold else "/usr/share/fonts/dejavu/DejaVuSansMono.ttf",
        "/usr/share/fonts/truetype/freefont/FreeMonoBold.ttf" if bold else "/usr/share/fonts/truetype/freefont/FreeMono.ttf",
    ]
    for path in candidates:
        if os.path.isfile(path):
            return path
    return None


def get_font(size, bold=False):
    path = find_font(bold)
    if path:
        try:
            return ImageFont.truetype(path, size)
        except OSError:
            pass
    return ImageFont.load_default()


BG = (11, 15, 20)
FG = (201, 209, 217)
# xterm's 16 colours, as a dark terminal theme draws them.
BASE16 = [
    (0, 0, 0), (205, 49, 49), (13, 188, 121), (229, 229, 16), (36, 114, 200), (188, 63, 188), (17, 168, 205), (229, 229, 229),
    (102, 102, 102), (241, 76, 76), (35, 209, 139), (245, 245, 67), (59, 142, 234), (214, 112, 214), (41, 184, 219), (255, 255, 255),
]
SGR = re.compile(r"\x1b\[([0-9;:]*)m")
OTHER = re.compile(r"\x1b(\[[0-9;?]*[A-Za-ln-z]|\][^\x07\x1b]*(\x07|\x1b\\)|[()][0-9A-B])")


def xterm256(n):
    if n < 16:
        return BASE16[n]
    if n < 232:
        n -= 16
        steps = [0, 95, 135, 175, 215, 255]
        return (steps[n // 36], steps[(n // 6) % 6], steps[n % 6])
    v = 8 + (n - 232) * 10
    return (v, v, v)


class Style:
    def __init__(self):
        self.reset()

    def reset(self):
        self.fg = None
        self.bg = None
        self.bold = self.dim = self.italic = self.under = self.reverse = False

    def copy(self):
        s = Style()
        s.__dict__.update(self.__dict__)
        return s

    def apply(self, params):
        p = [int(x) if x.isdigit() else 0 for x in re.split(r"[;:]", params)] if params else [0]
        i = 0
        while i < len(p):
            c = p[i]
            if c == 0:
                self.reset()
            elif c == 1:
                self.bold = True
            elif c == 2:
                self.dim = True
            elif c == 3:
                self.italic = True
            elif c == 4:
                self.under = True
            elif c == 7:
                self.reverse = True
            elif c == 22:
                self.bold = self.dim = False
            elif c == 23:
                self.italic = False
            elif c == 24:
                self.under = False
            elif c == 27:
                self.reverse = False
            elif 30 <= c <= 37:
                self.fg = BASE16[c - 30]
            elif 90 <= c <= 97:
                self.fg = BASE16[c - 90 + 8]
            elif 40 <= c <= 47:
                self.bg = BASE16[c - 40]
            elif 100 <= c <= 107:
                self.bg = BASE16[c - 100 + 8]
            elif c == 39:
                self.fg = None
            elif c == 49:
                self.bg = None
            elif c in (38, 48) and i + 1 < len(p):
                col = None
                if p[i + 1] == 5 and i + 2 < len(p):
                    col = xterm256(p[i + 2])
                    i += 2
                elif p[i + 1] == 2 and i + 4 < len(p):
                    col = (p[i + 2], p[i + 3], p[i + 4])
                    i += 4
                if c == 38:
                    self.fg = col
                else:
                    self.bg = col
            i += 1


def wide(ch):
    o = ord(ch)
    return 0x1100 <= o <= 0x115F or 0x2E80 <= o <= 0xA4CF or 0xAC00 <= o <= 0xD7A3 or 0xF900 <= o <= 0xFAFF or 0xFE30 <= o <= 0xFE4F or 0xFF00 <= o <= 0xFF60 or 0x1F300 <= o <= 0x1FAFF


def parse(text):
    """Rows of (char, style) cells."""
    rows = []
    style = Style()
    for line in text.split("\n"):
        cells = []
        pos = 0
        while pos < len(line):
            m = SGR.match(line, pos)
            if m:
                style.apply(m.group(1))
                pos = m.end()
                continue
            m = OTHER.match(line, pos)
            if m:
                pos = m.end()
                continue
            ch = line[pos]
            pos += 1
            if ch in "\r\x07":
                continue
            cells.append((ch, style.copy()))
            if wide(ch):
                cells.append(("", style.copy()))
        rows.append(cells)
    while rows and not rows[-1]:
        rows.pop()
    return rows


def dimmed(c):
    return tuple(int(v * 0.6 + b * 0.4) for v, b in zip(c, BG))


def render(text, cols=None, title=None):
    rows = parse(text)
    cols = cols or max((len(r) for r in rows), default=1)
    top = CELL_H + 6 if title else 0
    img = Image.new("RGB", (cols * CELL_W + 16, top + len(rows) * CELL_H + 12), BG)
    d = ImageDraw.Draw(img)
    font = get_font(15, bold=False)
    bold = get_font(15, bold=True)
    if title:
        d.text((8, 4), title, font=get_font(13, bold=False), fill=(139, 148, 158))
    for y, row in enumerate(rows):
        for x, (ch, s) in enumerate(row[:cols]):
            fg, bg = s.fg or FG, s.bg
            if s.reverse:
                fg, bg = (bg or BG), fg
            if s.dim:
                fg = dimmed(fg)
            px, py = 8 + x * CELL_W, top + 6 + y * CELL_H
            if bg:
                d.rectangle([px, py, px + CELL_W - 1, py + CELL_H - 1], fill=bg)
            if ch and ch != " ":
                d.text((px, py + 1), ch, font=bold if s.bold else font, fill=fg)
            if s.under:
                d.line([px, py + CELL_H - 2, px + CELL_W - 1, py + CELL_H - 2], fill=fg)
    return img


def label(img, text):
    out = Image.new("RGB", (img.width, img.height + 22), (5, 8, 12))
    out.paste(img, (0, 22))
    ImageDraw.Draw(out).text((8, 4), text, font=get_font(14, bold=True), fill=(230, 237, 243))
    return out


def compare(mock, rendered, title=""):
    a = label(Image.open(mock).convert("RGB"), "MOCKUP  " + title)
    b = label(Image.open(rendered).convert("RGB"), "REAL TUI  " + title)
    w = max(a.width, b.width)
    out = Image.new("RGB", (w, a.height + b.height + 8), (40, 44, 52))
    out.paste(a, (0, 0))
    out.paste(b, (0, a.height + 8))
    return out


def main(argv=None):
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("render")
    r.add_argument("capture")
    r.add_argument("out")
    r.add_argument("--cols", type=int)
    r.add_argument("--title")
    c = sub.add_parser("compare")
    c.add_argument("mockup")
    c.add_argument("render")
    c.add_argument("out")
    c.add_argument("--title", default="")
    a = ap.parse_args(argv)
    if a.cmd == "render":
        with open(a.capture, encoding="utf-8", errors="replace") as fh:
            render(fh.read(), a.cols, a.title).save(a.out)
    else:
        compare(a.mockup, a.render, a.title).save(a.out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
