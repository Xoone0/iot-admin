"""Exercise F05 and F14 against the loopback preview with Playwright Chromium."""

import os
import re
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import expect, sync_playwright


UI = "http://127.0.0.1:5176/ui/a/energy"
TARGETS = "/admin/v1/analytics/building-energy/preview/targets?"
SUMMARY = "/overview/targets"
PERIODS = "/efficiency/targets"


def open_report(page, route, query=""):
    with page.expect_response(lambda response: TARGETS in response.url and response.ok) as request:
        page.goto(UI + route + query)
    payload = request.value.json()
    assert payload["code"] == 0 and payload["data"]["sample"]
    return payload["data"]


def check_layout(page, width):
    actual, content = page.evaluate("() => [innerWidth, document.documentElement.scrollWidth]")
    assert actual == width and content <= width, (actual, content)
    expect(page.get_by_role("main").get_by_role("heading", level=1)).to_have_count(1)
    expect(page.get_by_text("合成数据", exact=True)).to_be_visible()


def screenshot(page, directory, name):
    if directory:
        directory.mkdir(parents=True, exist_ok=True)
        page.screenshot(path=directory / name, type="png")


def toggle_theme(page, theme):
    label = "深色" if theme == "dark" else "浅色"
    page.get_by_role("button", name=f"切换到{label}主题").press("Enter")
    if theme == "dark":
        expect(page.locator("html")).to_have_class("dark")
    else:
        expect(page.locator("html")).not_to_have_class("dark")


def change_building(page, building):
    trigger = page.get_by_role("button", name="筛选", exact=True)
    trigger.press("Enter")
    drawer = page.get_by_role("dialog", name="筛选目标范围")
    building_option = drawer.get_by_role("combobox", name="建筑", exact=True)
    expect(building_option).to_be_visible()
    building_option.select_option(building)
    with page.expect_response(lambda response: TARGETS in response.url and f"building={building}" in response.url and response.ok) as request:
        drawer.get_by_role("button", name="应用筛选").press("Enter")
    expect(drawer).to_have_count(0)
    expect(trigger).to_be_focused()
    return request.value.json()["data"]


def verify_annual(page, directory):
    current = open_report(page, SUMMARY)
    assert (current["year"], current["unit"]) == (2026, "kWh")
    assert current["annual"]["target"] == 360_000 and current["annual"]["ratio"] is None
    assert current["annual"]["status"] == "incomplete_period" and current["annual"]["usage"] is None
    expect(page.get_by_text(re.compile(r"360,000"))).to_be_visible()
    expect(page.get_by_role("region", name="年度目标及实测").get_by_text("统计期未闭合", exact=True)).to_be_visible()

    history = open_report(page, SUMMARY, "?building=office-a&energy=electricity&year=2025")
    actual = history["annual"]
    assert actual["target"] == 240_000 and actual["usage"] == 221_520 and actual["ratio"] == 0.923
    assert actual["status"] == "comparable" and actual["coverage"]["rate"] == 1
    assert history["targetSelectedAt"] and history["target"]["version"] and history["measurement"]["sources"]
    main = page.get_by_role("main")
    for value in ["221,520", "240,000", "92.3%"]:
        expect(main.get_by_text(re.compile(re.escape(value))).first).to_be_visible()
    source = main.get_by_role("button", name="查看来源")
    source.press("Enter")
    evidence = page.get_by_role("dialog", name="目标版本与计量来源")
    expect(evidence.get_by_text(history["target"]["version"], exact=True)).to_be_visible()
    expect(evidence.get_by_text(history["measurement"]["sources"][0]["name"], exact=True)).to_be_visible()
    page.keyboard.press("Escape")
    expect(source).to_be_focused()
    check_layout(page, 1920)
    screenshot(page, directory, "targets-f05-1920-light.png")
    toggle_theme(page, "dark")
    check_layout(page, 1920)
    toggle_theme(page, "light")
    link = main.get_by_role("link", name="查看月度分解", exact=True)
    link.press("Enter")
    expect(page).to_have_url(re.compile(r"/efficiency/targets\?"))
    assert parse_qs(urlparse(page.url).query) == {
        "building": ["office-a"], "energy": ["electricity"], "year": ["2025"],
    }
    page.go_back()
    expect(main.get_by_role("heading", name="年度目标摘要", exact=True)).to_be_visible()
    assert parse_qs(urlparse(page.url).query)["year"] == ["2025"]
    nav = page.get_by_role("navigation", name="建筑能耗导航")
    for group, destination in [("能效指标与考核", "目标与分期分解"), ("能耗统计分析", "分类分项统计")]:
        parent = nav.get_by_role("button", name=group, exact=True)
        if parent.get_attribute("aria-expanded") == "false":
            parent.press("Enter")
        nav.get_by_role("link", name=destination, exact=True).press("Enter")
        expect(main.get_by_role("heading", name=destination, exact=True)).to_be_visible()
        if destination == "目标与分期分解":
            assert parse_qs(urlparse(page.url).query)["year"] == ["2025"]
    expect(main.get_by_role("status").get_by_text("已从目标年度切换到当前用量样本周期。")).to_be_visible()
    print("PASS annual: 2026 cutoff withholding, 2025 exact annual ratio, evidence, same-scope link and browser back")


