from datetime import datetime, time

from app.models import ListeningWindow
from app.scheduler import _show_in_any_window


def make_window(weekdays: str, start: str, end: str) -> ListeningWindow:
    return ListeningWindow(
        user_id=1,
        weekdays=weekdays,
        start_time=time.fromisoformat(start),
        end_time=time.fromisoformat(end),
    )


def test_weekday_set_parses_comma_separated_string():
    window = make_window("0,1,2,3,4", "15:00", "17:00")
    assert window.weekday_set() == {0, 1, 2, 3, 4}


def test_show_in_window_matches_weekday_and_time():
    windows = [make_window("0,1,2,3,4", "15:00", "17:00")]
    # 2026-07-20 is a Monday (weekday 0)
    show_start = datetime(2026, 7, 20, 16, 0)
    assert _show_in_any_window(show_start, windows) is True


def test_show_outside_time_range_does_not_match():
    windows = [make_window("0,1,2,3,4", "15:00", "17:00")]
    show_start = datetime(2026, 7, 20, 18, 0)
    assert _show_in_any_window(show_start, windows) is False


def test_show_on_wrong_weekday_does_not_match():
    windows = [make_window("0,1,2,3,4", "15:00", "17:00")]
    # 2026-07-25 is a Saturday (weekday 5), not in the Mon-Fri window
    show_start = datetime(2026, 7, 25, 16, 0)
    assert _show_in_any_window(show_start, windows) is False


def test_show_matches_if_any_of_several_windows_matches():
    windows = [
        make_window("5,6", "20:00", "22:00"),
        make_window("0,1,2,3,4", "15:00", "17:00"),
    ]
    show_start = datetime(2026, 7, 20, 16, 0)  # Monday, matches the second window
    assert _show_in_any_window(show_start, windows) is True


def test_no_windows_never_matches():
    assert _show_in_any_window(datetime(2026, 7, 20, 16, 0), []) is False
