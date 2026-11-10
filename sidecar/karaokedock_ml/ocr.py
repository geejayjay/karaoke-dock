"""Reads the lyrics shown in a video and uses them to correct lyric timing.

A line on screen shows when it can be sung, but a karaoke video often shows it
early, so appearing on screen is not the same as being sung. Published timings
get the rhythm right but can be shifted against this video. So the text on
screen anchors each line to a time window, and the published timing decides
where in that window the line goes.

Karaoke videos go further: a colour wipe moves across each line exactly as it's
sung. Where one can be followed, it gives the true start, end and word timing.
"""

from __future__ import annotations

import colorsys
import json
import os
import statistics
import subprocess
import unicodedata
from dataclasses import dataclass
from difflib import SequenceMatcher
from typing import Any, Callable, Iterator, Sequence

import numpy as np

from . import runtime

WIDTH, HEIGHT = 854, 480
FULL_FPS = 2
PROBE_FPS = 0.1
MIN_CONFIDENCE = 0.6
MATCH = 0.7
TRACK_MATCH = 0.8
TRACK_GAP_MS = 1500
MIN_TRACK_MS = 400
LOGO_MS = 30000  # text that stays this long is a logo, not a lyric
WINDOW_MARGIN_MS = 250
MAX_LOCAL_FIX_MS = 8000
SAME_PAGE_MS = 700
SAME_FRAME_DIFF = 1.2
SAME_ROW = 14
WIPE_FPS = 10
WIPE_COLUMN_STEP = 4
WIPE_TEXT_DIFF = 30  # how far a pixel must be from the background to be text
WIPE_MIN_CHANGE = 60.0
WIPE_STEP = 2  # frames over which a column must change colour
WIPE_SKIP_FRAMES = 2  # frames to ignore while a row fades in
WIPE_TAIL_MS = 450  # a row can stay up this long after it was last read
WIPE_MIN_ORDER = 0.8  # how strictly the wipe must run left to right
WIPE_MIN_COVER = 0.4  # how much of a row must be seen changing colour
WIPE_RETRY_TAIL_MS = 2500  # the text can get too hard to read before the wipe ends
WIPE_RETRY_ORDER = 0.7
WIPE_RETRY_COVER = 0.6  # a looser order has to be backed by seeing more of the row
WIPE_MIN_MS = 150
MIN_WORDS_SHOWN = 0.4  # how much of the lyrics must be on screen to copy its layout
TIME_SLACK_MS = 12000  # how far from its expected time a word can be matched
MIN_OVERLAP = 0.5  # how much of the shorter wipe must run alongside another
SMALL_TEXT = 0.7  # text this much smaller than usual is backing vocals
HUE_GROUP = 30  # hues closer than this are the same colour
MIN_SINGER_SHARE = 0.15  # a colour needs this share of rows to count as a singer


@dataclass
class Box:
    text: str
    x: float
    y: float
    right: float = 0.0
    height: float = 0.0


@dataclass
class Track:
    """One piece of text, for as long as it stays on screen."""

    text: str
    y: float
    first: int
    last: int
    key: str = ""
    left: float = 0.0
    right: float = 0.0
    height: float = 0.0

    def __post_init__(self) -> None:
        self.key = self.key or normalize(self.text)


@dataclass
class Wipe:
    """When the colour wipe reached each point across a line."""

    fractions: list[float]
    times: list[float]
    before: tuple[float, float, float] | None = None  # text colour before and after, as BGR
    after: tuple[float, float, float] | None = None

    def at(self, fraction: float) -> int:
        return int(np.interp(fraction, self.fractions, self.times))

    @property
    def start(self) -> int:
        return self.at(0.0)

    @property
    def end(self) -> int:
        return self.at(1.0)


def normalize(s: str) -> str:
    return "".join(c for c in unicodedata.normalize("NFKC", s).casefold() if c.isalnum())


def similar(a: str, b: str, floor: float = 0.0) -> float:
    if not a or not b:
        return 0.0
    m = SequenceMatcher(None, a, b, autojunk=False)
    if m.real_quick_ratio() < floor or m.quick_ratio() < floor:
        return 0.0
    return m.ratio()


