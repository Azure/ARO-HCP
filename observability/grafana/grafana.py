import json
import os
import subprocess
import tempfile

def run_command(command):
    result = subprocess.run(command, shell=True, capture_output=True, text=True)
    if result.returncode != 0:
        print(f"Command failed: {command}\nError: {result.stderr}")
        exit(result.returncode)
    return result.stdout.strip()

class GrafanaRunner:
    def __init__(self, rg: str, grafana: str):
        self.rg = rg
        self.grafana = grafana

    def get_existing_folders(self) -> dict[str, any]:
        existing_folders_output = run_command(f'az grafana folder list -g "{self.rg}" -n "{self.grafana}"')
        return json.loads(existing_folders_output)
    
    def create_folder(self, name: str) -> dict[str, any]:
        return json.loads(run_command(f'az grafana folder create --only-show-errors  -g "{self.rg}" -n "{self.grafana}" --title "{name}"'))

    def create_dashboard(self, dashboard_file: str):
        run_command(f'az grafana dashboard update --overwrite true -g "{self.rg}" -n "{self.grafana}" --definition "{dashboard_file}"')


def folder_exists(name: str, grafana_folders: dict[str, any]) ->bool:
    return len([n for n in grafana_folders 
                if n["title"] == name ]) > 0

def get_folder_uid(name: str, grafana_folders: dict[str, any]) ->str:
    found = [n for n in grafana_folders if n["title"] == name ]
    assert len(found) == 1
    return found[0]["uid"]


def fs_get_dashboards(folder: str) -> list[dict[str, any]]:
    print(f"reading dashboards in {folder}")
    return_array =[]
    files = [f for f in os.listdir(folder) if f.endswith('.json')]
    for f in files:
        with open(os.path.join(folder, f)) as dashboard_file:
            return_array.append(json.load(dashboard_file))
    return return_array

def fs_get_dashboard_fodlers() -> list[str]:
    return [ f[0] for f in os.walk("../..") 
            if "/grafana-dashboards" in f[0] ]

def main():
    RESOURCEGROUP = os.getenv('GLOBAL_RESOURCEGROUP', 'global')
    DRY_RUN = os.getenv('DRY_RUN', 'false').lower() == 'true'
    GRAFANA_NAME = os.getenv('GRAFANA_NAME')

    g = GrafanaRunner(RESOURCEGROUP, GRAFANA_NAME)

    existing_folders = g.get_existing_folders()
    
    for local_folder in fs_get_dashboard_fodlers():
        folder_service_name = ""
        for part in local_folder.split("/"):
            if part != "..":
                folder_service_name = part
                break
        if not folder_service_name:
            raise RuntimeError(f"could not determine folder_service_name for local_folder {local_folder}")

        if folder_exists(folder_service_name, existing_folders):
            folder_uid = get_folder_uid(folder_service_name, existing_folders)
        else:
            folder_uid = g.create_folder(folder_service_name)["uid"]
        
        for dashboard in fs_get_dashboards(local_folder):
            temp_file = tempfile.NamedTemporaryFile()
            with open(temp_file.name, 'w') as f:
                dashboard["folderUid"] = folder_uid
                json.dump(dashboard, f)
            
            g.create_dashboard(temp_file.name)
            os.remove(temp_file.name)

if __name__ == "__main__":
    main()