import unittest
from unittest.mock import MagicMock
import json
import os
import tempfile
from grafana import (
    create_dashboard,
    delete_stale_dashboard,
    fs_get_dashboards,
    get_folder_uid,
    get_or_create_folder,
    GrafanaRunner,
)
from tempfile import mkdtemp


class TestFolder(unittest.TestCase):
    folders = [{"title": "bar", "uid": "a"}, {"title": "foo", "uid": "b"}]

    def test_get_folder_uid(self):
        self.assertEqual(get_folder_uid("a", self.folders), "")
        self.assertEqual(get_folder_uid("bar", self.folders), "a")

    def test_get_or_create_folder(self):
        g = GrafanaRunner("", "", "")
        g.create_folder = MagicMock(return_value={"uid": "foo"})
        self.assertEqual(get_or_create_folder("test", g, []), "foo")

        self.assertEqual(
            get_or_create_folder("test", g, [{"title": "test", "uid": "bar"}]),
            "bar",
        )
        g.create_folder.assert_called_once_with("test")


class TestDashboard(unittest.TestCase):
    def test_create_dashboard(self):
        g = GrafanaRunner("", "", "")
        g.create_dashboard = MagicMock(return_value={})

        temp_file = tempfile.NamedTemporaryFile()

        create_dashboard(temp_file.name, {}, "a", [], g)
        g.create_dashboard.assert_called_once_with(temp_file.name)

    def test_create_dashboard_exists(self):
        g = GrafanaRunner("", "", "")

        d = {"dashboard": {"title": "test"}}
        g.create_dashboard = MagicMock(return_value=d)
        g.show_existing_dashboard = MagicMock(return_value=d)

        temp_file = tempfile.NamedTemporaryFile()

        create_dashboard(
            temp_file.name,
            d,
            "a",
            [{"folderUid": "a", "title": "test", "uid": "bar"}],
            g,
        )
        g.create_dashboard.assert_not_called
        g.show_existing_dashboard.assert_called_once_with("bar")

    def test_delete_stale_dashboard_keep(self):
        g = GrafanaRunner("", "", "")

        g.delete_dashboard = MagicMock(return_value=None)
        delete_stale_dashboard(
            {"folderUid": "a", "title": "n"}, {"a_n"}, [{"uid": "n"}], g, []
        )

        g.delete_dashboard.assert_not_called

    def test_delete_stale_dashboard(self):
        g = GrafanaRunner("", "", "")

        g.delete_dashboard = MagicMock(return_value=None)
        delete_stale_dashboard(
            {"folderUid": "b", "title": "n"},
            {"a_n"},
            [{"title": "n", "uid": "n"}],
            g,
            [],
        )

        g.delete_dashboard.assert_called_once_with("n")

    def test_delete_stale_dashboard_azure_folder(self):
        g = GrafanaRunner("", "", "")

        g.delete_dashboard = MagicMock(return_value=None)
        delete_stale_dashboard(
            {"folderUid": "b", "title": "n"},
            {"a_n"},
            [{"title": "n", "uid": "n"}],
            g,
            ["n"],
        )

        g.delete_dashboard.assert_not_called


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
        self.assertListEqual(fs_get_dashboards(self.dashboarddir), [{"dashboard": {}}])

    def test_fs_get_dashboards_failure(self):
        errorfile = os.path.join(self.dashboarddir, "bar.json")
        with open(errorfile, "w") as testFile:
            testFile.write("{x}")

        with self.assertRaises(json.decoder.JSONDecodeError):
            fs_get_dashboards(self.dashboarddir)
