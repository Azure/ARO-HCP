from resources_cleanup import (
    resource_group_has_persist_tag_as_true,
    resource_group_is_managed, 
    time_delta_greater_than_two_days, 
    get_date_time_from_str
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
    "time1,time2,expected", 
    [
        (datetime.datetime(year=2024, month=1, day=22), datetime.datetime(year=2024, month=1, day=25), True),
        (datetime.datetime(year=2024, month=1, day=23), datetime.datetime(year=2024, month=1, day=25), False),
        (datetime.datetime(year=2024, month=1, day=25), datetime.datetime(year=2024, month=1, day=25), False),
    ]
)
def test_time_delta_greater_than_two_days(time1, time2, expected):
    assert time_delta_greater_than_two_days(time1,time2) == expected    
    

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