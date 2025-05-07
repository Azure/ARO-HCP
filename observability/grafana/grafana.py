from base64 import b64decode


import json
import os
import subprocess
import tempfile


def run_command(command):
    if os.getenv("PRINT_COMMANDS"):
        print(f"calling '{command}'")
    result = subprocess.run(command, shell=True, capture_output=True, text=True)
    if result.returncode != 0:
        print(f"Command failed: {command}\nError: {result.stderr}")
        exit(result.returncode)
    return result.stdout.strip()


def yq_to_json(yaml_file: str) -> str:
    command = f"yq -o=json '.' {yaml_file}"
    result = subprocess.run(command, shell=True, capture_output=True, text=True)
    if result.returncode != 0:
        print(f"Command failed: {command}\nError: {result.stderr}")
        exit(result.returncode)
    return result.stdout.strip()


class GrafanaRunner:
    def __init__(self, rg: str, grafana: str, dry_run: bool):
        self.rg = rg
        self.grafana = grafana
        self.dry_run = dry_run

    def list_existing_folders(self) -> dict[str, any]:
        return json.loads(
            run_command(f'az grafana folder list -g "{self.rg}" -n "{self.grafana}"')
        )

    def create_folder(self, name: str) -> dict[str, any]:
        command_to_run = f'az grafana folder create --only-show-errors  -g "{self.rg}" -n "{self.grafana}" --title "{name}"'
        if self.dry_run:
            print(f"DRY_RUN: {command_to_run}")
            return {}
        return json.loads(run_command(command_to_run))

    def create_dashboard(self, dashboard_file: str) -> dict[str, any]:
        command_to_run = f'az grafana dashboard update --overwrite true -g "{self.rg}" -n "{self.grafana}" --definition "{dashboard_file}"'
        if self.dry_run:
            print(f"DRY_RUN: {command_to_run}")
            return {}
        return json.loads(run_command(command_to_run))

    def delete_dashboard(self, uid: str):
        command_to_run = f'az grafana dashboard delete -g "{self.rg}" -n "{self.grafana}" --dashboard "{uid}"'
        if self.dry_run:
            print(f"DRY_RUN: {command_to_run}")
        else:
            command_to_run

    def list_existing_dashboards(self) -> dict[str, any]:
        return json.loads(
            run_command(f'az grafana dashboard list -g "{self.rg}" -n "{self.grafana}"')
        )

    def show_existing_dashboard(self, uid: str) -> dict[str, any]:
        return json.loads(
            run_command(
                f'az grafana dashboard show --dashboard "{uid}" -g "{self.rg}" -n "{self.grafana}"'
            )
        )


def get_folder_uid(name: str, grafana_folders: list[dict[str, any]]) -> str:
    found = [n for n in grafana_folders if n["title"] == name]
    return found[0]["uid"] if found else ""


def fs_get_dashboards(folder: str) -> list[dict[str, any]]:
    print(f"reading dashboards in {folder}")
    return_array = []
    files = [f for f in os.listdir(folder) if f.endswith(".json")]
    for f in files:
        with open(os.path.join(folder, f)) as dashboard_file:
            dashboard = json.load(dashboard_file)
            if "dashboard" in dashboard:
                return_array.append(dashboard)
            else:
                return_array.append({"dashboard": dashboard})
    return return_array


def get_or_create_folder(
    name: str, g: GrafanaRunner, existing_folders: list[dict[str, any]]
) -> str:
    existing_uid = get_folder_uid(name, existing_folders)
    return existing_uid if existing_uid != "" else g.create_folder(name).get("uid", "")


def create_dashboard(
    temp_file: str,
    dashboard: dict[str, any],
    folder_uid: str,
    existing_dashboards: list[dict[str, any]],
    g: GrafanaRunner,
) -> None:
    with open(temp_file, "w") as f:
        dashboard["folderUid"] = folder_uid
        json.dump(dashboard, f)

    dashboard_found = [
        e
        for e in existing_dashboards
        if e["title"] == dashboard["dashboard"]["title"]
        if e["folderUid"] == folder_uid
    ]
    create_or_update = True
    if dashboard_found:
        assert len(dashboard_found) == 1
        existing_dashboard = g.show_existing_dashboard(dashboard_found[0]["uid"])

        # Deleting info that might change
        # This script uses override and will always create new versions/ids
        if existing_dashboard["dashboard"].get("uid", None):
            del existing_dashboard["dashboard"]["uid"]
        if existing_dashboard["dashboard"].get("id", None):
            del existing_dashboard["dashboard"]["id"]
        if existing_dashboard["dashboard"].get("version", None):
            del existing_dashboard["dashboard"]["version"]
        if dashboard["dashboard"].get("id", None):
            del dashboard["dashboard"]["id"]
        if dashboard["dashboard"].get("version", None):
            del dashboard["dashboard"]["version"]

        if existing_dashboard["dashboard"] == dashboard["dashboard"]:
            print(
                f"Dashboard '{dashboard['dashboard']['title']}' matches, no update needed"
            )
            create_or_update = False

    if create_or_update:
        print(
            f"Dashboard '{dashboard['dashboard']['title']}' differs or does not exist update needed"
        )
        g.create_dashboard(temp_file)


def delete_stale_dashboard(
    d: str,
    dashboards_visited: set[str],
    existing_folders: list[dict[str, any]],
    g: GrafanaRunner,
    azure_managed_folders: list[str],
) -> None:
    if f"{d['folderUid']}_{d['title']}" not in dashboards_visited:
        for amf in azure_managed_folders:
            uid = get_folder_uid(amf, existing_folders)
            if uid and uid == d["folderUid"]:
                return
        g.delete_dashboard(d["title"])


def main():
    RESOURCEGROUP = os.getenv("GLOBAL_RESOURCEGROUP", "global")
    DRY_RUN = os.getenv("DRY_RUN", "false").lower() == "true"
    GRAFANA_NAME = os.getenv("GRAFANA_NAME")
    OBSERVABILITY_CONFIG = os.getenv("OBSERVABILITY_CONFIG", "observability.yaml")

    WORK_DIR = os.path.join(os.path.dirname(__file__), "..")

    config = json.loads(yq_to_json(os.path.join(WORK_DIR, OBSERVABILITY_CONFIG)))

    g = GrafanaRunner(RESOURCEGROUP, GRAFANA_NAME, DRY_RUN)

    existing_folders = g.list_existing_folders()
    existing_dashboards = g.list_existing_dashboards()

    dashboards_visited = set()

    for local_folder in config["grafana-dashboards"]["dashboardFolders"]:
        folder_uid = get_or_create_folder(local_folder["name"], g, existing_folders)

        for dashboard in fs_get_dashboards(
            os.path.join(WORK_DIR, local_folder["path"])
        ):
            temp_file = tempfile.NamedTemporaryFile()
            create_dashboard(
                temp_file.name, dashboard, folder_uid, existing_dashboards, g
            )

            dashboards_visited.add(f"{folder_uid}_{dashboard['dashboard']['title']}")
            os.remove(temp_file.name)

    for d in existing_dashboards:
        delete_stale_dashboard(
            d,
            dashboards_visited,
            existing_folders,
            g,
            config["grafana-dashboards"]["azureManagedFolders"],
        )


if __name__ == "__main__":
    main()
