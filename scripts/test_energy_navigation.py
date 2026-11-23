"""Exercise the local building-energy navigation with Playwright Chromium."""

import os
import re
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import expect, sync_playwright


SITE = "http://127.0.0.1:5176/ui"
ENERGY = "/a/energy"
PARENTS = ["能耗总览", "能耗统计分析", "能效指标与考核", "节能诊断与改造评估", "能耗报表"]
ACTIVE = {"F01", "F02", "F03", "F04", "F05", "F06", "F07", "F08", "F09", "F10", "F11", "F12", "F13", "F14", "F16", "F17", "F21", "F22", "F25"}
PARTIAL = {"F01", "F04", "F16"}


def sidebar(page):
    return page.get_by_role("navigation", name="建筑能耗导航", exact=True)


def feature(nav, identifier):
    return nav.locator(f'[data-energy-feature="{identifier}"]')


def parent(nav, label):
    return nav.get_by_role("button", name=label, exact=True)


def active_feature(nav, identifier):
    active = nav.locator('a[aria-current="page"]')
    expect(active).to_have_count(1)
    link = feature(nav, identifier).get_by_role("link")
    expect(link).to_have_attribute("aria-current", "page")
    expect(link).to_be_visible()
    return link


def location(page, path, query=None, fragment=""):
    expect(page).to_have_url(re.compile(re.escape(SITE + path) + r"(?:[?#].*)?$"))
    parsed = urlparse(page.url)
    assert parsed.fragment == fragment, parsed
    if query is not None:
        assert parse_qs(parsed.query) == {key: [value] for key, value in query.items()}, parsed


def verify_content(page):
    main = page.get_by_role("main")
    expect(main.get_by_role("navigation")).to_have_count(0)
    expect(main.get_by_role("tablist")).to_have_count(0)
    expect(main.get_by_role("heading", level=1)).to_have_count(1)
    expect(main.get_by_text("合成数据", exact=True)).to_be_visible()


def tree_structure(page):
    page.goto(SITE + "/")
    location(page, ENERGY + "/overview")
    nav = sidebar(page)
    expect(nav.get_by_role("button")).to_have_count(5)
    active_feature(nav, "F01")
    for label in PARENTS:
        button = parent(nav, label)
        child_id = button.get_attribute("aria-controls")
        assert child_id
        children = nav.locator(f'ul[id="{child_id}"]')
        expect(children.locator("[data-energy-feature]")).to_have_count(5)
        if button.get_attribute("aria-expanded") == "false":
            button.press("Enter")
        expect(button).to_have_attribute("aria-expanded", "true")
        expect(children).to_be_visible()
    items = nav.locator("[data-energy-feature]")
    assert items.evaluate_all("(items) => items.map((node) => node.dataset.energyFeature)") == [
        f"F{index:02}" for index in range(1, 26)
    ]
    expect(nav.get_by_role("link")).to_have_count(len(ACTIVE))
    expect(nav.get_by_text("未开放", exact=True)).to_have_count(25 - len(ACTIVE))
    for ordinal in range(1, 26):
        identifier = f"F{ordinal:02}"
        item = feature(nav, identifier)
        if identifier in ACTIVE:
            expect(item.get_by_role("link")).to_have_attribute("href", re.compile(r"^/ui/a/energy/"))
            if identifier in PARTIAL:
                expect(item.get_by_text("部分开放", exact=True)).to_be_visible()
        else:
            expect(item.locator("[aria-disabled=true]")).to_be_visible()
            expect(item.locator("a,button,[tabindex]")).to_have_count(0)
    parent(nav, PARENTS[0]).press("Space")
    expect(parent(nav, PARENTS[0])).to_have_attribute("aria-expanded", "false")
    expect(feature(nav, "F01")).to_be_hidden()
    expect(nav.get_by_role("button")).to_have_count(5)
    parent(nav, PARENTS[0]).press("Space")
    active_feature(nav, "F01")
    verify_content(page)
    for identifier in sorted(ACTIVE):
        link = feature(nav, identifier).get_by_role("link")
        title = link.locator("span").first.inner_text()
        link.press("Enter")
        expect(page.get_by_role("main").get_by_role("heading", level=1)).to_have_text(title)
        expect(page.get_by_text("页面不存在。", exact=True)).to_have_count(0)
        active_feature(nav, identifier)
    print(f"PASS tree: 5 parent disclosures, 25 ordered features, {len(ACTIVE)} live routes, {25 - len(ACTIVE)} disabled, keyboard toggle and selected item")


