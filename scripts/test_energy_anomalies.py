"""Exercise F10 and F17 with Playwright Chromium against the loopback preview."""

import os
import re
from pathlib import Path
from urllib.parse import urlencode

from playwright.sync_api import expect, sync_playwright


UI = "http://127.0.0.1:5176/ui/a/energy"
API = "/admin/v1/analytics/building-energy/preview/anomalies?"
ANOMALIES = "/statistics/anomalies"
OPPORTUNITIES = "/diagnosis/opportunities"


def open_report(page, view, scenario="standard", building="office-a", energy="electricity"):
    query = urlencode({"building": building, "energy": energy, "scenario": scenario})
    with page.expect_response(lambda response: API in response.url and response.ok) as request:
        page.goto(f"{UI}{view}?{query}")
    data = request.value.json()["data"]
    assert data["sample"] and data["scenario"] == scenario
    return data


def screenshot(page, directory, name):
    if directory:
        directory.mkdir(parents=True, exist_ok=True)
        page.screenshot(path=directory / name, type="png")


def check_layout(page, width):
    viewport, content = page.evaluate("() => [innerWidth, document.documentElement.scrollWidth]")
    assert viewport == width and content <= width, (viewport, content)
    expect(page.get_by_role("main").get_by_role("heading", level=1)).to_have_count(1)
    expect(page.get_by_text("合成数据", exact=True)).to_be_visible()


def toggle_theme(page, dark):
    theme = "深色" if dark else "浅色"
    page.get_by_role("button", name=f"切换到{theme}主题").press("Enter")
    root = page.locator("html")
    if dark:
        expect(root).to_have_class("dark")
    else:
        expect(root).not_to_have_class("dark")


def verify_candidate(page, directory):
    data = open_report(page, ANOMALIES)
    finding = data["finding"]
    assert finding["status"] == "usage_candidate"
    assert (data["current"]["usage"], finding["baseline"], finding["delta"], finding["changeRate"]) == (1536, 384, 1152, 3)
    assert finding["baselineCount"] == 3 and data["rule"]["version"]
    main = page.get_by_role("main")
    expect(main.get_by_text("异常候选", exact=True)).to_be_visible()
    for value in (r"1,536", r"384", r"300%"):
        expect(main.get_by_text(re.compile(value)).first).to_be_visible()
    check_layout(page, 1920)
    screenshot(page, directory, "anomalies-f10-1920-light.png")
    toggle_theme(page, True)
    check_layout(page, 1920)
    toggle_theme(page, False)

    source = main.get_by_role("button", name=re.compile("查看.*来源|查看.*证据")).first
    source.press("Enter")
    dialog = page.get_by_role("dialog")
    expect(dialog.get_by_text(data["rule"]["version"], exact=False).first).to_be_visible()
    expect(dialog.get_by_text(data["current"]["sources"][0]["name"], exact=False).first).to_be_visible()
    page.keyboard.press("Escape")
    expect(dialog).to_have_count(0)
    expect(source).to_be_focused()
    print("PASS F10: measured 1536, same-weekday median 384, increase 1152 / 300%, rule version and metering drill-down")


def verify_quality_and_zero(page, directory):
    checks = {
        "missing": ("metering_anomaly", None),
        "suspect": ("metering_anomaly", None),
        "partial": ("metering_anomaly", None),
        "zero": ("no_candidate", 0),
        "normal": ("no_candidate", 384),
        "incomparable": ("insufficient_evidence", None),
        "baseline-zero": ("insufficient_evidence", None),
        "no-rule": ("insufficient_evidence", None),
    }
    page.set_viewport_size({"width": 390, "height": 844})
    toggle_theme(page, True)
    for scenario, (status, expected_usage) in checks.items():
        data = open_report(page, ANOMALIES, scenario)
        finding = data["finding"]
        assert finding["status"] == status, (scenario, finding)
        if scenario != "no-rule":
            assert not data["opportunities"], (scenario, data["opportunities"])
        if expected_usage is not None or scenario == "missing":
            assert data["current"]["usage"] == expected_usage, (scenario, data["current"])
        if status == "metering_anomaly":
            assert finding["usageStatus"] == "insufficient_evidence"
            assert finding["changeRate"] is None
            quality = data["current"]["coverage"]
            assert quality["missing"] > 0 or quality["suspect"] > 0
            expect(page.get_by_role("main").get_by_text("证据不足", exact=True).first).to_be_visible()
            if scenario == "suspect":
                check_layout(page, 390)
                screenshot(page, directory, "anomalies-f10-390-dark-suspect.png")
        elif status == "insufficient_evidence":
            expect(page.get_by_role("main").get_by_text("证据不足", exact=True).first).to_be_visible()
        else:
            expect(page.get_by_role("main").get_by_text("未发现用量异常候选", exact=True)).to_be_visible()
            if scenario == "zero":
                expect(page.get_by_role("region", name="计量数据质量").get_by_text("有效零值", exact=True)).to_be_visible()
    toggle_theme(page, False)
    check_layout(page, 390)

    trigger = page.get_by_role("button", name="筛选", exact=True)
    trigger.press("Enter")
    dialog = page.get_by_role("dialog")
    dialog.get_by_role("combobox", name=re.compile("场景")).select_option("zero")
    with page.expect_response(lambda response: API in response.url and "scenario=zero" in response.url and response.ok):
        dialog.get_by_role("button", name="应用筛选").press("Enter")
    expect(trigger).to_be_focused()
    expect(page).to_have_url(re.compile("scenario=zero"))
    print("PASS F10: missing, suspect, partial, true zero, no candidate, incompatible range, zero baseline, absent rule and keyboard filter")


