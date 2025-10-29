from resources_cleanup import (
    resource_group_has_persist_tag_as_true,
    resource_group_is_managed,
    older_than,
    get_date_time_from_str,
    get_dry_run,
    get_boolean_from_string,
    get_creation_time_of_resource_group,
    get_subscription_id,
    get_client_id,
)
import datetime
from collections import namedtuple
import pytest
from azure.mgmt.resource.resources.v2022_09_01.models._models_py3 import ResourceGroup


@pytest.mark.parametrize(
    "input_resource_group,expected",
    [
        (ResourceGroup(location="test_location", name="simple_resource_group", tags=None), False),
        (ResourceGroup(location="test_location", name="simple_resource_group", tags={"persist": "false"}), False),
        (ResourceGroup(location="test_location", name="simple_resource_group", tags={"persist": "tru"}), False),
        (ResourceGroup(location="test_location", name="simple_resource_group", tags={"persist": ""}), False),
        (ResourceGroup(location="test_location", name="simple_resource_group", tags={"some_tag": "something"}), False),
        (ResourceGroup(location="test_location", name="simple_resource_group", tags={"persist": "TRUE"}), True),
        (ResourceGroup(location="test_location", name="simple_resource_group", tags={"persist": "truE"}), True),
        (ResourceGroup(location="test_location", name="simple_resource_group", tags={"persist": "true"}), True),
    ]
)
def test_resource_group_has_persist_tag_as_true(input_resource_group, expected):
    assert resource_group_has_persist_tag_as_true(input_resource_group) == expected


@pytest.mark.parametrize(
    "input_resource_group,expected",
    [
        (ResourceGroup(location="test_location", name="simple_resource_group", managed_by=None), False),
        (ResourceGroup(location="test_location", name="simple_resource_group", managed_by="somebody"), True),
    ]
)
def test_resource_group_is_managed(input_resource_group, expected):
    assert resource_group_is_managed(input_resource_group) == expected


@pytest.mark.parametrize(
    "now_time,creation_time,expected",
    [
        (datetime.datetime(year=2024, month=1, day=25, tzinfo=datetime.timezone.utc), datetime.datetime(year=2024, month=1, day=22, tzinfo=datetime.timezone.utc), True),
        (datetime.datetime(year=2024, month=1, day=25, tzinfo=datetime.timezone.utc), datetime.datetime(year=2024, month=1, day=23, tzinfo=datetime.timezone.utc), False),
        (datetime.datetime(year=2024, month=1, day=25, tzinfo=datetime.timezone.utc), datetime.datetime(year=2024, month=1, day=25, tzinfo=datetime.timezone.utc), False),
    ]
)
def test_older_than_two_days(monkeypatch, now_time, creation_time, expected):
    monkeypatch.setattr("resources_cleanup.datetime.datetime", type("datetime", (), {"now": lambda tz: now_time}))
    assert older_than(creation_time, days=2) == expected


Expected_date = namedtuple("Expected_date", ["year", "month", "day", "hour", "minute", "second"])
@pytest.mark.parametrize(
    "date_time_str,expected",
    [
        ("2023-12-07T18:03:19Z", Expected_date(year=2023, month=12, day=7, hour=18, minute=3, second=19)),
        ("2023-12-07T18:03:19.3628069Z", Expected_date(year=2023, month=12, day=7, hour=18, minute=3, second=19)),
        ("2023-12-07T18:03:19.3628069", Expected_date(year=2023, month=12, day=7, hour=18, minute=3, second=19)),
        ("2023-12-07T18:03:19.362636584736578436729474369", Expected_date(year=2023, month=12, day=7, hour=18, minute=3, second=19)),
        ("2023-12-07T18:03:19.362636584736578436729474369Z", Expected_date(year=2023, month=12, day=7, hour=18, minute=3, second=19)),
    ]
)
def test_get_date_time_from_str(date_time_str, expected: Expected_date):
    date_time = get_date_time_from_str(date_time_str)
    assert date_time.year== expected.year
    assert date_time.month== expected.month
    assert date_time.day== expected.day
    assert date_time.hour== expected.hour
    assert date_time.minute == expected.minute
    assert date_time.second == expected.second


def test_get_date_time_from_str_raises_error_if_input_is_invalid_before_milliseconds_part():
    with pytest.raises(ValueError):
        date_time_str = "20_malformed_7T18:03:19"
        get_date_time_from_str(date_time_str)


