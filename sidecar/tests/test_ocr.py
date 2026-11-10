import numpy as np

from karaokedock_ml.ocr import Box, Track, Wipe, align_words, build_rows, build_tracks, fill_gaps, find_wipe, is_junk, keep_backing_together, mark_backing, mark_singers, line_score, lines_from_tracks, lyric_tracks, match_lines, merge_rows, normalize, retime, retry_end, solve_offset, wipe_with_retry, word_times
from karaokedock_ml.separate import level_db


def wiping(frames=40, columns=30, nan_until=0):
    """A line whose columns turn from teal to cyan, left to right."""
    colors = np.tile(np.array([30.0, 110.0, 130.0]), (frames, columns, 1))
    for c in range(columns):
        colors[10 + c :, c] = [40.0, 210.0, 255.0]
    colors[:nan_until] = np.nan
    return colors, [i * 100 for i in range(frames)]


def test_a_wipe_gives_start_end_and_words():
    wipe = find_wipe(*wiping())
    assert wipe is not None
    assert abs(wipe.start - 1000) <= 200 and abs(wipe.end - 3900) <= 200
    times = word_times(["aaa", "bbb"], wipe)
    assert times[0][0] == wipe.start and times[1][1] == wipe.end and times[0][1] == times[1][0]


def test_the_page_changing_afterwards_is_not_the_wipe():
    colors, stamps = wiping()
    colors[36:] = [255.0, 255.0, 255.0]  # the next page arrives, just as big a change
    wipe = find_wipe(colors, stamps)
    assert wipe is not None and abs(wipe.start - 1000) <= 200 and abs(wipe.end - 3500) <= 200  # cut short by the new page


def test_a_fade_of_the_whole_line_is_not_a_wipe():
    colors = np.tile(np.array([30.0, 110.0, 130.0]), (40, 30, 1))
    colors[20:] = [40.0, 210.0, 255.0]
    assert find_wipe(colors, [i * 100 for i in range(40)]) is None


def test_unchanging_text_is_not_a_wipe():
    colors = np.tile(np.array([200.0, 200.0, 200.0]), (40, 30, 1))
    assert find_wipe(colors, [i * 100 for i in range(40)]) is None


def test_retime_uses_the_wipe_for_exact_times_and_words():
    lines = [{**lyric("one two", 1000), "words": ["one", "two"]}, lyric("three", 4000), lyric("four", 7000), lyric("five", 10000)]
    tracks = [track("one two", 5000, 9000), track("three", 8500, 9500), track("four", 11500, 12500), track("five", 14500, 15500)]
    wipes = {0: Wipe([0.0, 1.0], [6000.0, 7500.0])}
    timed, _ = retime(lines, list(enumerate(tracks)), wipes)
    assert (timed[0]["startMs"], timed[0]["endMs"]) == (6000, 7500)
    assert timed[0]["wordTimes"][0][0] == 6000 and timed[0]["wordTimes"][1][1] == 7500


def layout(lyrics, screen):
    """Word counts per row for lyric lines shown on the given screen rows."""
    words = [w for line in lyrics for w in line.split()]
    line_of = [i for i, line in enumerate(lyrics) for _ in line.split()]
    tracks = [track(text, i * 1000, i * 1000 + 800) for i, text in enumerate(screen)]
    matches = align_words([normalize(w) for w in words], tracks)
    return [r["words"] for r in build_rows(words, matches, fill_gaps(matches, line_of), tracks, {})]


def test_a_long_lyric_line_splits_like_the_screen():
    assert layout(["I cant save us my Atlantis we fall"], ["I cant save us", "my Atlantis we fall"]) == [4, 4]


def test_short_lyric_lines_join_like_the_screen():
    assert layout(["I cant save us", "my Atlantis we fall"], ["I cant save us my Atlantis we fall"]) == [8]


