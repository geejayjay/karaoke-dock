from karaokedock_ml.align import loudest_start, map_words


def test_loudest_part_is_found():
    assert loudest_start([0, 0, 1, 9, 9, 1, 0], 2) == 3
    assert loudest_start([1, 2], 30) == 0


def w(text, start=0):
    return {"text": text, "startMs": start, "endMs": start + 10}


def test_words_return_to_their_lines():
    out = map_words(["hello there", "second line"], [w("hello"), w("there"), w("second"), w("line")])
    assert [[x["text"] for x in r] for r in out] == [["hello", "there"], ["second", "line"]]


def test_split_tokens_are_kept_together_by_character_count():
    out = map_words(["don't stop", "go"], [w("do"), w("n't"), w("stop"), w("go")])
    assert [len(r) for r in out] == [3, 1]


def test_cjk_lines():
    out = map_words(["你好", "世界"], [w("你"), w("好"), w("世"), w("界")])
    assert [len(r) for r in out] == [2, 2]


def test_short_result_leaves_later_lines_empty():
    out = map_words(["one", "two"], [w("one")])
    assert out[1] == []