def test_get_date_time_from_str_raises_error_if_input_is_empty():
    with pytest.raises(ValueError):
        date_time_str = ""
        get_date_time_from_str(date_time_str)

def test_get_dry_run_from_automation_variable(monkeypatch):
    # Simulate get_automation_variable returning a truthy value
    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: "true")
    assert get_dry_run() is True

    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: "false")
    assert get_dry_run() is False

    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: "TRUE")
    assert get_dry_run() is True

    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: "FALSE")
    assert get_dry_run() is False

def test_get_dry_run_from_env(monkeypatch):
    # Simulate get_automation_variable raising an exception
    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: (_ for _ in ()).throw(Exception("not found")))
    monkeypatch.setenv("DRY_RUN", "true")
    assert get_dry_run() is True

    monkeypatch.setenv("DRY_RUN", "false")
    assert get_dry_run() is False

    monkeypatch.setenv("DRY_RUN", "TRUE")
    assert get_dry_run() is True

    monkeypatch.setenv("DRY_RUN", "FALSE")
    assert get_dry_run() is False

def test_get_dry_run_env_invalid(monkeypatch, capsys):
    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: (_ for _ in ()).throw(Exception("not found")))
    monkeypatch.setenv("DRY_RUN", "notaboolean")
    assert get_dry_run() is False
    captured = capsys.readouterr()
    assert "Warning: Invalid DRY_RUN environment variable value" in captured.out

def test_get_dry_run_default(monkeypatch, capsys):
    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: (_ for _ in ()).throw(Exception("not found")))
    monkeypatch.delenv("DRY_RUN", raising=False)
    assert get_dry_run() is False
    captured = capsys.readouterr()
    assert "Info: DRY_RUN not set in automation variables or environment" in captured.out


# Tests for get_boolean_from_string function
@pytest.mark.parametrize(
    "input_str,expected",
    [
        ("true", True),
        ("True", True),
        ("TRUE", True),
        ("TrUe", True),
        ("false", False),
        ("False", False),
        ("FALSE", False),
        ("FaLsE", False),
        ("  true  ", True),  # with whitespace
        ("  false  ", False),  # with whitespace
        ("  TRUE  ", True),
        ("  FALSE  ", False),
    ]
)
def test_get_boolean_from_string(input_str, expected):
    assert get_boolean_from_string(input_str) == expected


def test_get_boolean_from_string_invalid_value():
    with pytest.raises(ValueError) as exc_info:
        get_boolean_from_string("maybe")
    assert "Invalid truth value" in str(exc_info.value)


def test_get_boolean_from_string_non_string_input():
    with pytest.raises(ValueError) as exc_info:
        get_boolean_from_string(123)
    assert "Expected a string" in str(exc_info.value)


def test_get_boolean_from_string_none_input():
    with pytest.raises(ValueError) as exc_info:
        get_boolean_from_string(None)
    assert "Expected a string" in str(exc_info.value)


# Tests for older_than with 30 days (one month)
@pytest.mark.parametrize(
    "now_time,creation_time,expected",
    [
        (datetime.datetime(year=2024, month=2, day=5, tzinfo=datetime.timezone.utc), datetime.datetime(year=2024, month=1, day=1, tzinfo=datetime.timezone.utc), True),
        (datetime.datetime(year=2024, month=3, day=1, tzinfo=datetime.timezone.utc), datetime.datetime(year=2024, month=1, day=1, tzinfo=datetime.timezone.utc), True),
        (datetime.datetime(year=2024, month=1, day=31, tzinfo=datetime.timezone.utc), datetime.datetime(year=2024, month=1, day=1, tzinfo=datetime.timezone.utc), False),
        (datetime.datetime(year=2024, month=1, day=30, tzinfo=datetime.timezone.utc), datetime.datetime(year=2024, month=1, day=1, tzinfo=datetime.timezone.utc), False),
        (datetime.datetime(year=2024, month=1, day=1, tzinfo=datetime.timezone.utc), datetime.datetime(year=2024, month=1, day=1, tzinfo=datetime.timezone.utc), False),
        # Test reverse order (creation time before now)
        (datetime.datetime(year=2024, month=2, day=5, tzinfo=datetime.timezone.utc), datetime.datetime(year=2024, month=1, day=1, tzinfo=datetime.timezone.utc), True),
    ]
)
def test_older_than_one_month(monkeypatch, now_time, creation_time, expected):
    monkeypatch.setattr("resources_cleanup.datetime.datetime", type("datetime", (), {"now": lambda tz: now_time}))
    assert older_than(creation_time, days=30) == expected