def test_misread_and_missing_words_stay_with_their_row():
    assert layout(["we built this town on shaky ground"], ["we bu1lt this town", "on ground"]) == [4, 3]


def backing_layout(lyrics, screen):
    words = [w for line in lyrics for w in line.split()]
    line_of = [i for i, line in enumerate(lyrics) for _ in line.split()]
    tracks = [track(text, i * 1000, i * 1000 + 800) for i, text in enumerate(screen)]
    matches = align_words([normalize(w) for w in words], tracks)
    assigned = keep_backing_together(words, matches, fill_gaps(matches, line_of), line_of)
    return [r["words"] for r in build_rows(words, matches, assigned, tracks, {})]


def test_a_bracketed_line_the_video_skips_keeps_its_own_row():
    assert backing_layout(["we fall down", "(ooh ooh)", "and rise again"], ["we fall down", "and rise again"]) == [3, 2, 3]


def test_brackets_the_video_spreads_over_rows_follow_the_video():
    assert backing_layout(["(every day every hour turn the pain)"], ["every day every hour", "turn the pain"]) == [4, 3]


def test_a_repeated_chorus_only_matches_the_right_repeat():
    tracks = [track("the chorus line", 10000, 12000), track("the chorus line", 90000, 92000)]
    matches = align_words(["the", "chorus", "line"], tracks, [90500, 90500, 90500])
    assert [m[0] for m in matches] == [1, 1, 1]


def test_a_wipe_times_only_the_words_in_the_row():
    tracks = [track("aaa bbb ccc ddd", 0, 4000)]
    words = ["ccc", "ddd"]
    matches = align_words(words, tracks)
    wipe = Wipe([0.0, 1.0], [0.0, 4000.0])
    row = build_rows(words, matches, fill_gaps(matches, [0, 0]), tracks, {id(tracks[0]): wipe})[0]
    assert row["wordTimes"] == [[2000, 3000], [3000, 4000]]


def row_for(words, n, wipe_span=None, height=40):
    t = track(" ".join(words), 0, 9000)
    t.height = height
    return t, ({id(t): Wipe([0.0, 1.0], list(wipe_span))} if wipe_span else {}), {"words": len(words), "_track": n}


def roles(rows_data):
    tracks, found, rows, words = [], {}, [], []
    for n, (ws, span, height) in enumerate(rows_data):
        t, f, r = row_for(ws, n, span, height)
        tracks.append(t)
        found.update(f)
        rows.append(r)
        words += ws
    mark_backing(rows, words, tracks, found)
    return [r.get("role") or ("plain" if r.get("plain") else "main") for r in rows]


def test_backing_vocals_are_told_from_main_lines():
    main = (["we", "fall", "down"], (0.0, 3000.0), 40)
    # Bracketed and wiping alongside the main line.
    assert roles([main, (["(ooh", "ooh)"], (500.0, 2500.0), 40)]) == ["main", "backing"]
    # Bracketed, but the video wipes it alone at full size, so it's a main line.
    assert roles([main, (["(every", "day)"], (4000.0, 6000.0), 40)]) == ["main", "plain"]
    # Smaller text needs no brackets.
    assert roles([main, (["ooh", "ooh"], (500.0, 2500.0), 20), main]) == ["main", "backing", "main"]
    # Two equal rows together are a duet.
    assert roles([main, (["we", "rise", "up"], (0.0, 3000.0), 40)]) == ["main", "main"]
    # Bracketed and shown at full size stays a main line, even with no wipe to follow.
    assert roles([main, (["(every", "day)"], None, 40)]) == ["main", "plain"]


def test_a_bracketed_row_the_video_leaves_out_is_backing():
    t = track("we fall down", 0, 3000)
    t.height = 40
    rows = [{"words": 3, "_track": 0}, {"words": 2, "_track": -2}]
    mark_backing(rows, ["we", "fall", "down", "(ooh", "ooh)"], [t], {})
    assert rows[1]["role"] == "backing" and "role" not in rows[0]


