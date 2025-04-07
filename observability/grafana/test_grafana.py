import unittest
import json
import os
from grafana import (
    fs_get_dashboards,
    fs_get_dashboard_folders,
    folder_exists,
    get_folder_uid,
)
from tempfile import mkdtemp


class TestFolder(unittest.TestCase):
    folders = [{"title": "bar", "uid": "a"}, {"title": "foo", "uid": "b"}]
    folders_duplicate = [{"title": "foo", "uid": "a"}, {"title": "foo", "uid": "b"}]

    def test_folder_exists(self):
        self.assertTrue(folder_exists("foo", self.folders))
        self.assertFalse(folder_exists("x", self.folders))

    def test_get_folder_uid(self):
        self.assertEqual(get_folder_uid("bar", self.folders), "a")

    def test_get_folder_uid_assert(self):
        with self.assertRaises(AssertionError):
            get_folder_uid("foo", self.folders_duplicate)


class TestFSOperations(unittest.TestCase):
    def setUp(self):
        self.tmpdir = mkdtemp()
        self.servicedir = os.path.join(self.tmpdir, "test-service")
        self.dashboarddir = os.path.join(self.servicedir, "grafana-dashboards")
        self.other = os.path.join(self.servicedir, "foo")

        os.mkdir(self.servicedir)
        os.mkdir(self.dashboarddir)
        os.mkdir(self.other)

        with open(os.path.join(self.dashboarddir, "test.json"), "w") as testFile:
            testFile.write("{}")

        with open(os.path.join(self.dashboarddir, "test.bar"), "w") as testFile:
            testFile.write("{}")
        return super().setUp()

    def test_fs_get_dashboards(self):
        self.assertListEqual(fs_get_dashboards(self.dashboarddir), [{}])

    def test_fs_get_dashboards_failure(self):
        errorfile = os.path.join(self.dashboarddir, "bar.json")
        with open(errorfile, "w") as testFile:
            testFile.write("{x}")

        with self.assertRaises(json.decoder.JSONDecodeError):
            fs_get_dashboards(self.dashboarddir)

    def test_fs_get_dashboard_folders(self):
        self.assertListEqual(
            fs_get_dashboard_folders(self.tmpdir), ["test-service/grafana-dashboards"]
        )