# Tests for get_creation_time_of_resource_group function
def test_get_creation_time_of_resource_group_with_created_at_tag():
    rg = ResourceGroup(
        location="test_location",
        name="test_rg",
        tags={"createdAt": "2023-12-07T18:03:19Z"}
    )
    creation_time = get_creation_time_of_resource_group(rg)
    assert creation_time is not None
    assert creation_time.year == 2023
    assert creation_time.month == 12
    assert creation_time.day == 7


def test_get_creation_time_of_resource_group_without_created_at_tag():
    rg = ResourceGroup(
        location="test_location",
        name="test_rg",
        tags={"someOtherTag": "value"}
    )
    creation_time = get_creation_time_of_resource_group(rg)
    assert creation_time is None


def test_get_creation_time_of_resource_group_no_tags():
    rg = ResourceGroup(
        location="test_location",
        name="test_rg",
        tags=None
    )
    creation_time = get_creation_time_of_resource_group(rg)
    assert creation_time is None


def test_get_creation_time_of_resource_group_invalid_date_format(capsys):
    rg = ResourceGroup(
        location="test_location",
        tags={"createdAt": "invalid-date-format"}
    )
    creation_time = get_creation_time_of_resource_group(rg)
    assert creation_time is None
    captured = capsys.readouterr()
    assert "Warning: Failed to parse createdAt tag" in captured.out
    assert "invalid-date-format" in captured.out


# Tests for get_subscription_id function
def test_get_subscription_id_from_automation_variable(monkeypatch):
    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: "test-subscription-id")
    assert get_subscription_id() == "test-subscription-id"


def test_get_subscription_id_from_env(monkeypatch):
    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: (_ for _ in ()).throw(Exception("not found")))
    monkeypatch.setenv("SUBSCRIPTION_ID", "env-subscription-id")
    assert get_subscription_id() == "env-subscription-id"


def test_get_subscription_id_missing(monkeypatch):
    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: (_ for _ in ()).throw(Exception("not found")))
    monkeypatch.delenv("SUBSCRIPTION_ID", raising=False)
    with pytest.raises(ValueError) as exc_info:
        get_subscription_id()
    assert "Subscription ID missing" in str(exc_info.value)


# Tests for get_client_id function
def test_get_client_id_from_automation_variable(monkeypatch):
    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: "test-client-id")
    assert get_client_id() == "test-client-id"


def test_get_client_id_from_env(monkeypatch):
    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: (_ for _ in ()).throw(Exception("not found")))
    monkeypatch.setenv("CLIENT_ID", "env-client-id")
    assert get_client_id() == "env-client-id"


def test_get_client_id_missing(monkeypatch):
    monkeypatch.setattr("resources_cleanup.get_automation_variable", lambda name: (_ for _ in ()).throw(Exception("not found")))
    monkeypatch.delenv("CLIENT_ID", raising=False)
    with pytest.raises(ValueError) as exc_info:
        get_client_id()
    assert "Client ID missing" in str(exc_info.value)


# Edge case tests for older_than
def test_older_than_exactly_two_days(monkeypatch):
    now_time = datetime.datetime(year=2024, month=1, day=3, hour=12, minute=0, tzinfo=datetime.timezone.utc)
    creation_time = datetime.datetime(year=2024, month=1, day=1, hour=12, minute=0, tzinfo=datetime.timezone.utc)
    monkeypatch.setattr("resources_cleanup.datetime.datetime", type("datetime", (), {"now": lambda tz: now_time}))
    assert older_than(creation_time, days=2) is False


def test_older_than_reverse_order(monkeypatch):
    # Test that absolute value is used (creation time in the future)
    now_time = datetime.datetime(year=2024, month=1, day=22, tzinfo=datetime.timezone.utc)
    creation_time = datetime.datetime(year=2024, month=1, day=25, tzinfo=datetime.timezone.utc)
    monkeypatch.setattr("resources_cleanup.datetime.datetime", type("datetime", (), {"now": lambda tz: now_time}))
    assert older_than(creation_time, days=2) is True