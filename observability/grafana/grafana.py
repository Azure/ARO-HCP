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


def folder_exists(name: str, grafana_folders: list[dict[str, any]]) -> bool:
    return len([n for n in grafana_folders if n["title"] == name]) > 0


def get_folder_uid(name: str, grafana_folders: list[dict[str, any]]) -> str:
    found = [n for n in grafana_folders if n["title"] == name]
    assert len(found) == 1
    return found[0]["uid"]


def fs_get_dashboards(folder: str) -> list[dict[str, any]]:
    print(f"reading dashboards in {folder}")
    return_array = []
    files = [f for f in os.listdir(folder) if f.endswith(".json")]
    for f in files:
        with open(os.path.join(folder, f)) as dashboard_file:
            return_array.append(json.load(dashboard_file))
    return return_array


def fs_get_dashboard_folders(search_root: str) -> list[str]:
    return [
        f[0].removeprefix(search_root).removeprefix("/")
        for f in os.walk(search_root)
        if "/grafana-dashboards" in f[0]
    ]


def main():
    RESOURCEGROUP = os.getenv("GLOBAL_RESOURCEGROUP", "global")
    DRY_RUN = os.getenv("DRY_RUN", "false").lower() == "true"
    GRAFANA_NAME = os.getenv("GRAFANA_NAME")

    SEARCH_ROOT = os.path.join(os.path.dirname(__file__), "..", "..")

    g = GrafanaRunner(RESOURCEGROUP, GRAFANA_NAME, DRY_RUN)

    existing_folders = g.list_existing_folders()
    existing_dashboards = g.list_existing_dashboards()

    dashboards_visited = set()

    for local_folder in fs_get_dashboard_folders(SEARCH_ROOT):
        parts = local_folder.split("/")
        if len(parts) != 2:
            raise RuntimeError(
                f"A 'grafana-dashboards' should be in the first subdirectory only. Error in {local_folder}"
            )

        folder_service_name = parts[0]

        if folder_exists(folder_service_name, existing_folders):
            folder_uid = get_folder_uid(folder_service_name, existing_folders)
        else:
            folder_uid = g.create_folder(folder_service_name)["uid"]

        for dashboard in fs_get_dashboards(os.path.join(SEARCH_ROOT, local_folder)):
            temp_file = tempfile.NamedTemporaryFile()
            with open(temp_file.name, "w") as f:
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
                existing_dashboard = g.show_existing_dashboard(
                    dashboard_found[0]["uid"]
                )

                # Deleting info that might change
                # This script uses override and will always create new versions/ids
                del existing_dashboard["dashboard"]["uid"]
                del existing_dashboard["dashboard"]["id"]
                del existing_dashboard["dashboard"]["version"]
                del dashboard["dashboard"]["id"]
                del dashboard["dashboard"]["version"]

                if existing_dashboard["dashboard"] == dashboard["dashboard"]:
                    print("Dashboard matches, no update needed")
                    create_or_update = False

            if create_or_update:
                print("Dashboard differs or does not exist update needed")
                g.create_dashboard(temp_file.name)

            dashboards_visited.add(f"{folder_uid}_{dashboard["dashboard"]["title"]}")
            os.remove(temp_file.name)

    for d in existing_dashboards:
        k = f"{d["folderUid"]}_{d["title"]}"
        if k not in dashboards_visited:
            if get_folder_uid("Azure Monitor", existing_folders) in k:
                continue
            if get_folder_uid("Microsoft Defender for Cloud", existing_folders) in k:
                continue
            g.delete_dashboard(k.split("_")[1])


if __name__ == "__main__":
    main()