def line_score(lyric: str, shown: str) -> float:
    """How well on-screen text matches a lyric, allowing a line wrapped in two."""
    whole = similar(lyric, shown, MATCH)
    if whole >= MATCH or len(shown) < 8 or len(shown) * 2 < len(lyric) or len(shown) > len(lyric):
        return whole
    m = SequenceMatcher(None, shown, lyric, autojunk=False)
    inside = sum(b.size for b in m.get_matching_blocks()) / len(shown)
    return MATCH if inside >= 0.85 else whole


def merge_rows(boxes: Sequence[Box]) -> list[Box]:
    """Joins text pieces that sit on one row, top to bottom."""
    rows: list[list[Box]] = []
    for b in sorted(boxes, key=lambda b: b.y):
        if rows and abs(rows[-1][0].y - b.y) < SAME_ROW:
            rows[-1].append(b)
        else:
            rows.append([b])
    out = []
    for row in rows:
        row.sort(key=lambda b: b.x)
        out.append(Box(" ".join(b.text for b in row), row[0].x, row[0].y, max(b.right for b in row), max(b.height for b in row)))
    return out


def build_tracks(samples: Sequence[tuple[int, Sequence[Box]]]) -> list[Track]:
    """Follows each piece of text across frames."""
    active: list[Track] = []
    done: list[Track] = []
    for ms, boxes in samples:
        for t in [t for t in active if ms - t.last > TRACK_GAP_MS]:
            active.remove(t)
            done.append(t)
        for box in merge_rows(boxes):
            key = normalize(box.text)
            if len(key) < 3:
                continue
            # The row matters: a chorus can show nearly the same line twice at once.
            same = next((t for t in active if abs(t.y - box.y) < SAME_ROW and similar(t.key, key, TRACK_MATCH) >= TRACK_MATCH), None)
            if same:
                same.last = ms
                same.left, same.right = min(same.left, box.x), max(same.right, box.right)
                same.height = max(same.height, box.height)
                if len(key) > len(same.key):  # a wipe can hide letters in some frames
                    same.text, same.key = box.text, key
            else:
                active.append(Track(box.text, box.y, ms, ms, key, box.x, box.right, box.height))
    done += active
    kept = [t for t in done if MIN_TRACK_MS <= t.last - t.first <= LOGO_MS]
    return sorted(kept, key=lambda t: (t.first, t.y))


def match_lines(lyrics: Sequence[str], tracks: Sequence[Track]) -> list[tuple[int, Track]]:
    """Pairs lyric lines with on-screen text, keeping both in order."""
    keys = [normalize(s) for s in lyrics]
    n, m = len(keys), len(tracks)
    scores = [[line_score(k, t.key) if len(k) >= 3 else 0.0 for t in tracks] for k in keys]
    best = [[0.0] * (m + 1) for _ in range(n + 1)]
    for i in range(1, n + 1):
        for j in range(1, m + 1):
            s = scores[i - 1][j - 1]
            best[i][j] = max(best[i - 1][j], best[i][j - 1], best[i - 1][j - 1] + s if s >= MATCH else 0.0)
    pairs: list[tuple[int, Track]] = []
    i, j = n, m
    while i and j:
        s = scores[i - 1][j - 1]
        if s >= MATCH and best[i][j] == best[i - 1][j - 1] + s:
            pairs.append((i - 1, tracks[j - 1]))
            i -= 1
            j -= 1
        elif best[i][j] == best[i - 1][j]:
            i -= 1
        else:
            j -= 1
    pairs.reverse()
    return pairs


def solve_offset(pairs: Sequence[tuple[int, int, int]]) -> int:
    """Finds the shift that puts the most lines inside their on-screen windows."""
    low = min(first - start for start, first, _ in pairs) - WINDOW_MARGIN_MS
    high = max(last - start for start, _, last in pairs) + WINDOW_MARGIN_MS
    top = -1
    winners: list[int] = []
    for d in range(low, high + 1, 100):
        hits = sum(1 for start, first, last in pairs if first - WINDOW_MARGIN_MS <= start + d <= last + WINDOW_MARGIN_MS)
        if hits > top:
            top, winners = hits, [d]
        elif hits == top:
            winners.append(d)
    return int(statistics.median_low(winners))


def median_color(samples: Sequence[Any]) -> tuple[float, float, float] | None:
    values = np.array(samples, dtype=np.float32)
    if values.size == 0 or np.all(np.isnan(values)):
        return None
    b, g, r = np.nanmedian(values, axis=0)
    return float(b), float(g), float(r)