def verify_periods(page, directory):
    page.set_viewport_size({"width": 1440, "height": 900})
    report = open_report(page, PERIODS)
    assert report["target"]["method"] == "seasonal" and len(report["months"]) == 12
    assert sum(month["target"] for month in report["months"]) == report["annual"]["target"]
    september, october, november = report["months"][8:11]
    assert (september["index"], september["target"], september["usage"], september["status"]) == (9, 32_400, 25_200, "comparable")
    assert october["status"] == "partial_coverage" and october["ratio"] is None and october["delta"] is None
    assert (october["usage"], october["coverage"]["missing"], october["coverage"]["suspect"]) == (26_880, 72, 72)
    assert november["status"] == "incomplete_period" and november["ratio"] is None
    table = page.get_by_role("table", name=re.compile("月度目标、实测与覆盖状态"))
    expect(table.get_by_role("row")).to_have_count(13)
    expect(table.get_by_role("row").filter(has=page.get_by_role("rowheader", name=re.compile("^9 月")))).to_contain_text("25,200")
    partial = table.get_by_role("row").filter(has=page.get_by_role("rowheader", name=re.compile("^10 月")))
    expect(partial).to_contain_text("28,800")
    expect(partial).to_contain_text("26,880")
    expect(partial).to_contain_text("实测覆盖不足")
    expect(partial).not_to_contain_text("占目标")
    check_layout(page, 1440)
    screenshot(page, directory, "targets-f14-1440-light.png")
    toggle_theme(page, "dark")
    check_layout(page, 1440)
    toggle_theme(page, "light")

    page.set_viewport_size({"width": 768, "height": 1024})
    check_layout(page, 768)
    toggle_theme(page, "dark")
    check_layout(page, 768)
    screenshot(page, directory, "targets-f14-768-dark.png")
    print("PASS monthly: 12 targets reconcile, September parity, October partial coverage and future month withhold ratio")


def verify_unconfigured(page, directory):
    page.set_viewport_size({"width": 390, "height": 844})
    water = open_report(page, PERIODS, "?building=office-a&energy=water&year=2026")
    assert water["unit"] == "m³" and water["target"]["method"] == "linear"
    assert [month["target"] for month in water["months"]] == [7_500] * 12
    expect(page.get_by_role("table", name=re.compile("月度目标、实测与覆盖状态"))).to_be_visible()
    check_layout(page, 390)
    screenshot(page, directory, "targets-f14-390-dark-linear.png")
    toggle_theme(page, "light")
    check_layout(page, 390)
    toggle_theme(page, "dark")
    report = change_building(page, "office-b")
    assert report["target"] is None and report["annual"]["status"] == "unconfigured"
    assert all(month["target"] is None and month["ratio"] is None for month in report["months"])
    expect(page.get_by_text("未配置有效目标", exact=True)).to_be_visible()
    assert parse_qs(urlparse(page.url).query) == {
        "building": ["office-b"], "energy": ["water"], "year": ["2026"],
    }
    check_layout(page, 390)
    screenshot(page, directory, "targets-f14-390-dark-unconfigured.png")
    print("PASS mobile: linear water target, building filter, unconfigured state, dark theme and keyboard focus")


def main():
    destination = os.environ.get("ENERGY_TARGET_EVIDENCE_DIR")
    directory = Path(destination) if destination else None
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1920, "height": 1080}, reduced_motion="reduce", color_scheme="light")
        page.set_default_timeout(8_000)
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        verify_annual(page, directory)
        verify_periods(page, directory)
        verify_unconfigured(page, directory)
        assert not errors, errors
        browser.close()


if __name__ == "__main__":
    main()
