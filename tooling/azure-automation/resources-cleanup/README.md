## About
The resources_cleanup.py Python script is intended to be used in [Azure Automation](https://learn.microsoft.com/en-us/azure/automation/overview) in order to automatically clean up resource groups of the [ARO Hosted Control Planes (EA Subscription 1)](https://portal.azure.com/#@redhat0.onmicrosoft.com/resource/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/overview) to keep just the minimum resources needed.

## What does the script do?
The flowchart folder contains a flowchart with details about what the script does. It basically iterates over all the resource groups of the subscription and deletes those that satisfy some conditions, skipping the resource groups that have a deny assignment rule.

## Azure Automation
We use the Azure Automation service which includes a range of tools to integrate different aspects of automation of tasks in Azure.

An [Azure Automation Account](https://learn.microsoft.com/en-us/azure/automation/quickstarts/create-azure-automation-account-portal) serves as a box/container for all the automation resources of the same kind/concern. It is a logical group of automation scripts, assets, configurations, etc. Inside the _HCP-automation-account_, we find, among other things:
- a Python [Runbook](https://learn.microsoft.com/en-us/azure/automation/automation-runbook-types?tabs=lps72%2Cpy10) with the content of the file resources_cleanup.py (the script that will handle the deletion of resource groups).
- [Jobs](https://learn.microsoft.com/en-us/azure/automation/automation-runbook-execution#jobs), which are the different actual executions of Runbooks after those are published (explained later).
- [Schedules](https://learn.microsoft.com/en-us/azure/automation/shared-resources/schedules), which contain the configuration regarding when and how often a Runbook should be executed.
- [Python packages](https://learn.microsoft.com/en-gb/azure/automation/python-3-packages?tabs=py3%2Csa-mi), a section containing the dependencies of Python runbooks that are needed for them to run properly.
- Diagnostic Settings section that is used to [Forward Azure Automation diagnostic logs to Azure Monitor](https://learn.microsoft.com/en-us/azure/automation/automation-manage-send-joblogs-log-analytics), that will be used to provide automatic notification on Job failures.
- [Alerts](https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-overview) section which allows the configuration of Alerts based on different custom logic, like number of Jobs run or custom result of Queries of Logs. This also includes action groups, that are used to provide a custom configuration to notify users in a specific way (like sending an email) when an alert is triggered.

### Basic concepts
#### Python script
The Python script in this folder is what will be run in the Azure Automation account. The script does not include configuration about when or how often it should be executed, this is configured directly in Azure Automation, decoupling what the code does with the configuration.

#### Runbook
An executable script is called _Runbook_ in Azure Automation. In order to modify the contents of the Runbook that exists in Azure, we need to go to Azure > Specific Azure Automation Account > Runbooks (Process Automation section) > Click on the Runbook name or Create a new Runbook > Edit > Edit in Portal > Paste the contents of the script in the editor. To execute (test) it: Test Pane > Start > The result of the script will be shown on screen after some seconds. This does not count as a Job as we are testing the Runbook. A Job is an instance of an execution of the Runbook after it is Published (explained below).

#### Job
Once we decide the script is doing what we expect, we need to get out of this _Test pane_ View and go back to _Edit Runbook_ (we can use the top breadcrumb bar) and Click _Publish_. A pop up will ask if we really want to override the previously published version with the new content, we click _Yes_. Once the Runbook is published, we can directly press the _Start_ button, a pop up will appear on the side where we can introduce parameters, if any. We then press _OK_ to execute the Runbook, creating a new _Job_. The view of the current Job will open with the details of that execution. We can go to the _Output_ tab and press the _Refresh_ button (on top of the screen) to see the logs of the Job (after some seconds).

#### Alert rules
Alert rules allow users to define different kind of rules (based on Metrics -number of Job runs-, Logs, etc) that trigger the creation of an alert. Alerts can be viewed and configured in Azure Automation Account > Alerts (Monitoring Section). For this resources_cleanup Runbook, we want to create an alert when a Job fails, so we need to create a [_Log search alert rule_](https://learn.microsoft.com/en-gb/azure/azure-monitor/alerts/alerts-create-log-alert-rule) that basically will run a Query on the Logs of the Jobs and trigger an alert when it detects that a Job has failed.

##### Alert rule for the clean-up resources 
The clean-up resources has defined an alert rule (Home > Automation Accounts > Automation Account > Alert rules) with a signal name _Custom log search_ that will generate an alert based on a certain conditions after executing the following query (which shows Jobs that have failed):

```
AzureDiagnostics 
| where ResourceProvider == "MICROSOFT.AUTOMATION"
    and Category == "JobLogs"
    and (ResultType == "Failed") 
| project TimeGenerated, RunbookName_s, ResultType, _ResourceId, JobId_g
```

In the current alert rule configuration, this query is run every day and we check the aggregation of the past day: each day, that query will be run for the logs of the last 24h and if there is any Job failure in the automation account, it will generate an alert. 

##### Diagnostic settings for the clean-up resources
Inside the Azure Automation Account, in the _Monitoring_ section, we have the _Diagnostic settings_, that are used to configure streaming export of platform logs and metrics for a resource. In our case, we have configured a new Diagnostic setting named _hcp-cleanup-diagnostic_ with a configuration to export the logs from the Jobs to be able to query those logs using the custom query (explained above). It basically defines that the _JobLogs_ should be _Send to Log Analytics workspace_, to the development subscription ARO-HCP, to a Default Workspace.

##### Action group for the clean-up resources
The previous alert rule includes an action group (concept explained below) that will be activated when an alert of that type gets triggered. This action group (email-action-group) basically sends an automatic email to a custom list of recipients with information about the Job failure, to react to it.

#### [Action group](https://learn.microsoft.com/en-gb/azure/azure-monitor/alerts/action-groups)
When Azure Monitor data indicates that there might be a problem with the infrastructure or application, an alert is triggered (previews section). Alerts can contain action groups, which are a collection of notification preferences. So action groups are used to notify users about the alert and take an action. 

#### How to mitigate an Alert
After we know there is an alert (because we received a notification as a consequence of an action group or because we see it in the Alerts view in the Automation Account), we should go to Automation Account > Alerts (Monitoring section) > Select the alert > Click _Change user response_ > Select Closed to mark the alert as mitigated. If we press the _Refresh_ button we will see that the _User response_ field is now updated with the option we previously selected (sometimes we need to press _refresh_ more than once to see the updated value).

## Authentication
The usage of Azure Automation simplifies how our script authenticates to Azure to manage resources (instead of having that running externally and managing credentials). When using Azure Automation, we simply use a System Assigned [Managed Identity](https://learn.microsoft.com/en-us/entra/identity/managed-identities-azure-resources/overview), so we don't need to store or manage credentials in order to make our script work properly.

We can find the Managed Identity associated to the Automation Account by going to Home > The automation Account > Identity (Account Settings Section). There, we find the Object (principal) ID of the Managed Identity and the Permissions section to manage RBAC roles (to grant different permissions to this Managed Identity).

More precisely, for this _resource groups clean-up Runbook_, we use the following role assignments:

|     Role      | Resource Name                                  | Resource Type   | Assigned To            | Condition |
| ------------- | ---------------------------------------------- | -------------   | ---------------------- | --------- |
| Reader        | HCP-automation-rg                              | Resource Group  | HCP-automation-account |   None    |
| Contributor   | ARO Hosted Control Planes (EA Subscription 1)  | Subscription    | HCP-automation-account |   None    |

Regarding the code, we simply use a [ResourceManagementClient class](https://learn.microsoft.com/en-us/python/api/azure-mgmt-resource/azure.mgmt.resource.resources.resourcemanagementclient?view=azure-python) which enables us [authenticate](https://pypi.org/project/azure-mgmt-resource/#Authentication) to Azure without the need to define any explicit environment variable or credentials.

## Python packages 
These are the [packages](https://portal.azure.com/#@redhat0.onmicrosoft.com/resource/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/HCP-automation-rg/providers/Microsoft.Automation/automationAccounts/HCP-automation-account/python2packages) needed for the Python Runbook to work properly (we need to manually Install them in the _Python packages_ section in the Azure Automation account):
- azure_common 1.1.28
- azure_core 1.29.7
- azure_identity 1.15.0
- azure_mgmt_core 1.4.0
- azure_mgmt_resource 23.0.1
- msal 1.26.0
- typing_extensions 4.9.0

Apart from that list, we also have the file _requirements.txt_ containing all the dependencies of the script in order to work, including some development dependencies like Pytest.

In other words, if we were considering the script a regular Python program, we could use directly _requirements.txt_, but if we need to deploy the script in Azure automation as a Runbook, we should manually install the packages from the previous list using the _Python Packages_ section (don't need to install manually everything in _requirements.txt_ as it contains also indirect and development dependencies).

## Development dependencies
The instructions below are intended to be used when modifying the script locally for development purposes and when running the unit test. If we want to install the required dependencies in Azure automation (when "deploying" the script), we should go to the _Python packages_ section and this section does not apply.

For development purposes, we need:
- [Python 3.8](https://www.python.org/downloads/release/python-380/)
- [Pyenv](https://github.com/pyenv/pyenv) ([tutorial](https://towardsdatascience.com/managing-virtual-environment-with-pyenv-ae6f3fb835f8))
- The dependencies listed in _requirements.txt_

### Accessing variables defined in Azure Automation
Instead of hardcoding some values in the script (like the subscription_id) we _[Manage variables](https://learn.microsoft.com/en-us/azure/automation/shared-resources/variables?tabs=azure-powershell#python-functions-to-access-variables)_ in the Variables view (Shared resources) in the Automation Account in Azure (like if they were environment variables) and access them in the code using the [automationassets](https://learn.microsoft.com/en-us/azure/automation/shared-resources/variables?tabs=azure-powershell#python-functions-to-access-variables) module (_import automationassets_). In regard to this Python module, we do NOT need to install it explicitly in Python Packages (Shared resources section) in the Automation Account as it exists by default. But, in order to make that work locally when developing (or running unit tests), we should have a file called _automationassets.py_ with the contents of [this file](https://github.com/azureautomation/python_emulated_assets/blob/master/automationassets/automationassets.py). We can also use a file _localassets.json_ which can contain the variables that are defined in Azure but for local development. [Instructions about this _automationassets.py_](https://github.com/azureautomation/python_emulated_assets). We should not include any of those two files when deploying the resources_cleanup.py script to Azure as this is just to emulate those variables in the cloud when working locally.

### virtual environment activation
After we install Pyenv, we change to the folder where the .py file exists and we create a virtual environment (we just need to do it once)
```sh
pyenv virtualenv 3.8 env 
```

Then, we need to activate the virtual environment:
```sh
pyenv activate env 
```

After that, we can check the virtual environment is active to ensure that when we install a package, it gets installed in the virtual environment. If we execute
```sh
pyenv virtualenvs
```
we should see an asterisk (indicating that this environment is activated) just before the environment details, something like
```
* env (created from /Users/<user>/.pyenv/versions/3.8.18)
```

### requirements.txt
Once the virtual environment is activated, we need to install the dependencies by doing:
```sh
pip install -r requirements.txt
```

### Run the unit tests
We can run the unit tests by doing:
```sh
pytest
```
(we can add "-v" to see the verbose output)

### virtual environment deactivation (optional)
If we want to deactivate the virtual environment, we can deactivate it by doing:
```sh
pyenv deactivate
```
and
```sh
pyenv virtualenvs
```

should not show an asterisk before the env details (indicating the environment is not active):
```
env (created from /Users/afustert/.pyenv/versions/3.8.18)
```

## Custom tags
When possible, the different resources related to this Automation script (the Runbook, the alert rule, etc) contain a Tag in Azure with key _app_ and value _resources_cleanup_.

## Existing policy
We have an ARO-CreatedAt Tag [Policy](https://portal.azure.com/#view/Microsoft_Azure_Policy/PolicyDetailBlade/definitionId/%2Fsubscriptions%2F1d3378d3-5a3f-4712-85a1-2485495dfc4b%2Fproviders%2FMicrosoft.Authorization%2FpolicyDefinitions%2F9d2b25a6-fadb-47d8-bd68-eaf115bc5411) that adds a createdAt tag with the time "now" in UTC format when a resource group is created (as this information is not present by default for resource groups). That tag is used in the script to check if the resource group should be deleted or not.

## Clean Orphaned Role Assignments

This is a Powershell script, that removes role assignments where the granting entity does not exist anymore. Example is giving permission on the global Keyvalt to a Managed Identity in a personal environment and deleting that environment after some time.

This Script needs `Role Based Access Control Administrator` on Scope Subscription

It also needs `Directory Reader` access, which must be granted by Azure Admins, example [DPP-16498](https://issues.redhat.com/browse/DPP-16498)