def find_wipe(
    colors: Any, stamps: Sequence[int], min_order: float = WIPE_MIN_ORDER, min_cover: float = WIPE_MIN_COVER
) -> Wipe | None:
    """Finds when each column of a line changed colour, if it swept left to right.

    `colors` holds the text colour of each column in each frame. A wipe is the
    first big change once the row has appeared. Later ones are the page changing,
    which can be just as big, and early ones are the row fading in.
    """
    frames, columns = colors.shape[:2]
    if frames <= WIPE_STEP + WIPE_SKIP_FRAMES + 2 or columns < 8:
        return None
    fractions: list[float] = []
    times: list[float] = []
    unsung: list[Any] = []
    sung: list[Any] = []
    for c in range(columns):
        delta = np.linalg.norm(colors[WIPE_STEP:, c] - colors[:-WIPE_STEP, c], axis=1)[WIPE_SKIP_FRAMES:]
        if np.all(np.isnan(delta)):
            continue
        floor = max(WIPE_MIN_CHANGE, float(np.nanmax(delta)) * 0.5)
        hits = np.flatnonzero(np.nan_to_num(delta) >= floor)
        if hits.size:
            k = int(hits[0]) + WIPE_SKIP_FRAMES
            fractions.append(c / (columns - 1))
            times.append((stamps[k] + stamps[k + WIPE_STEP]) / 2)
            unsung.append(colors[k, c])
            sung.append(colors[min(k + WIPE_STEP + 1, frames - 1), c])
    if len(times) < max(8, columns * min_cover) or np.std(times) == 0:
        return None
    if float(np.corrcoef(fractions, times)[0, 1]) < min_order:
        return None
    smooth = [float(np.median(times[max(i - 2, 0) : i + 3])) for i in range(len(times))]
    rising = np.maximum.accumulate(smooth).tolist()
    if rising[-1] - rising[0] < WIPE_MIN_MS:
        return None
    return Wipe(fractions, rising, median_color(unsung), median_color(sung))


def column_colors(frame: Any, t: Track) -> Any:
    """The text colour of each column of a line, or NaN where there's no text."""
    top, bottom = max(int(t.y - t.height * 0.6), 0), min(int(t.y + t.height * 0.6) + 1, HEIGHT)
    left, right = max(int(t.left), 0), min(int(t.right) + 1, WIDTH)
    band = frame[top:bottom, left:right][:, :: WIPE_COLUMN_STEP].astype(np.float32)
    background = np.median(band.reshape(-1, 3), axis=0)
    is_text = np.abs(band - background).sum(axis=2) > WIPE_TEXT_DIFF
    count = is_text.sum(axis=0)
    out = np.full((band.shape[1], 3), np.nan, dtype=np.float32)
    has = count > 0
    out[has] = (band * is_text[..., None]).sum(axis=0)[has] / count[has, None]
    return out


def wipe_with_retry(colors: Any, stamps: Sequence[int], normal_end: int) -> Wipe | None:
    """Reads a row's wipe from its normal window, then looks further if that failed."""
    seen = sum(1 for ms in stamps if ms <= normal_end)
    wipe = find_wipe(colors[:seen], stamps[:seen]) if seen else None
    return wipe or find_wipe(colors, stamps, WIPE_RETRY_ORDER, WIPE_RETRY_COVER)


def retry_end(t: Track, tracks: Sequence[Track]) -> int:
    """How long to keep watching a row: not past different text taking its place."""
    later = [
        o.first
        for o in tracks
        if abs(o.y - t.y) < SAME_ROW and o.first > t.last and similar(o.key, t.key, TRACK_MATCH) < TRACK_MATCH
    ]
    return min([t.last + WIPE_RETRY_TAIL_MS, *later])


def read_wipes(path: str, tracks: Sequence[Track]) -> dict[int, Wipe]:
    """Follows the colour wipe of each line. Returns those it could follow, by index."""
    colors: list[list[Any]] = [[] for _ in tracks]
    stamps: list[list[int]] = [[] for _ in tracks]
    ends = [retry_end(t, tracks) for t in tracks]
    for ms, frame in read_frames(path, WIPE_FPS):
        for n, t in enumerate(tracks):
            # Not before the row is seen, when its place still holds the old page.
            if t.right > t.left and t.height > 0 and t.first <= ms <= ends[n]:
                colors[n].append(column_colors(frame, t))
                stamps[n].append(ms)
    found: dict[int, Wipe] = {}
    for n, t in enumerate(tracks):
        if colors[n]:
            wipe = wipe_with_retry(np.stack(colors[n]), stamps[n], t.last + WIPE_TAIL_MS)
            if wipe:
                found[n] = wipe
    return found


