import unittest

from grafana import folder_exists


class TestFolderExists(unittest.TestCase):

    def test_folder_exists(self):
        folders = [{"title": "bar"}, {"title": "foo"}]
        assert folder_exists("foo", folders)
        assert not folder_exists("x", folders)
