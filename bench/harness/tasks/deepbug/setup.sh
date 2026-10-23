cat > textutil.py <<'EOF'
"""Small text helpers shared by the catalog code."""
import re


def collapse_ws(text):
    """Replace every run of whitespace (spaces, tabs, newlines, ...) with a
    single space and strip both ends."""
    return re.sub(r"[ ]{2,}", " ", text).strip()


def truncate(text, limit):
    """Shorten to at most `limit` characters, ending in '...' when cut."""
    if limit < 3:
        raise ValueError("limit must be >= 3")
    if len(text) <= limit:
        return text
    return text[: limit - 3].rstrip() + "..."


def is_blank(text):
    return not text or not text.strip()
EOF
cat > slugs.py <<'EOF'
"""URL-slug generation."""
import re

from textutil import collapse_ws


def slugify(title):
    """'Rock & Roll!' -> 'rock-roll'. Lower case, ASCII letters and digits,
    words joined by single dashes, no leading/trailing dash."""
    text = re.sub(r"[^a-z0-9\s]", "", title.lower())
    return collapse_ws(text).replace(" ", "-")
EOF
cat > catalog.py <<'EOF'
"""A tiny title catalog that refuses duplicate titles."""
from slugs import slugify
from textutil import collapse_ws, is_blank, truncate


class DuplicateTitle(Exception):
    pass


class Catalog:
    def __init__(self):
        self._by_slug = {}

    def add(self, title):
        """Store a title; two titles that differ only in case, punctuation or
        whitespace are duplicates. Returns the slug."""
        if is_blank(title):
            raise ValueError("title must not be blank")
        slug = slugify(title)
        if slug in self._by_slug:
            raise DuplicateTitle(self._by_slug[slug])
        self._by_slug[slug] = title.strip()
        return slug

    def remove(self, title):
        return self._by_slug.pop(slugify(title), None) is not None

    def titles(self):
        return [self._by_slug[s] for s in sorted(self._by_slug)]

    def __len__(self):
        return len(self._by_slug)


def display(title, width=30):
    """Title as shown in listings: one line, collapsed spaces, shortened."""
    return truncate(collapse_ws(title), width)
EOF
cat > test_catalog.py <<'EOF'
import unittest

from catalog import Catalog, DuplicateTitle, display
from slugs import slugify


class SlugTests(unittest.TestCase):
    def test_basic(self):
        self.assertEqual(slugify("Hello World"), "hello-world")
        self.assertEqual(slugify("Rock & Roll!"), "rock-roll")
        self.assertEqual(slugify("  padded  "), "padded")
        self.assertEqual(slugify("A  B"), "a-b")
        self.assertEqual(slugify("Route 66"), "route-66")

    def test_mixed_whitespace(self):
        self.assertEqual(slugify("Hello \t World"), "hello-world")
        self.assertEqual(slugify("one\ntwo\r\nthree"), "one-two-three")
        self.assertEqual(slugify("\tTabbed title\t"), "tabbed-title")


class CatalogTests(unittest.TestCase):
    def test_add_returns_slug_and_counts(self):
        c = Catalog()
        self.assertEqual(c.add("Hello World"), "hello-world")
        self.assertEqual(len(c), 1)

    def test_exact_duplicate_rejected(self):
        c = Catalog()
        c.add("Hello World")
        with self.assertRaises(DuplicateTitle):
            c.add("Hello World")

    def test_case_and_punctuation_duplicates_rejected(self):
        c = Catalog()
        c.add("Hello, World!")
        with self.assertRaises(DuplicateTitle):
            c.add("hello world")

    def test_whitespace_variants_are_duplicates(self):
        c = Catalog()
        c.add("Hello World")
        for variant in ("hello \t world", "Hello\nWorld", "  HELLO   WORLD  "):
            with self.assertRaises(DuplicateTitle, msg=repr(variant)):
                c.add(variant)
        self.assertEqual(len(c), 1)

    def test_blank_title_rejected(self):
        with self.assertRaises(ValueError):
            Catalog().add(" \t ")

    def test_remove_and_titles(self):
        c = Catalog()
        c.add("Zebra")
        c.add("apple pie")
        self.assertEqual(c.titles(), ["apple pie", "Zebra"])
        self.assertTrue(c.remove("APPLE   PIE"))
        self.assertFalse(c.remove("nothing"))
        self.assertEqual(c.titles(), ["Zebra"])


class DisplayTests(unittest.TestCase):
    def test_multiline_title_is_one_line(self):
        self.assertEqual(display("  Hello \n World \t again "), "Hello World again")

    def test_long_title_is_cut(self):
        self.assertEqual(display("word " * 20, 12), "word word...")

    def test_short_title_untouched(self):
        self.assertEqual(display("Short"), "Short")


if __name__ == "__main__":
    unittest.main()
EOF
