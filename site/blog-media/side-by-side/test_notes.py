"""Tests of notes.py's add and list, on a notes file of their own."""
import io
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path

import notes


class NotesTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        notes.NOTES = Path(self.dir.name) / "notes.txt"

    def tearDown(self):
        self.dir.cleanup()

    def run_notes(self, *argv):
        out = io.StringIO()
        with redirect_stdout(out):
            code = notes.main(list(argv))
        return code, out.getvalue()

    def test_add_and_list(self):
        self.run_notes("add", "buy milk")
        self.run_notes("add", "call the bank")
        code, out = self.run_notes("list")
        self.assertEqual(code, 0)
        self.assertIn("1  buy milk", out)
        self.assertIn("2  call the bank", out)


if __name__ == "__main__":
    unittest.main()
