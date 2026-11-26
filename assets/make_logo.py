"""Draws the KaraokeDock microphone logo as SVG.

Usage: python assets/make_logo.py assets
"""
import math, sys

PINK, VIOLET, INK, LIGHT = "#ff4d8d", "#8f6bff", "#0d0b14", "#ece9f5"
out_dir = sys.argv[1]

# Microphone head: a capsule.
HX, HY, HW, HH = 176, 48, 160, 244
R = HW / 2

def inside_head(x, y, pad):
    cx = HX + R
    if y < HY + R:
        return math.hypot(x - cx, y - (HY + R)) <= R - pad
    if y > HY + HH - R:
        return math.hypot(x - cx, y - (HY + HH - R)) <= R - pad
    return HX + pad <= x <= HX + HW - pad

def grille():
    """Dark dots that grow towards the bottom of the head, only on its lower part."""
    dots = []
    step = 17.0
    start = HY + HH * 0.38  # the top of the head stays solid
    row = 0
    y = start
    while y < HY + HH:
        offset = step / 2 if row % 2 else 0
        x = HX + offset
        while x <= HX + HW:
            t = (y - start) / (HY + HH - start)
            r = 1.2 + 6.4 * t ** 0.9
            if inside_head(x, y, r + 2):
                dots.append(f'<circle cx="{x:.1f}" cy="{y:.1f}" r="{r:.2f}"/>')
            x += step
        y += step * 0.866
        row += 1
    return "".join(dots)

# Two beamed notes, drawn only as dots that shrink as they rise.
NOTE_HEADS = [(372, 182), (456, 154)]
HEAD_RX, HEAD_RY, HEAD_TILT = 30, 22, math.radians(-22)
STEM_W, STEM_TOP = 16, [(52,), (24,)]
BEAM_H = 28

def in_note(x, y):
    for (cx, cy), (top,) in zip(NOTE_HEADS, STEM_TOP):
        dx, dy = x - cx, y - cy
        u = dx * math.cos(HEAD_TILT) + dy * math.sin(HEAD_TILT)
        v = -dx * math.sin(HEAD_TILT) + dy * math.cos(HEAD_TILT)
        if (u / HEAD_RX) ** 2 + (v / HEAD_RY) ** 2 <= 1:
            return True
        sx = cx + HEAD_RX - STEM_W + 3
        if sx <= x <= sx + STEM_W and top <= y <= cy:
            return True
    (x1, _), (x2, _) = NOTE_HEADS
    left, right = x1 + HEAD_RX - STEM_W + 3, x2 + HEAD_RX + 3
    if left <= x <= right:
        t = (x - left) / (right - left)
        top = STEM_TOP[0][0] + (STEM_TOP[1][0] - STEM_TOP[0][0]) * t
        return top <= y <= top + BEAM_H
    return False

NOTES_LEFT, NOTES_RIGHT = 320.0, 490.0
# Where the notes sit when the mic is mirrored: same shapes, moved to the other side.
MIRROR_SHIFT = (512 - (NOTES_LEFT + NOTES_RIGHT) / 2) - (NOTES_LEFT + NOTES_RIGHT) / 2

def notes(mirror=False):
    """The notes are biggest nearest the mic, so a mirrored mic flips the fade, not the notes."""
    near_x = NOTES_LEFT + NOTES_RIGHT - 340 if mirror else 340
    dots = []
    step = 8.5
    row = 0
    y = 10.0
    while y < 230:
        x = 320.0 + (step / 2 if row % 2 else 0)
        while x < 490:
            if in_note(x, y):
                fade = min(max((math.hypot(x - near_x, y - 210) - 30) / 230, 0), 1)
                r = 4.4 - 2.2 * fade
                dots.append(f'<circle cx="{x:.1f}" cy="{y:.1f}" r="{r:.2f}"/>')
            x += step
        y += step * 0.866
        row += 1
    shift = f' transform="translate({MIRROR_SHIFT:.1f} 0)"' if mirror else ""
    return f'<g fill="url(#kd-n)"{shift}>{"".join(dots)}</g>'