def test_a_missed_wipe_is_found_by_watching_for_longer():
    colors, stamps = wiping()
    assert find_wipe(colors[:19], stamps[:19]) is None  # the text stopped being readable here
    wipe = wipe_with_retry(colors, stamps, 1800)
    assert wipe is not None and abs(wipe.end - 3900) <= 200


def test_a_wipe_found_the_normal_way_ignores_what_comes_later():
    colors, stamps = wiping()
    normal = find_wipe(colors[:36], stamps[:36])
    colors[36:] = [255.0, 255.0, 255.0]
    assert wipe_with_retry(colors, stamps, 3500).times == normal.times


def test_watching_longer_stops_at_the_next_text_on_that_row():
    t = track("first row here", 1000, 3000)
    assert retry_end(t, [t, track("something else", 4000, 6000)]) == 4000
    assert retry_end(t, [t, track("something else", 9000, 11000)]) == 5500
    assert retry_end(t, [t, track("something else", 4000, 6000, y=300)]) == 5500


def test_a_page_of_legal_text_is_not_lyrics():
    tracks = [track("Legal Information", 1000, 5000), track("may need a licence from the owner", 1400, 5000), track("I found you in Atlantis", 9000, 12000)]
    assert [t.text for t in lyric_tracks(tracks, [])] == ["I found you in Atlantis"]


def test_lyrics_read_off_the_screen_use_the_wipe():
    tracks = [track("one two", 1000, 7000, 100), track("three four", 1000, 7000, 200)]
    out = lines_from_tracks(tracks, {id(tracks[1]): Wipe([0.0, 1.0], [4000.0, 6000.0])})
    assert "wordTimes" not in out[0]
    assert (out[1]["startMs"], out[1]["endMs"]) == (4000, 6000) and len(out[1]["wordTimes"]) == 2


def singers(colors, side="after"):
    tracks, found, rows = [], {}, []
    for n, color in enumerate(colors):
        t = track(f"row {n} text", 0, 9000)
        tracks.append(t)
        found[id(t)] = Wipe([0.0, 1.0], [0.0, 1000.0], **{side: color})
        rows.append({"words": 3, "_track": n})
    mark_singers(rows, tracks, found)
    return [r.get("voice") for r in rows]


BLUE, PINK, PURPLE_GREEN, RED, WHITE = (230.0, 90.0, 30.0), (170.0, 60.0, 255.0), (60.0, 220.0, 60.0), (20.0, 30.0, 230.0), (240.0, 240.0, 240.0)


def test_singers_come_from_the_rows_colours():
    assert singers([BLUE, PINK] * 3) == ["boy", "girl"] * 3
    assert singers([BLUE, PINK, PURPLE_GREEN] * 3) == ["boy", "girl", "both"] * 3


def test_singers_can_be_coloured_before_the_wipe():
    assert singers([BLUE, PINK] * 3, side="before") == ["boy", "girl"] * 3


def test_one_colour_means_no_singers():
    assert singers([RED] * 8) == [None] * 8
    assert singers([WHITE] * 8) == [None] * 8


def test_logos_and_title_cards_are_not_lyrics():
    ignore = ["What If I Miss You For The Rest Of My Life?", "Janine Berdin"]
    assert is_junk(track("KARA keyTV", 0, 1400), ignore)
    assert is_junk(track("ANTANTONIA", 1400, 2900), ignore)
    assert is_junk(track("What If I Miss You For The Rest Of My Life", 3000, 6000), ignore)
    assert not is_junk(track("What if I miss you for the rest of my life", 60000, 64000), ignore)
    assert not is_junk(track("You left me standing here", 3000, 6000), ignore)


def test_same_text_on_two_rows_stays_separate():
    samples = [(i * 500, [Box("we build it up", 0, 100), Box("we build it up", 0, 200)]) for i in range(6)]
    assert len(build_tracks(samples)) == 2


