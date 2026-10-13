#!/usr/bin/env python3
"""Generate icon design proposals for RSS-Tracker (256px previews in icon-proposals/).

Pick one, then port its parameters into scripts/generate-icon.py to regenerate
the final set (addon/icon.png, addon/logo.png, root icon.png / logo.png).
"""

from PIL import Image, ImageDraw

SIZE = 2048
RADIUS = 440
DOT = (760, 1280)
ARC_R1 = 340
ARC_R2 = 680
STROKE = 128
BADGE = (1560, 480)
BADGE_R = 176
BADGE_RING = 28


def bg(top, bottom) -> Image.Image:
    img = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    mask = Image.new("L", (SIZE, SIZE), 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, SIZE - 1, SIZE - 1), radius=RADIUS, fill=255)
    grad = Image.new("RGBA", (SIZE, SIZE))
    gd = ImageDraw.Draw(grad)
    for y in range(SIZE):
        t = y / (SIZE - 1)
        color = tuple(round(top[i] + t * (bottom[i] - top[i])) for i in range(3))
        gd.line([(0, y), (SIZE, y)], fill=color + (255,))
    img.paste(grad, (0, 0), mask)
    return img


def glyph(d: ImageDraw.ImageDraw, arc_alphas, dot_color) -> None:
    r = STROKE // 2 + 4
    d.ellipse((DOT[0] - r, DOT[1] - r, DOT[0] + r, DOT[1] + r), fill=dot_color)
    for radius, alpha in zip((ARC_R1, ARC_R2), arc_alphas):
        color = dot_color[:3] + (round(255 * alpha),)
        d.arc(
            (DOT[0] - radius, DOT[1] - radius, DOT[0] + radius, DOT[1] + radius),
            start=180, end=270, fill=color, width=STROKE,
        )


def badge(d: ImageDraw.ImageDraw, core) -> None:
    ring = BADGE_R + BADGE_RING
    d.ellipse((BADGE[0] - ring, BADGE[1] - ring, BADGE[0] + ring, BADGE[1] + ring), fill=(255, 255, 255, 255))
    d.ellipse((BADGE[0] - BADGE_R, BADGE[1] - BADGE_R, BADGE[0] + BADGE_R, BADGE[1] + BADGE_R), fill=core + (255,))


def save(img: Image.Image, name: str) -> None:
    img.resize((256, 256), Image.LANCZOS).save(f"icon-proposals/{name}", optimize=True)
    print(name)


WHITE = (255, 255, 255, 255)
RED = (237, 66, 69)
GREEN = (35, 165, 90)
DISCORD_BLURPLE = (88, 101, 242)
DARK = (35, 38, 52)

# 1 — classic: blurple gradient, white RSS, red notification badge (current)
img = bg((108, 121, 240), (71, 82, 196))
d = ImageDraw.Draw(img)
glyph(d, (1.0, 1.0), WHITE)
badge(d, RED)
save(img, "1-classic-red-badge.png")

# 2 — dark dashboard: near-black slate, blurple→cyan RSS, green "online" badge
img = bg((30, 33, 43), (18, 20, 27))
d = ImageDraw.Draw(img)
glyph(d, (1.0, 1.0), DISCORD_BLURPLE + (255,))
badge(d, GREEN)
save(img, "2-dark-green-badge.png")

# 3 — light: off-white background, blurple RSS, red badge
img = bg((250, 250, 252), (232, 233, 240))
d = ImageDraw.Draw(img)
glyph(d, (1.0, 1.0), DISCORD_BLURPLE + (255,))
badge(d, RED)
save(img, "3-light-red-badge.png")

# 4 — signal fade: dark navy, white RSS with fading arcs, no badge
img = bg((42, 45, 86), (24, 26, 52))
d = ImageDraw.Draw(img)
glyph(d, (1.0, 0.55), WHITE)
save(img, "4-signal-fade.png")

# 5 — blurple flat + green badge: "bot en ligne"
img = bg((99, 112, 246), (79, 92, 236))
d = ImageDraw.Draw(img)
glyph(d, (1.0, 1.0), WHITE)
badge(d, GREEN)
save(img, "5-blurple-green-badge.png")
