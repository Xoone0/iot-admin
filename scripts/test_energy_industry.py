"""Verify F12/F13 in the isolated Playwright Chromium preview."""

import os
import re
from pathlib import Path
from urllib.parse import parse_qs, urlencode, urlparse

from playwright.sync_api import expect, sync_playwright


UI = "http://127.0.0.1:5176/ui/a/energy/efficiency"
PREVIEW = "http://127.0.0.1:5176/admin/v1/analytics/building-energy/preview"


def open_report(page, view, building="office-a", energy="electricity", scenario="standard"):
    query = urlencode({"building": building, "energy": energy, "scenario": scenario})
    with page.expect_response(lambda response: "/preview/industry?" in response.url and response.ok) as request:
        page.goto(f"{UI}/{view}?{query}")
    report = request.value.json()["data"]
    assert report["sample"] and report["building"] == building and report["scenario"] == scenario
    return report


def metric_with_unit(report, unit):
    return next(metric for metric in report["metrics"] if metric["unit"] == unit)


def check_layout(page, width):
    viewport, content = page.evaluate("() => [innerWidth, document.documentElement.scrollWidth]")
    assert viewport == width and content <= width, (viewport, content)
    expect(page.get_by_role("main").get_by_role("heading", level=1)).to_have_count(1)
    expect(page.get_by_text("合成数据", exact=True)).to_be_visible()


def screenshot(page, directory, name):
    if directory:
        directory.mkdir(parents=True, exist_ok=True)
        page.screenshot(path=directory / name, type="png")


def toggle_theme(page, dark):
    desired = "深色" if dark else "浅色"
    page.get_by_role("button", name=f"切换到{desired}主题").press("Enter")
    root = page.locator("html")
    if dark:
        expect(root).to_have_class("dark")
    else:
        expect(root).not_to_have_class("dark")


def verify_templates(page, directory):
    office = open_report(page, "templates")
    f11 = page.request.get(f"{PREVIEW}/efficiency?building=office-a&energy=electricity&period=day").json()["data"]
    assert office["numerator"] == f11["numerator"] and office["sources"] == f11["sources"]
    assert {metric["unit"]: metric["value"] for metric in office["metrics"]} == {
        metric["unit"]: metric["value"] for metric in f11["metrics"]
    } == {"kWh/m²": 1.28, "kWh/人": 16}

    hospital = open_report(page, "templates", "hospital-a")
    bed_days = metric_with_unit(hospital, "kWh/床日")
    revenue = metric_with_unit(hospital, "kWh/万元")
    assert hospital["numerator"]["usage"] == 3072 and hospital["project"] != office["project"]
    assert (bed_days["value"], bed_days["denominator"]["value"], revenue["value"]) == (24, 128, 128)
    assert bed_days["template"]["version"] and bed_days["denominator"]["source"]
    main = page.get_by_role("main")
    bed_metric = main.get_by_role("article", name=re.compile("床日"))
    expect(bed_metric).to_contain_text(re.compile(r"24\s*kWh/床日"))
    expect(main.get_by_role("article", name=re.compile("收入"))).to_be_visible()
    check_layout(page, 1920)
    screenshot(page, directory, "industry-f12-1920-light.png")
    toggle_theme(page, True)
    check_layout(page, 1920)
    toggle_theme(page, False)

    source = main.get_by_role("button", name="查看计量来源").first
    source.press("Enter")
    drawer = page.get_by_role("dialog", name="计量来源与口径")
    expect(drawer.get_by_text(hospital["sources"][0]["name"], exact=True)).to_be_visible()
    page.keyboard.press("Escape")
    expect(source).to_be_focused()
    print("PASS F12: real Go F11 parity, isolated hospital bed-days/revenue, template provenance and source drawer")


def verify_benchmarks(page, directory):
    page.set_viewport_size({"width": 1440, "height": 900})
    hospital = open_report(page, "benchmarks", "hospital-a")
    benchmark = metric_with_unit(hospital, "kWh/床日")["benchmark"]
    assert benchmark["status"] == "within_threshold" and benchmark["limit"] == 25 and benchmark["delta"] == -1
    assert benchmark["standard"]["kind"] == "demo"
    assert all(benchmark["standard"][key] for key in ("source", "publisher", "region", "buildingType", "unit", "from", "to", "condition"))
    assert metric_with_unit(hospital, "kWh/万元")["benchmark"]["status"] == "no_standard"
    main = page.get_by_role("main")
    expect(main.get_by_text("实测指标", exact=True).first).to_be_visible()
    expect(main.get_by_text("演示阈值", exact=True).first).to_be_visible()
    expect(main.get_by_role("heading", name="外部标准", exact=True)).to_be_visible()
    expect(main.get_by_role("heading", name="正式考核", exact=True)).to_be_visible()
    expect(main.get_by_text("已达标", exact=True)).to_have_count(0)
    check_layout(page, 1440)
    screenshot(page, directory, "industry-f13-1440-light.png")
    toggle_theme(page, True)
    check_layout(page, 1440)
    toggle_theme(page, False)

    office = open_report(page, "benchmarks", scenario="no-standard")
    assert any(metric["value"] == 1.28 for metric in office["metrics"])
    internal = main.get_by_role("link", name=re.compile("查看.*内部目标|查看.*年度目标"))
    expect(internal).to_be_visible()
    assert "/efficiency/targets?" in internal.get_attribute("href")
    internal.press("Enter")
    expect(main.get_by_role("heading", name="目标与分期分解", exact=True)).to_be_visible()
    expect(main.get_by_role("status").get_by_text(re.compile("已清除行业样本情景"))).to_be_visible()
    school = open_report(page, "benchmarks", "school-a")
    assert any(metric["value"] == 3.2 and metric["benchmark"]["status"] == "exceeds_threshold" for metric in school["metrics"])

    page.set_viewport_size({"width": 768, "height": 1024})
    result = open_report(page, "benchmarks", "hospital-a", scenario="not-applicable")
    rejected = metric_with_unit(result, "kWh/床日")["benchmark"]
    assert rejected["status"] == "inapplicable" and rejected["delta"] is None
    expect(main.get_by_text(re.compile(r"不适用")).first).to_be_visible()
    check_layout(page, 768)
    toggle_theme(page, True)
    check_layout(page, 768)
    screenshot(page, directory, "industry-f13-768-dark-inapplicable.png")
    missing = open_report(page, "benchmarks", "hospital-a", scenario="no-standard")
    assert all(metric["benchmark"]["standard"] is None and metric["benchmark"]["delta"] is None for metric in missing["metrics"])
    expect(main.get_by_text("无演示规则", exact=True).first).to_be_visible()
    print("PASS F13: demo-only threshold, internal target link, not applicable, absent standard and two comparison directions")