def word_times(words: Sequence[str], wipe: Wipe, low: float = 0.0, high: float = 1.0) -> list[list[int]]:
    """Times each word by where its letters sit between two points of the line."""
    sizes = [max(len(normalize(w)), 1) for w in words]
    total = sum(sizes)
    at = lambda done: wipe.at(low + (high - low) * done / total)  # noqa: E731
    out: list[list[int]] = []
    done = 0
    for size in sizes:
        start = at(done)
        done += size
        out.append([start, max(at(done), start + 1)])
    return out


def is_cjk(c: str) -> bool:
    return "぀" <= c <= "ヿ" or "㐀" <= c <= "鿿" or "가" <= c <= "힯"


def tokenize(text: str) -> list[str]:
    """Splits text into comparable words, with each CJK character as its own."""
    out: list[str] = []
    for part in text.split():
        key = normalize(part)
        if key and any(is_cjk(c) for c in key):
            out.extend(key)
        elif key:
            out.append(key)
    return out


def same_word(a: str, b: str) -> bool:
    """Equal, or close enough to be the same word read imperfectly."""
    if a == b:
        return True
    if min(len(a), len(b)) < 4 or abs(len(a) - len(b)) > 2:
        return False
    return similar(a, b, 0.75) >= 0.75


def near_in_time(expected: int, t: Track) -> bool:
    return t.first - TIME_SLACK_MS <= expected <= t.last + TIME_SLACK_MS


def align_words(
    keys: Sequence[str], tracks: Sequence[Track], expected: Sequence[int] | None = None
) -> list[tuple[int, int] | None]:
    """Finds where each lyric word appears on screen, keeping both in order.

    Returns, for each word, which piece of on-screen text it's in and its
    position there, or None for a word the video doesn't show. With `expected`
    times, a word can only match text shown around then, so a repeated chorus
    can't be matched to the wrong repeat.
    """
    shown = [(n, pos, word) for n, t in enumerate(tracks) for pos, word in enumerate(tokenize(t.text))]
    rows, columns = len(keys), len(shown)
    previous = [0] * (columns + 1)
    moves = [bytearray(columns + 1) for _ in range(rows + 1)]
    for i in range(1, rows + 1):
        current = [0] * (columns + 1)
        key = keys[i - 1]
        row = moves[i]
        for j in range(1, columns + 1):
            best, move = previous[j], 1
            if current[j - 1] > best:
                best, move = current[j - 1], 2
            if key and previous[j - 1] + 1 >= best and same_word(key, shown[j - 1][2]):
                if expected is None or near_in_time(expected[i - 1], tracks[shown[j - 1][0]]):
                    best, move = previous[j - 1] + 1, 3
            current[j] = best
            row[j] = move
        previous = current
    out: list[tuple[int, int] | None] = [None] * rows
    i, j = rows, columns
    while i and j:
        move = moves[i][j]
        if move == 3:
            out[i - 1] = shown[j - 1][:2]
            i -= 1
            j -= 1
        elif move == 1:
            i -= 1
        else:
            j -= 1
    return out


def fill_gaps(matches: Sequence[tuple[int, int] | None], line_of: Sequence[int]) -> list[int]:
    """Gives every word a piece of on-screen text, including the unmatched ones.

    An unmatched word stays with the word before it while they share a lyric
    line, and otherwise goes with the word after it.
    """
    n = len(matches)
    out = [m[0] if m else -1 for m in matches]
    for i in range(1, n):
        if out[i] == -1 and out[i - 1] != -1 and line_of[i] == line_of[i - 1]:
            out[i] = out[i - 1]
    for i in range(n - 2, -1, -1):
        if out[i] == -1:
            out[i] = out[i + 1]
    for i in range(1, n):
        if out[i] == -1:
            out[i] = out[i - 1]
    return out