def verify_opportunities(page, directory):
    page.set_viewport_size({"width": 1440, "height": 900})
    data = open_report(page, OPPORTUNITIES)
    clues = data["opportunities"]
    assert {clue["kind"] for clue in clues} == {"baseline_increase", "high_usage_observation"}
    assert all(clue["status"] == "pending_manual_review" for clue in clues)
    assert all(clue["evidence"] and clue["limitations"] and clue["confirm"] and clue["sources"] for clue in clues)
    peer = next(clue for clue in clues if clue["kind"] == "high_usage_observation")
    assert peer["usage"] == 1056 and peer["comparablePeers"] >= 2
    main = page.get_by_role("main")
    expect(main.get_by_text("待人工核查", exact=True).first).to_be_visible()
    check_layout(page, 1440)
    screenshot(page, directory, "anomalies-f17-1440-light.png")
    toggle_theme(page, True)
    check_layout(page, 1440)
    page.set_viewport_size({"width": 768, "height": 1024})
    check_layout(page, 768)
    screenshot(page, directory, "anomalies-f17-768-dark.png")
    toggle_theme(page, False)
    check_layout(page, 768)

    high_usage = main.get_by_role("article", name="核查区域高用量")
    trigger = high_usage.get_by_role("button", name="查看计量来源")
    trigger.press("Enter")
    drawer = page.get_by_role("dialog", name="计量来源与口径")
    expect(drawer.get_by_text(peer["sources"][0]["name"], exact=False).first).to_be_visible()
    drawer.get_by_text("计算与目录版本", exact=True).press("Enter")
    expect(drawer.get_by_text(peer["measurement"]["ruleVersion"], exact=True)).to_be_visible()
    page.keyboard.press("Escape")
    expect(trigger).to_be_focused()
    main.get_by_role("link", name=re.compile("查看.*异常|查看.*候选")).first.press("Enter")
    expect(page).to_have_url(re.compile(ANOMALIES))
    page.go_back()
    expect(main.get_by_role("heading", name="节能机会分析", exact=True)).to_be_visible()

    no_rule = open_report(page, OPPORTUNITIES, "no-rule")
    assert no_rule["finding"]["usageStatus"] == "insufficient_evidence"
    assert [clue["kind"] for clue in no_rule["opportunities"]] == ["high_usage_observation"]
    expect(main.get_by_role("article", name="核查区域高用量")).to_be_visible()
    expect(main.get_by_role("article", name="核查用量上升")).to_have_count(0)
    nav_trigger = page.get_by_role("button", name="打开导航")
    nav_trigger.press("Enter")
    nav = page.get_by_role("dialog", name="建筑能耗监测分析")
    nav.get_by_role("button", name="能耗统计分析", exact=True).press("Enter")
    nav.get_by_role("link", name="异常浪费识别", exact=True).press("Enter")
    expect(page).to_have_url(re.compile(r"/statistics/anomalies\?.*scenario=no-rule"))
    expect(nav_trigger).to_be_focused()
    expect(main.get_by_text("证据不足", exact=True)).to_be_visible()

    no_clues = open_report(page, OPPORTUNITIES, "missing")
    assert not no_clues["opportunities"]
    expect(main.get_by_text(re.compile("暂无.*线索")).first).to_be_visible()
    print("PASS F17: both evidence-based leads are pending human review, scoped source link and no-clue state")


def main():
    output = os.environ.get("ENERGY_ANOMALY_EVIDENCE_DIR")
    directory = Path(output) if output else None
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1920, "height": 1080}, reduced_motion="reduce", color_scheme="light")
        page.set_default_timeout(8_000)
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        verify_candidate(page, directory)
        verify_quality_and_zero(page, directory)
        verify_opportunities(page, directory)
        assert not errors, errors
        browser.close()


if __name__ == "__main__":
    main()
