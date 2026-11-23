"""Check F22/F25 preview rows and real downloads against one Go snapshot."""

import csv
import io
import os
import re
from pathlib import Path
from urllib.parse import urlencode

from playwright.sync_api import expect, sync_playwright

from test_energy_industry import PREVIEW, check_layout, toggle_theme


def screenshot(page, directory, name):
    if directory:
        directory.mkdir(parents=True, exist_ok=True)
        page.screenshot(path=directory / name, type="png", animations="disabled")


API = f"{PREVIEW}/industry-reports"
UI = "http://127.0.0.1:5176/ui/a/energy/reports"
NOTICE = "演示规则，非权威标准"
PATH = {"f22": "metrics", "f25": "custom"}


def open_report(page, kind, **filters):
    with page.expect_response(lambda response: "/preview/industry-reports?" in response.url and response.ok) as request:
        page.goto(f"{UI}/{PATH[kind]}?{urlencode(filters)}")
    report = request.value.json()["data"]
    assert report["kind"] == kind and report["sample"]
    for field in ("building", "energy", "scenario"):
        if field in filters:
            assert report[field] == filters[field]
    return report


def column(report, field, row=0):
    index = next(i for i, item in enumerate(report["columns"]) if item["id"] == field)
    return report["rows"][row][index]


def verify_snapshot(page, report, evidence=None):
    table = page.get_by_role("table", name="报表预览数据")
    headers = [field["label"] for field in report["columns"]]
    if page.viewport_size["width"] >= 640:
        expect(table).to_be_visible()
        expect(table.get_by_role("columnheader")).to_have_text(headers)
        expect(table.locator("tbody tr")).to_have_count(len(report["rows"]))
        for locator, row in zip(table.locator("tbody tr").all(), report["rows"], strict=True):
            assert locator.locator("[data-value]").evaluate_all("cells => cells.map(cell => cell.dataset.value)") == row
    else:
        records = page.get_by_role("list", name="报表预览数据").locator("li")
        expect(records).to_have_count(len(report["rows"]))
        for record, row in zip(records.all(), report["rows"], strict=True):
            record.locator("summary").press("Enter")
            expect(record.locator("details")).to_have_attribute("open", "")
            assert record.locator("dd").evaluate_all("cells => cells.map(cell => cell.dataset.value)") == row
            record.locator("summary").press("Enter")
    expect(page.get_by_role("main").get_by_text(NOTICE, exact=True).first).to_be_visible()
    with page.expect_download() as exported:
        page.get_by_role("button", name="导出 CSV", exact=True).press("Enter")
    assert exported.value.suggested_filename == report["filename"]
    content = exported.value.path().read_bytes()
    assert content == report["csv"].encode("utf-8")
    assert list(csv.reader(io.StringIO(content.decode("utf-8-sig"), newline=""))) == [headers, *report["rows"]]
    assert NOTICE in content.decode("utf-8-sig")
    if evidence:
        evidence.write_bytes(content)


def verify_rules(page, screenshots, evidence):
    hospital = open_report(page, "f22", building="hospital-a", energy="electricity", scenario="standard")
    raw = page.request.get(f"{PREVIEW}/industry?building=hospital-a&energy=electricity&scenario=standard").json()["data"]
    assert hospital["sources"] == raw["sources"]
    assert {metric["value"] for metric in raw["metrics"]} == {24, 128}
    assert {item["label"] for item in hospital["columns"]} >= {"内部目标", "外部定额标准"}
    footer = page.locator('footer[aria-label="报表快照页脚"]')
    expect(footer).to_be_visible()
    expect(footer.get_by_text(hospital["reportId"], exact=True)).to_be_visible()
    expect(footer.get_by_text(hospital["operator"], exact=True)).to_be_visible()
    expect(footer.get_by_text(hospital["timezone"], exact=True)).to_be_visible()
    verify_snapshot(page, hospital, evidence / "report-f22.csv" if evidence else None)
    check_layout(page, 1920)
    screenshot(page, screenshots, "reports-f22-1920-light.png")
    toggle_theme(page, True)
    check_layout(page, 1920)
    toggle_theme(page, False)
    source = page.get_by_role("button", name="查看计量来源", exact=True)
    source.press("Enter")
    drawer = page.get_by_role("dialog", name="计量来源与口径")
    expect(drawer.get_by_text(raw["sources"][0]["name"], exact=True)).to_be_visible()
    page.keyboard.press("Escape")
    expect(source).to_be_focused()

    for bldg, expected_vals in (("office-a", {"1.28", "16"}), ("office-b", {"1.28", ""})):
        office = open_report(page, "f22", building=bldg, energy="electricity", scenario="standard")
        f11 = page.request.get(f"{PREVIEW}/efficiency?building={bldg}&energy=electricity&period=day").json()["data"]
        f12 = page.request.get(f"{PREVIEW}/industry?building={bldg}&energy=electricity&scenario=standard").json()["data"]
        assert office["sources"] == f11["sources"] == f12["sources"]
        assert {column(office, "value", i) for i in range(len(office["rows"]))} == expected_vals
        if bldg == "office-b":
            assert column(office, "denominator", 1) == ""
        verify_snapshot(page, office)

    page.set_viewport_size({"width": 768, "height": 1024})
    for scenario, expected in (
        ("invalid-denominator", "分母"),
        ("out-of-period", "统计期"),
        ("incomparable", "不可比较"),
        ("not-applicable", "不适用"),
        ("no-standard", "无演示规则"),
        ("partial", "覆盖"),
    ):
        report = open_report(page, "f22", building="hospital-a", energy="electricity", scenario=scenario)
        verify_snapshot(page, report)
        assert expected in " ".join(value for row in report["rows"] for value in row), report
        assert "已达标" not in report["csv"] and "正式合规" not in report["csv"]
    check_layout(page, 768)
    toggle_theme(page, True)
    check_layout(page, 768)
    screenshot(page, screenshots, "reports-f22-768-dark.png")
    toggle_theme(page, False)
    print("PASS F22: Go provenance, standard separation, inapplicability, no standard, partial coverage, exact preview/download and keyboard evidence")