def backing_groups(words: Sequence[str]) -> list[tuple[int, int]]:
    """Finds each run of words inside brackets, which are backing vocals."""
    groups: list[tuple[int, int]] = []
    depth = 0
    start = 0
    for i, word in enumerate(words):
        opens = word.count("(") + word.count("（")
        closes = word.count(")") + word.count("）")
        if depth == 0 and opens:
            start = i
        depth = max(depth + opens - closes, 0)
        if depth == 0 and (opens or closes):
            groups.append((start, i + 1))
    if depth:
        groups.append((start, len(words)))
    return groups


def keep_backing_together(
    words: Sequence[str], matches: Sequence[tuple[int, int] | None], assigned: Sequence[int], line_of: Sequence[int]
) -> list[int]:
    """Stops backing vocals changing the video's layout.

    A bracketed run shown on one row stays together. One the video spreads over
    several rows follows the video, because there it's really a main line.
    Videos often leave these out, so a whole bracketed line the video doesn't
    show keeps a small row of its own.
    """
    out = list(assigned)
    own_row = -2
    for start, end in backing_groups(words):
        shown = [m[0] for m in matches[start:end] if m]
        starts_line = start == 0 or line_of[start - 1] != line_of[start]
        ends_line = end == len(words) or line_of[end] != line_of[end - 1]
        if len(set(shown)) > 1:
            continue
        if shown:
            target = shown[0]
        elif starts_line and ends_line:
            target = own_row
            own_row -= 1
        else:
            target = out[start]
        out[start:end] = [target] * (end - start)
    return out


def overlaps(a: tuple[int, int], b: tuple[int, int]) -> bool:
    """True when two wipes run together for at least half of the shorter one."""
    shorter = min(a[1] - a[0], b[1] - b[0])
    return shorter > 0 and min(a[1], b[1]) - max(a[0], b[0]) >= shorter * MIN_OVERLAP


def mark_backing(rows: list[dict[str, Any]], words: Sequence[str], tracks: Sequence[Track], found: dict[int, Wipe]) -> None:
    """Decides which rows are backing vocals, trusting what the video shows.

    Smaller text is backing. A bracketed row is too if the video leaves it out
    or wipes it alongside another row. Shown at normal size on its own, it's a
    main line, even when its wipe couldn't be followed. Two equal rows wiping
    together without brackets are a duet, so overlap alone doesn't count.
    """
    heights = [tracks[r["_track"]].height for r in rows if r["_track"] >= 0]
    typical = statistics.median(heights) if heights else 0.0
    wipes = [found.get(id(tracks[r["_track"]])) if r["_track"] >= 0 else None for r in rows]
    spans = [(w.start, w.end) if w else None for w in wipes]
    at = 0
    for n, row in enumerate(rows):
        run = words[at : at + row["words"]]
        at += row["words"]
        bracketed = backing_groups(run) == [(0, len(run))]
        small = row["_track"] >= 0 and typical > 0 and tracks[row["_track"]].height < typical * SMALL_TEXT
        span = spans[n]
        together = span is not None and any(m != n and other and overlaps(span, other) for m, other in enumerate(spans))
        if small or (bracketed and (row["_track"] < 0 or together)):
            row["role"] = "backing"
        elif bracketed:
            row["plain"] = True


def hue_of(color: tuple[float, float, float] | None) -> float | None:
    """The colour's hue in degrees, or None for white, grey or very dark text."""
    if color is None:
        return None
    b, g, r = color
    h, s, v = colorsys.rgb_to_hsv(r / 255, g / 255, b / 255)
    return h * 360 if s >= 0.3 and v >= 0.3 else None


def hue_gap(a: float, b: float) -> float:
    d = abs(a - b) % 360
    return min(d, 360 - d)


def group_hues(hues: Sequence[float]) -> list[tuple[float, int]]:
    """Groups similar hues, returning each group's hue and size, largest first."""
    groups: list[list[float]] = []
    for hue in hues:
        near = next((g for g in groups if hue_gap(g[0], hue) < HUE_GROUP), None)
        if near is None:
            groups.append([hue])
        else:
            near.append(hue)
    return sorted(((g[0], len(g)) for g in groups), key=lambda x: -x[1])


