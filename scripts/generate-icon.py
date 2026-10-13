#!/usr/bin/env python3
"""Generate RSS-Tracker icons for the Home Assistant add-on.

Outputs:
  addon/icon.png  — 128x128 (add-on card)
  addon/logo.png  — 256x256 (add-on details page)
  icon.png/logo.png at repo root (add-on repository listing)

Re-run after any design change: python3 scripts/generate-icon.py
"""

from PIL import Image, ImageDraw

SIZE = 2048  # drawn at 4x, downscaled for clean antialiasing
RADIUS = 440
GRAD_TOP = (99, 112, 246)    # #6370F6 (Discord blurple gradient)
GRAD_BOTTOM = (79, 92, 236)  # #4F5CEC
WHITE = (255, 255, 255, 255)
GREEN = (35, 165, 90)        # #23A55A (Discord green — "online" badge)

DOT = (760, 1280)   # RSS glyph dot (bottom-left of the glyph)
ARC_R1 = 340
ARC_R2 = 680
STROKE = 128
BADGE = (1560, 480) # notification badge center (top-right)
BADGE_R = 176
BADGE_RING = 28


def rounded_gradient_bg() -> Image.Image:
    img = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    mask = Image.new("L", (SIZE, SIZE), 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        (0, 0, SIZE - 1, SIZE - 1), radius=RADIUS, fill=255
    )
    grad = Image.new("RGBA", (SIZE, SIZE))
    gd = ImageDraw.Draw(grad)
    for y in range(SIZE):
        t = y / (SIZE - 1)
        color = tuple(round(GRAD_TOP[i] + t * (GRAD_BOTTOM[i] - GRAD_TOP[i])) for i in range(3))
        gd.line([(0, y), (SIZE, y)], fill=color + (255,))
    img.paste(grad, (0, 0), mask)
    return img


def draw_glyph(img: Image.Image) -> None:
    d = ImageDraw.Draw(img)
    # Dot
    r = STROKE // 2 + 4
    d.ellipse((DOT[0] - r, DOT[1] - r, DOT[0] + r, DOT[1] + r), fill=WHITE)
    # Two quarter arcs opening to the top-right (PIL angles: 0° = 3 o'clock, CW)
    for radius in (ARC_R1, ARC_R2):
        d.arc(
            (DOT[0] - radius, DOT[1] - radius, DOT[0] + radius, DOT[1] + radius),
            start=180,
            end=270,
            fill=WHITE,
            width=STROKE,
        )
    # Notification badge: green core with a white ring
    ring = BADGE_R + BADGE_RING
    d.ellipse((BADGE[0] - ring, BADGE[1] - ring, BADGE[0] + ring, BADGE[1] + ring), fill=WHITE)
    d.ellipse((BADGE[0] - BADGE_R, BADGE[1] - BADGE_R, BADGE[0] + BADGE_R, BADGE[1] + BADGE_R), fill=GREEN + (255,))


def main() -> None:
    master = rounded_gradient_bg()
    draw_glyph(master)
    master = master.resize((1024, 1024), Image.LANCZOS)  # master antialiasing pass
    for path, size in (
        ("addon/icon.png", 128),
        ("addon/logo.png", 256),
        ("icon.png", 256),
        ("logo.png", 512),
    ):
        master.resize((size, size), Image.LANCZOS).save(path, optimize=True)
        print(f"wrote {path} ({size}x{size})")


if __name__ == "__main__":
    main()