def verify_custom(page, screenshots, evidence):
    page.set_viewport_size({"width": 1440, "height": 900})
    large = open_report(page, "f25", building="hospital-a", energy="electricity", scenario="display-stress")
    assert len(large["buildingName"]) > len("合成医院"), large["buildingName"]
    assert any(re.fullmatch(r"\d{8,}(?:\.\d+)?", cell) for row in large["rows"] for cell in row), large["rows"]
    verify_snapshot(page, large)
    check_layout(page, 1440)
    screenshot(page, screenshots, "reports-f25-1440-light.png")
    toggle_theme(page, True)
    check_layout(page, 1440)
    toggle_theme(page, False)

    page.set_viewport_size({"width": 390, "height": 844})
    open_report(page, "f25", building="hotel-a", energy="electricity", scenario="standard")
    check_layout(page, 390)
    trigger = page.get_by_role("button", name="筛选", exact=True)
    trigger.press("Enter")
    drawer = page.get_by_role("dialog")
    drawer.get_by_role("combobox", name="样本情景", exact=True).select_option("missing-denominator")
    with page.expect_response(lambda response: "/preview/industry-reports?" in response.url and "missing-denominator" in response.url and response.ok) as request:
        drawer.get_by_role("button", name="应用筛选", exact=True).press("Enter")
    expect(trigger).to_be_focused()
    missing = request.value.json()["data"]
    assert column(missing, "value") == ""
    verify_snapshot(page, missing)
    toggle_theme(page, True)
    check_layout(page, 390)
    screenshot(page, screenshots, "reports-f25-390-dark-missing.png")

    zero = open_report(page, "f25", building="hotel-a", energy="electricity", scenario="zero")
    assert column(zero, "value") == "0"
    verify_snapshot(page, zero)
    config_button = page.get_by_role("button", name="字段与排序", exact=True)
    config_button.press("Enter")
    config = page.get_by_role("dialog")
    config.get_by_role("combobox", name="排序字段", exact=True).select_option("coverage")
    config.get_by_role("combobox", name="排序方向", exact=True).select_option("desc")
    numerator = next(item["label"] for item in zero["availableFields"] if item["id"] == "numerator")
    control = config.get_by_role("checkbox", name=numerator, exact=True)
    control.press("Space")
    denominator = next(item["label"] for item in zero["availableFields"] if item["id"] == "denominator")
    config.get_by_role("button", name=f"上移{denominator}", exact=True).press("Enter")
    with page.expect_response(lambda response: "/preview/industry-reports?" in response.url and "sort=coverage" in response.url and response.ok) as request:
        config.get_by_role("button", name="应用报表配置", exact=True).press("Enter")
    changed = request.value.json()["data"]
    assert changed["sort"] == "coverage" and changed["direction"] == "desc"
    assert [item["id"] for item in changed["columns"]][:3] == ["metric", "denominator", "value"]
    assert "numerator" not in {item["id"] for item in changed["columns"]}
    expect(config_button).to_be_focused()
    verify_snapshot(page, changed, evidence / "report-f25.csv" if evidence else None)
    check_layout(page, 390)
    screenshot(page, screenshots, "reports-f25-390-dark.png")

    trigger.press("Enter")
    drawer = page.get_by_role("dialog", name="筛选报表范围")
    drawer.get_by_role("combobox", name="行业模板", exact=True).select_option("school")
    with page.expect_response(lambda response: "/preview/industry-reports?" in response.url and "template=school" in response.url and response.ok) as request:
        drawer.get_by_role("button", name="应用筛选").press("Enter")
    assert request.value.json()["data"]["status"] == "no_applicable_template"

    partial = open_report(page, "f25", building="hotel-a", energy="electricity", scenario="partial")
    assert column(partial, "value") == ""
    assert "部分覆盖" in column(partial, "quality") and "缺失率" in column(partial, "quality")
    verify_snapshot(page, partial)

    empty = open_report(page, "f25", building="hotel-a", energy="electricity", scenario="no-template")
    assert not empty["rows"] and NOTICE in empty["csv"]
    expect(page.get_by_role("main").get_by_text(re.compile("无适用.*模板|没有适用.*模板")).first).to_be_visible()
    download = page.get_by_role("button", name="导出 CSV", exact=True)
    if download.count():
        expect(download).to_be_disabled()
    screenshot(page, screenshots, "reports-f25-390-dark-empty.png")

    menu = page.get_by_role("button", name="打开导航")
    menu.press("Enter")
    nav = page.get_by_role("dialog", name="建筑能耗监测分析")
    nav.get_by_role("link", name="指标与定额报表", exact=True).press("Enter")
    expect(page).to_have_url(re.compile(r"/reports/metrics\?building=hotel-a&energy=electricity$"))
    expect(menu).to_be_focused()
    expect(page.get_by_role("status").get_by_text("样本场景已恢复为标准。", exact=True)).to_be_visible()
    print("PASS F25: large values, long synthetic building, mobile filter, missing denominator, valid zero, configured projection, no template, navigation reset")