def name_singers(groups: Sequence[tuple[float, int]]) -> list[tuple[float, str]]:
    """Karaoke videos usually use blue for him, pink for her, and a third for both."""
    kinds = ["boy" if 170 <= hue <= 265 else "girl" if hue >= 285 or hue < 25 else "" for hue, _ in groups]
    if "boy" in kinds and "girl" in kinds:
        return [(g[0], kind or "both") for g, kind in zip(groups, kinds)]
    return [(g[0], f"part{i + 1}") for i, g in enumerate(groups[:3])]


def mark_singers(rows: list[dict[str, Any]], tracks: Sequence[Track], found: dict[int, Wipe]) -> None:
    """Gives rows a singer when the video colours rows by singer."""
    wipes = [found.get(id(tracks[r["_track"]])) if r["_track"] >= 0 else None for r in rows]
    for side in ("after", "before"):
        hues = [hue_of(getattr(w, side)) if w else None for w in wipes]
        known = [h for h in hues if h is not None]
        needed = max(3, int(len(known) * MIN_SINGER_SHARE))
        groups = [g for g in group_hues(known) if g[1] >= needed]
        if len(groups) < 2:
            continue
        singers = name_singers(groups)
        last = ""
        for row, hue in zip(rows, hues):
            if hue is not None:
                gap, name = min(((hue_gap(hue, center), who) for center, who in singers), key=lambda x: x[0])
                if gap < HUE_GROUP * 2:
                    last = name
            if last and row.get("role") != "backing":
                row["voice"] = last
        first = next((r["voice"] for r in rows if r.get("voice")), "")
        for row in rows:
            if row.get("voice"):
                break
            if row.get("role") != "backing":
                row["voice"] = first
        return


def build_rows(
    words: Sequence[str],
    matches: Sequence[tuple[int, int] | None],
    assigned: Sequence[int],
    tracks: Sequence[Track],
    found: dict[int, Wipe],
) -> list[dict[str, Any]]:
    """Groups words into the rows the video shows, timing any row with a wipe."""
    rows: list[dict[str, Any]] = []
    start = 0
    for i in range(1, len(words) + 1):
        if i < len(words) and assigned[i] == assigned[start]:
            continue
        row: dict[str, Any] = {"words": i - start, "_track": assigned[start]}
        wipe = found.get(id(tracks[assigned[start]])) if assigned[start] >= 0 else None
        if wipe:
            sizes = [len(w) for w in tokenize(tracks[assigned[start]].text)]
            positions = [m[1] for m in matches[start:i] if m]
            total = sum(sizes) or 1
            low = sum(sizes[: positions[0]]) / total if positions else 0.0
            high = sum(sizes[: positions[-1] + 1]) / total if positions else 1.0
            row["wordTimes"] = word_times(words[start:i], wipe, low, high)
        rows.append(row)
        start = i
    return rows


def retime(
    lines: Sequence[dict[str, Any]], pairs: Sequence[tuple[int, Track]], wipes: dict[int, Wipe] | None = None
) -> tuple[list[dict[str, Any]], int]:
    """Shifts all lines together, then nudges any left outside their window.

    A line with a wipe has an exact time, so its window shrinks to that moment.
    """
    wipes = wipes or {}

    def window(i: int, t: Track) -> tuple[int, int]:
        return (wipes[i].start, wipes[i].start) if i in wipes else (t.first, t.last)

    offset = solve_offset([(lines[i]["startMs"], *window(i, t)) for i, t in pairs])
    anchors: list[tuple[int, int]] = []
    for i, t in pairs:
        at = lines[i]["startMs"] + offset
        first, last = window(i, t)
        fix = first - at if at < first else last - at if at > last else 0
        if abs(fix) <= MAX_LOCAL_FIX_MS:
            anchors.append((i, fix))

    def local_fix(i: int) -> float:
        if not anchors:
            return 0.0
        if i <= anchors[0][0]:
            return anchors[0][1]
        if i >= anchors[-1][0]:
            return anchors[-1][1]
        for (a, fa), (b, fb) in zip(anchors, anchors[1:]):
            if a <= i <= b:
                return fa + (fb - fa) * (i - a) / (b - a)
        return 0.0

    out: list[dict[str, Any]] = []
    previous = -50
    for i, line in enumerate(lines):
        start = max(int(line["startMs"] + offset + local_fix(i)), previous + 50, 0)
        out.append({"text": line["text"], "startMs": start, "endMs": start + max(line["endMs"] - line["startMs"], 200)})
        previous = start
    for i, wipe in wipes.items():
        out[i]["startMs"], out[i]["endMs"] = wipe.start, wipe.end
        if lines[i].get("words"):
            out[i]["wordTimes"] = word_times(lines[i]["words"], wipe)
    for a, b in zip(out, out[1:]):
        a["endMs"] = min(a["endMs"], b["startMs"])
    return out, offset