def verify_mobile_states(page, directory):
    page.set_viewport_size({"width": 390, "height": 844})
    data = open_report(page, "templates", "hotel-a")
    metric = metric_with_unit(data, "kWh/间夜")
    assert metric["value"] == 16
    expect(page.get_by_role("main").get_by_role("article", name=re.compile("间夜"))).to_be_visible()
    check_layout(page, 390)
    toggle_theme(page, False)
    check_layout(page, 390)
    toggle_theme(page, True)

    trigger = page.get_by_role("button", name="筛选", exact=True)
    trigger.press("Enter")
    drawer = page.get_by_role("dialog", name="筛选业态与情景")
    drawer.get_by_role("combobox", name="样本情景", exact=True).select_option("missing-denominator")
    with page.expect_response(lambda response: "/preview/industry?" in response.url and "scenario=missing-denominator" in response.url and response.ok) as request:
        drawer.get_by_role("button", name="应用筛选").press("Enter")
    expect(trigger).to_be_focused()
    missing = request.value.json()["data"]
    metric = metric_with_unit(missing, "kWh/间夜")
    assert metric["value"] is None and metric["denominator"] is None and metric["status"] == "missing_denominator"
    expect(page.get_by_role("main").get_by_text(re.compile("缺少.*分母|分母缺失")).first).to_be_visible()
    check_layout(page, 390)
    screenshot(page, directory, "industry-f12-390-dark-missing.png")

    nav_trigger = page.get_by_role("button", name="打开导航")
    nav_trigger.press("Enter")
    nav = page.get_by_role("dialog", name="建筑能耗监测分析")
    nav.get_by_role("link", name="定额标准对标", exact=True).press("Enter")
    expect(page).to_have_url(re.compile(r"/efficiency/benchmarks\?.*scenario=missing-denominator"))
    assert parse_qs(urlparse(page.url).query) == {
        "building": ["hotel-a"], "energy": ["electricity"], "scenario": ["missing-denominator"],
    }
    expect(nav_trigger).to_be_focused()
    zero = open_report(page, "benchmarks", "hotel-a", scenario="zero")
    assert zero["numerator"]["usage"] == 0 and zero["quality"] == "valid_zero"
    assert metric_with_unit(zero, "kWh/间夜")["value"] == 0
    expect(page.get_by_role("main").get_by_text("有效零", exact=True).first).to_be_visible()
    check_layout(page, 390)
    screenshot(page, directory, "industry-f13-390-dark-zero.png")

    for scenario, status in (("incomparable", "not_comparable"), ("partial", "insufficient_data")):
        example = open_report(page, "benchmarks", "hotel-a", scenario=scenario)
        actual = metric_with_unit(example, "kWh/间夜")["benchmark"]
        assert actual["status"] == status and actual["delta"] is None, (scenario, actual)
    nav_trigger.press("Enter")
    nav.get_by_role("link", name="通用能效强度", exact=True).press("Enter")
    expect(page).to_have_url(re.compile(r"/efficiency\?building=office-a&energy=electricity&period=day"))
    expect(page.get_by_role("main").get_by_role("status").get_by_text(re.compile("已从行业专用样本切换为办公楼 A"))).to_be_visible()
    print("PASS mobile: missing denominator, valid zero, incomparable and partial coverage, scenario continuity and keyboard focus")


def main():
    output = os.environ.get("ENERGY_INDUSTRY_EVIDENCE_DIR")
    directory = Path(output) if output else None
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1920, "height": 1080}, reduced_motion="reduce", color_scheme="light")
        page.set_default_timeout(8_000)
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        verify_templates(page, directory)
        verify_benchmarks(page, directory)
        verify_mobile_states(page, directory)
        assert not errors, errors
        browser.close()


if __name__ == "__main__":
    main()