def verify_rejections(page):
    for building, template, values in (("office-a", "office", {"1.28", "16"}), ("hospital-a", "hospital", {"24", "128"}),
                                       ("school-a", "school", {"3.2"}), ("hotel-a", "hotel", {"16"})):
        response = page.request.get(f"{API}?kind=f25&building={building}&energy=electricity&scenario=standard&template={template}")
        assert response.ok
        report = response.json()["data"]
        assert report["status"] == "ready" and {column(report, "value", i) for i in range(len(report["rows"]))} == values
    for query, expected in (("project=f06-demo", 403), ("fields=terminal_id", 400),
                            ("sort=COUNT%28%2A%29", 400), ("fields=value&fields=coverage", 400)):
        response = page.request.get(f"{API}?kind=f25&building=hospital-a&energy=electricity&scenario=standard&{query}")
        assert response.status == expected and not response.json().get("data"), (query, response.status)
    with page.expect_response(lambda response: "/preview/industry-reports?" in response.url and response.status == 403):
        page.goto(f"{UI}/custom?building=hospital-a&project=f06-demo")
    expect(page.get_by_role("main").get_by_role("alert").get_by_text(re.compile("项目|授权|403")).first).to_be_visible()
    expect(page.get_by_role("button", name="导出 CSV", exact=True)).to_have_count(0)
    for bad_query in ("fields=terminal_id", "sort=COUNT%28%2A%29"):
        with page.expect_response(lambda response: "/preview/industry-reports?" in response.url and response.status == 400):
            page.goto(f"{UI}/custom?building=hospital-a&{bad_query}")
        expect(page.get_by_role("main").get_by_role("alert").get_by_text(re.compile("无效|筛选条件|400")).first).to_be_visible()
        expect(page.get_by_role("button", name="导出 CSV", exact=True)).to_have_count(0)
    print("PASS contract: four industry templates, cross-project denial, unauthorized column, expression sort, duplicate parameter and no download on denial")


def main():
    screenshots = os.environ.get("ENERGY_REPORT_QA_SCREENS")
    evidence = os.environ.get("ENERGY_REPORT_QA_OUTPUT")
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1920, "height": 1080}, accept_downloads=True,
                                reduced_motion="reduce", color_scheme="light")
        page.set_default_timeout(8_000)
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        verify_rules(page, Path(screenshots) if screenshots else None, Path(evidence) if evidence else None)
        verify_custom(page, Path(screenshots) if screenshots else None, Path(evidence) if evidence else None)
        verify_rejections(page)
        assert not errors, errors
        browser.close()


if __name__ == "__main__":
    main()