def mic(mirror=False):
    """The microphone and notes. Mirrored, the mic leans right instead of left."""
    near, far = (NOTES_LEFT + NOTES_RIGHT - 340, NOTES_LEFT) if mirror else (340, NOTES_RIGHT)
    flip = '<g transform="translate(512 0) scale(-1 1)">' if mirror else "<g>"
    return f"""
  <defs>
    <linearGradient id="kd-g" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="{PINK}"/>
      <stop offset="1" stop-color="{VIOLET}"/>
    </linearGradient>
    <linearGradient id="kd-shade" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0" stop-color="#fff" stop-opacity=".22"/>
      <stop offset=".35" stop-color="#fff" stop-opacity="0"/>
      <stop offset=".7" stop-color="{INK}" stop-opacity="0"/>
      <stop offset="1" stop-color="{INK}" stop-opacity=".38"/>
    </linearGradient>
    <linearGradient id="kd-n" gradientUnits="userSpaceOnUse" x1="{near}" y1="210" x2="{far}" y2="20">
      <stop offset="0" stop-color="{PINK}"/>
      <stop offset="1" stop-color="{VIOLET}"/>
    </linearGradient>
  </defs>
  {notes(mirror)}
  {flip}<g transform="translate(-50 30) rotate(-16 256 256) translate(256 256) scale(.9) translate(-256 -256)">
  <path d="M132 216v22a124 124 0 0 0 248 0v-22" fill="none" stroke="#5a3db8" stroke-width="26" stroke-linecap="round"/>
  <path d="M132 210v22a124 124 0 0 0 248 0v-22" fill="none" stroke="url(#kd-g)" stroke-width="26" stroke-linecap="round"/>
  <path d="M256 356v66" fill="none" stroke="#6e4fd6" stroke-width="26" stroke-linecap="round"/>
  <path d="M249 360v58" fill="none" stroke="#fff" stroke-opacity=".18" stroke-width="7" stroke-linecap="round"/>
  <ellipse cx="256" cy="444" rx="68" ry="15" fill="#4a3399"/>
  <ellipse cx="256" cy="436" rx="68" ry="15" fill="{VIOLET}"/>
  <ellipse cx="248" cy="433" rx="40" ry="6" fill="#fff" fill-opacity=".16"/>
  <rect x="{HX}" y="{HY}" width="{HW}" height="{HH}" rx="{R}" fill="url(#kd-g)"/>
  <rect x="{HX}" y="{HY}" width="{HW}" height="{HH}" rx="{R}" fill="url(#kd-shade)"/>
  <ellipse cx="{HX + R}" cy="{HY + 34}" rx="{R - 22}" ry="18" fill="#fff" fill-opacity=".13"/>
  <path d="M{HX + 30} {HY + 78}a{R - 30} {R - 30} 0 0 1 {R - 44} {-(R - 34)}" fill="none" stroke="#fff" stroke-opacity=".38" stroke-width="11" stroke-linecap="round"/>
  <g fill="{INK}">{grille()}</g>
  </g></g>"""

mark = f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512">{mic()}\n</svg>\n'
icon = f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512">
  <rect width="512" height="512" rx="112" fill="{INK}"/>
  <g transform="translate(51.2 46) scale(.8)">{mic()}
  </g>
</svg>
"""
open(f"{out_dir}/logo.svg", "w").write(mark)
open(f"{out_dir}/logo-icon.svg", "w").write(icon)
# Cropped to the drawing (measured, plus room for the strokes), to sit beside text.
MARK_BOX = "33 8 470 498"
cropped = mark.replace('viewBox="0 0 512 512"', f'viewBox="{MARK_BOX}"', 1)
open(f"{out_dir}/logo-mark.svg", "w").write(cropped)
# Leaning right, for placing after text.
MARK_RIGHT_BOX = "30 8 449 499"  # measured like MARK_BOX
right = f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{MARK_RIGHT_BOX}">{mic(mirror=True)}\n</svg>\n'
open(f"{out_dir}/logo-mark-right.svg", "w").write(right)
# Square corners, for platforms that round the icon themselves.
open(f"{out_dir}/logo-square.svg", "w").write(icon.replace('rx="112"', 'rx="0"', 1))
print("written")