def test_quiet_vocals_measure_far_below_the_mix():
    music = np.sin(np.linspace(0, 500, 4000))
    assert level_db(music * 0.01, music) < -30
    assert level_db(music, music) > -10


def track(text, first, last, y=100):
    return Track(text, y, first, last)


def lyric(text, start, end=None):
    return {"text": text, "startMs": start, "endMs": end or start + 2000}


def test_reading_mistakes_still_match():
    assert line_score(normalize("I found you in Atlantis"), normalize("l found y0u in Atlantis")) >= 0.7
    assert line_score(normalize("I found you in Atlantis"), normalize("Subscribe to our channel")) < 0.7


def test_a_wrapped_line_matches_by_its_first_half():
    assert line_score(normalize("We were building castles in the sand tonight"), normalize("We were building castles")) >= 0.7


def test_pieces_on_one_row_are_joined_left_to_right():
    rows = merge_rows([Box("world", 200, 101), Box("hello", 10, 100), Box("below", 10, 200)])
    assert [r.text for r in rows] == ["hello world", "below"]


def test_text_is_followed_across_frames_and_logos_are_dropped():
    samples = []
    for i in range(80):  # 40 seconds at 2 frames a second
        boxes = [Box("Karaoke Channel", 0, 10)]
        if 4 <= i < 12:
            boxes.append(Box("first lyric line", 0, 300))
        samples.append((i * 500, boxes))
    tracks = build_tracks(samples)
    assert [t.text for t in tracks] == ["first lyric line"]
    assert (tracks[0].first, tracks[0].last) == (2000, 5500)


def test_a_repeated_chorus_stays_in_order():
    lines = ["verse one here", "the chorus line", "verse two here", "the chorus line"]
    tracks = [track("verse one here", 1000, 3000), track("the chorus line", 4000, 6000),
              track("verse two here", 7000, 9000), track("the chorus line", 10000, 12000)]
    pairs = match_lines(lines, tracks)
    assert [(i, t.first) for i, t in pairs] == [(0, 1000), (1, 4000), (2, 7000), (3, 10000)]


def test_offset_puts_lines_inside_their_windows():
    pairs = [(1000, 6000, 8000), (5000, 10000, 12000), (9000, 14000, 16000)]
    d = solve_offset(pairs)
    assert all(first <= start + d <= last for start, first, last in pairs)


def test_retime_shifts_every_line_including_unmatched_ones():
    lines = [lyric("one", 1000), lyric("two", 4000), lyric("three", 7000), lyric("not on screen", 10000)]
    tracks = [track("one", 5500, 6500), track("two", 8500, 9500), track("three", 11500, 12500)]
    timed, offset = retime(lines, [(0, tracks[0]), (1, tracks[1]), (2, tracks[2])])
    assert 4500 <= offset <= 5500
    assert timed[3]["startMs"] == 10000 + offset
    assert [t["startMs"] for t in timed] == sorted(t["startMs"] for t in timed)


def test_one_line_outside_its_window_is_nudged_back():
    lines = [lyric("one", 1000), lyric("two", 3000), lyric("three", 5000), lyric("four", 7000)]
    tracks = [track("one", 500, 1500), track("two", 5000, 6000), track("three", 5500, 6500), track("four", 7000, 8000)]
    timed, _ = retime(lines, list(enumerate(tracks)))
    assert 5000 <= timed[1]["startMs"] <= 6000


def test_lyrics_can_be_built_from_a_page_on_screen():
    tracks = [track("top row of lyrics", 1000, 7000, 100), track("second row", 1200, 7000, 200), track("next page", 8000, 9000, 100)]
    out = lines_from_tracks(tracks)
    assert [x["text"] for x in out] == ["top row of lyrics", "second row", "next page"]
    assert out[0]["startMs"] == 1000 and out[1]["startMs"] > out[0]["startMs"] and out[1]["endMs"] <= 8000