BRAND_WORDS = ("karaoke", "karafun", "singking", "stingray", "keytv")
INTRO_MS = 15000
LEGAL_PHRASES = ("legalinformation", "copyright", "allrightsreserved", "recordingyourown", "unauthori", "rightsholder", "publicperformance")
LEGAL_PAGE_MS = 2000  # text appearing this close to a legal notice is on its page


def is_junk(t: Track, ignore: Sequence[str]) -> bool:
    """Spots channel logos and title cards, which aren't lyrics."""
    if any(word in t.key for word in BRAND_WORDS):
        return True
    if len(t.text.split()) == 1 and t.first < 5000:
        return True
    names = [normalize(name) for name in ignore if name]
    return t.first < INTRO_MS and any(similar(t.key, name, 0.75) >= 0.75 for name in names)


def lyric_tracks(tracks: Sequence[Track], ignore: Sequence[str]) -> list[Track]:
    """Leaves out logos, title cards and whole pages of legal notices."""
    legal = [t for t in tracks if any(phrase in t.key for phrase in LEGAL_PHRASES)]
    return [t for t in tracks if not is_junk(t, ignore) and not any(abs(t.first - n.first) <= LEGAL_PAGE_MS for n in legal)]


def lines_from_tracks(tracks: Sequence[Track], found: dict[int, Wipe] | None = None) -> list[dict[str, Any]]:
    """Builds lyrics from the screen alone.

    A row with a wipe gets its exact times. The others share their page's time by length.
    """
    found = found or {}
    pages: list[list[Track]] = []
    for t in tracks:
        if pages and t.first - pages[-1][0].first <= SAME_PAGE_MS:
            pages[-1].append(t)
        else:
            pages.append([t])
    out: list[dict[str, Any]] = []
    for n, page in enumerate(pages):
        page.sort(key=lambda t: t.y)
        start = page[0].first
        end = pages[n + 1][0].first if n + 1 < len(pages) else max(t.last for t in page)
        letters = sum(len(t.key) for t in page)
        at = float(start)
        for t in page:
            span = (end - start) * len(t.key) / letters
            line: dict[str, Any] = {"text": t.text.strip(), "startMs": int(at), "endMs": int(at + span)}
            wipe = found.get(id(t))
            if wipe:
                line.update(startMs=wipe.start, endMs=wipe.end, wordTimes=word_times(t.text.split(), wipe))
            out.append(line)
            at += span
    out.sort(key=lambda x: x["startMs"])
    return out


def read_frames(path: str, fps: float) -> Iterator[tuple[int, Any]]:
    flags = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0
    size = WIDTH * HEIGHT * 3
    cmd = ["ffmpeg", "-hide_banner", "-loglevel", "error", "-i", path, "-an", "-vf",
           f"fps={fps},scale={WIDTH}:{HEIGHT}", "-f", "rawvideo", "-pix_fmt", "bgr24", "-"]
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, creationflags=flags)
    try:
        assert proc.stdout is not None
        i = 0
        while True:
            raw = proc.stdout.read(size)
            if len(raw) < size:
                return
            yield int(i * 1000 / fps), np.frombuffer(raw, dtype=np.uint8).reshape(HEIGHT, WIDTH, 3)
            i += 1
    finally:
        proc.kill()
        proc.wait()


def read_text(engine: Any, frame: Any) -> list[Box]:
    result, _ = engine(frame, use_cls=False)
    boxes = []
    for points, text, confidence in result or []:
        if float(confidence) >= MIN_CONFIDENCE:
            xs, ys = [float(p[0]) for p in points], [float(p[1]) for p in points]
            boxes.append(Box(text, min(xs), sum(ys) / len(ys), max(xs), max(ys) - min(ys)))
    return boxes