def deep_links(page):
    classification = {"building": "office-b", "energy": "water", "period": "14"}
    path = "/statistics"
    page.goto(SITE + ENERGY + "/classification?building=office-b&energy=water&period=14#category-tree")
    location(page, ENERGY + path, classification, "category-tree")
    nav = sidebar(page)
    active_feature(nav, "F06")
    table_title = page.get_by_role("heading", name="分类分项明细", exact=True)
    expect(table_title).to_be_in_viewport()
    page.wait_for_function("scrollY > 0")
    expect(page.get_by_text("5,376", exact=False).first).to_be_visible()
    feature(nav, "F07").get_by_role("link").press("Enter")
    location(page, ENERGY + path + "/dimensions", classification)
    active_feature(nav, "F07")
    feature(nav, "F08").get_by_role("link").press("Enter")
    trend = {"building": "office-b", "energy": "water", "granularity": "day"}
    location(page, ENERGY + path + "/trend", trend)
    active_feature(nav, "F08")
    expect(page.get_by_role("status").filter(has_text="统计周期已改为样本自然日")).to_be_visible()
    page.go_back()
    location(page, ENERGY + path + "/dimensions", classification)
    active_feature(nav, "F07")
    page.reload()
    active_feature(nav, "F07")
    page.get_by_role("button", name="返回能耗统计分析").press("Enter")
    location(page, ENERGY + path, classification)
    active_feature(nav, "F06")
    page.goto(SITE + ENERGY + path + "/trend?building=office-b&energy=water&granularity=month&mode=to-date#f08-chart-title")
    active_feature(nav, "F08")
    expect(page.get_by_role("heading", name="逐月用量趋势")).to_be_in_viewport()
    page.get_by_role("button", name="返回能耗统计分析").press("Enter")
    location(page, ENERGY + path, dict(classification, period="7"))
    expect(page.get_by_role("status").filter(has_text="统计周期已改为样本末 7 天")).to_be_visible()
    verify_content(page)
    print("PASS deep links: legacy F06 query + live anchor scroll, F07/F08 active on reload/back, cross-view filters and reset notice")


def mobile_route(page, identifier):
    trigger = page.get_by_role("button", name="打开导航", exact=True)
    trigger.press("Enter")
    dialog = page.get_by_role("dialog", name="建筑能耗监测分析", exact=True)
    expect(dialog).to_be_visible()
    nav = dialog.get_by_role("navigation", name="建筑能耗导航")
    expect(nav.get_by_role("button")).to_have_count(5)
    expect(active_feature(nav, identifier)).to_be_focused()
    page.keyboard.press("Escape")
    expect(dialog).to_have_count(0)
    expect(trigger).to_be_focused()
    trigger.press("Enter")
    parent(nav, PARENTS[1]).press("Enter")
    feature(nav, "F09").get_by_role("link").press("Enter")
    expect(dialog).to_have_count(0)
    expect(trigger).to_be_focused()
    location(page, ENERGY + "/statistics/balance", {"building": "office-a", "energy": "electricity", "period": "day"})
    expect(page.get_by_role("heading", name="能流与总分表平衡", level=1)).to_be_visible()
    trigger.press("Enter")
    expect(active_feature(nav, "F09")).to_be_focused()
    dialog.get_by_role("button", name="关闭导航").press("Enter")
    expect(dialog).to_have_count(0)
    expect(trigger).to_be_focused()


def responsive(browser, evidence):
    for width, height in [(1920, 1080), (1440, 900), (768, 1024), (390, 844)]:
        page = browser.new_page(viewport={"width": width, "height": height}, color_scheme="light")
        page.goto(SITE + ENERGY + "/overview")
        verify_content(page)
        mobile = width < 1024
        if mobile:
            expect(sidebar(page)).to_have_count(0)
            mobile_route(page, "F01")
        else:
            expect(page.get_by_role("complementary", name="侧边导航")).to_have_css("position", "fixed")
            nav = sidebar(page)
            parent(nav, PARENTS[1]).press("Enter")
            feature(nav, "F09").get_by_role("link").press("Enter")
            active_feature(nav, "F09")
        verify_content(page)
        for theme, opposite in [("light", "深色"), ("dark", "浅色")]:
            expect(page.locator("html")).to_have_class(re.compile(r"(?:^| )dark(?: |$)") if theme == "dark" else "")
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
            if mobile:
                page.get_by_role("button", name="打开导航").press("Enter")
            expect(sidebar(page).get_by_role("button")).to_have_count(5)
            active_feature(sidebar(page), "F09")
            if evidence and (width, theme) in {(1920, "light"), (1440, "dark"), (768, "dark"), (390, "light")}:
                page.screenshot(path=evidence / f"nav-tree-{width}-{theme}.png", animations="disabled")
            if mobile:
                page.keyboard.press("Escape")
                expect(page.get_by_role("dialog", name="建筑能耗监测分析")).to_have_count(0)
            page.get_by_role("button", name=f"切换到{opposite}主题").press("Enter")
        page.close()
        print(f"PASS {width}px: light/dark, no horizontal overflow, active F09" + (", drawer keyboard and focus" if mobile else ", fixed sidebar"))


def technical_route(page):
    page.goto(SITE + "/e/terminals")
    nav = page.get_by_role("navigation", name="主导航", exact=True)
    for heading in ("业务", "快照", "分析", "观测"):
        expect(nav.get_by_text(heading, exact=True)).to_be_visible()
    expect(nav.get_by_role("link", name="终端", exact=True)).to_have_attribute("aria-current", "page")
    expect(page.get_by_role("heading", name="终端", level=1)).to_be_visible()
    expect(sidebar(page)).to_have_count(0)
    print("PASS old technical route and navigation (local preview metadata only)")


def main():
    output = os.environ.get("ENERGY_NAV_EVIDENCE_DIR")
    evidence = Path(output) if output else None
    if evidence:
        evidence.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch()
        page = browser.new_page(viewport={"width": 1440, "height": 900}, color_scheme="light")
        tree_structure(page)
        deep_links(page)
        technical_route(page)
        responsive(browser, evidence)


if __name__ == "__main__":
    main()