def shows_lyrics(engine: Any, path: str, keys: Sequence[str]) -> bool:
    """A quick look through the video, so ordinary music videos cost almost nothing."""
    frames = with_text = 0
    seen: set[int] = set()
    for _, frame in read_frames(path, PROBE_FPS):
        frames += 1
        rows = [normalize(b.text) for b in merge_rows(read_text(engine, frame))]
        with_text += any(len(r) >= 8 for r in rows)
        for i, k in enumerate(keys):
            if len(k) >= 3 and any(line_score(k, r) >= MATCH for r in rows):
                seen.add(i)
    if keys:
        return len(seen) >= 2
    return with_text >= 3 and with_text >= frames * 0.3


def run(req: dict[str, Any], progress: Callable[[float], None]) -> dict[str, Any]:
    from rapidocr_onnxruntime import RapidOCR

    with open(req["lyrics"], encoding="utf-8") as f:
        lines: list[dict[str, Any]] = json.load(f)["lines"]
    duration = max(int(req.get("durationMs") or 0), 1)
    progress(2)
    engine = runtime.get_model("ocr", RapidOCR)
    progress(5)
    result: dict[str, Any] = {"found": False}
    if shows_lyrics(engine, req["input"], [normalize(x["text"]) for x in lines]):
        progress(12)
        samples: list[tuple[int, Sequence[Box]]] = []
        before = None
        boxes: list[Box] = []
        for ms, frame in read_frames(req["input"], FULL_FPS):
            small = frame[::6, ::6].mean(axis=2)
            if before is None or float(np.abs(small - before).mean()) >= SAME_FRAME_DIFF:
                boxes = read_text(engine, frame)
                before = small
            samples.append((ms, boxes))
            progress(min(12 + 86 * ms / duration, 98))
        tracks = build_tracks(samples)
        if lines:
            pairs = match_lines([x["text"] for x in lines], tracks)
            if len(pairs) >= max(4, len(lines) // 4):
                # A line split over two rows would give half a wipe, so those are left out.
                whole = [(i, t) for i, t in pairs if len(t.key) >= 0.8 * len(normalize(lines[i]["text"]))]
                # The video's own rows become the layout, if enough words line up.
                real = lyric_tracks(tracks, req.get("ignore") or [])
                words = [w for line in lines for w in (line.get("words") or line["text"].split())]
                line_of = [i for i, line in enumerate(lines) for _ in (line.get("words") or line["text"].split())]
                # Most lines agree on one shift, which says roughly when each word is due.
                shift = solve_offset([(lines[i]["startMs"], t.first, t.last) for i, t in pairs])
                pairs = [(i, t) for i, t in pairs if near_in_time(lines[i]["startMs"] + shift, t)]
                whole = [(i, t) for i, t in whole if near_in_time(lines[i]["startMs"] + shift, t)]
                expected = [lines[i]["startMs"] + shift for i in line_of]
                matches = align_words([normalize(w) for w in words], real, expected)
                organized = sum(1 for m in matches if m) >= len(words) * MIN_WORDS_SHOWN
                assigned = keep_backing_together(words, matches, fill_gaps(matches, line_of), line_of) if organized else []
                in_rows = [real[n] for n in sorted(set(assigned)) if n >= 0]
                to_read = list({id(t): t for t in [*in_rows, *(t for _, t in whole)]}.values())
                by_index = read_wipes(req["input"], to_read)
                found = {id(to_read[n]): w for n, w in by_index.items()}
                wipes = {i: found[id(t)] for i, t in whole if id(t) in found}
                timed, offset = retime(lines, pairs, wipes)
                result = {"found": True, "matched": len(pairs), "wiped": len(wipes), "offsetMs": offset, "lines": timed}
                if organized:
                    rows = build_rows(words, matches, assigned, real, found)
                    mark_backing(rows, words, real, found)
                    mark_singers(rows, real, found)
                    for row in rows:
                        del row["_track"]
                    result["rows"] = rows
        elif req.get("buildLyrics"):
            real = lyric_tracks(tracks, req.get("ignore") or [])
            if len(real) >= 4:
                found = {id(real[n]): w for n, w in read_wipes(req["input"], real).items()}
                result = {"found": True, "matched": len(real), "wiped": len(found), "offsetMs": 0, "lines": lines_from_tracks(real, found)}
    with open(req["output"], "w", encoding="utf-8") as f:
        json.dump(result, f, ensure_ascii=False)
    progress(100)
    return {"files": {"timed": req["output"]}}
