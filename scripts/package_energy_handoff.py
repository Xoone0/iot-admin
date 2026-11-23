"""Package the building-energy worktree for review using Python's standard library."""

import argparse
import difflib
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import posixpath
import re
import subprocess
import tempfile
from datetime import datetime, timezone
from urllib.parse import unquote, urlsplit
from zipfile import ZIP_DEFLATED, ZipFile


HANDOFF = "docs/energy-analysis/HANDOFF.md"
SHARED_UI = {
    "web/src/App.tsx",
    "web/src/api/energyLive.ts",
    "web/src/components/AppShell.tsx",
    "web/src/components/BuildingEnergyNavigation.tsx",
    "web/src/components/Drawer.tsx",
    "web/src/components/EnergyManagementDrawer.tsx",
    "web/vite.energy-preview.config.mjs",
    "web/vite.energy-live.config.mjs",
}
FORMAL_AUTH_FILES = {
    "cmd/server/main.go",
    "cmd/server/main_test.go",
    "internal/admin/auth.go",
    "internal/admin/auth_test.go",
}
EVIDENCE_NAMES = {
    "go-test.jsonl", "go-build.txt", "go-vet.txt", "web-build.txt",
    "sql-parse.txt", "m1b-go.diff", "browser-qa.json", "f11-f21-playwright.txt", "f11-f21-playwright.py",
    "navigation-playwright.txt", "f09-playwright.txt", "f09-playwright.py", "targets-playwright.txt", "anomalies-playwright.txt",
    "industry-playwright.txt", "industry-reports-playwright.txt", "industry-reports-performance.txt",
    "report-f22.csv", "report-f25.csv",
    "live-readonly-db-test.txt", "live-ui-smoke.txt", "go-live-sim-e2e.txt",
}
ROOT_DOCS = {"AGENTS.md", "FRONTEND_VISUAL_BASELINE.md"}
SCRIPTS = {
    "scripts/package_energy_handoff.py",
    "scripts/test_energy_navigation.py",
    "scripts/test_energy_targets.py",
    "scripts/test_energy_anomalies.py",
    "scripts/test_energy_industry.py",
    "scripts/test_energy_industry_reports.py",
}
MARKDOWN_LINK = re.compile(r"\]\((<[^>]+>|[^\s)]+)\)")
CREDENTIAL = re.compile(
    rb"-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----"
    rb"|(?:postgres(?:ql)?|mysql)://[^\s/:@]+:[^\s/@]+@"
    rb"|(?:gh[pousr]_|github_pat_)[a-zA-Z0-9_]{30,}"
    rb"|(?:AKIA|ASIA)[A-Z0-9]{16}"
    rb"|(?im:^\s*(?:IOT_PG_DSN|IOT_TEST_DSN|DATABASE_URL|PGPASSWORD|DB_PASSWORD|AWS_SECRET_ACCESS_KEY)\s*=\s*[^\s#]+)"
)


def git(root, *args):
    return subprocess.check_output(("git", "-C", str(root), *args))


def path_set(data):
    return set(data.decode("utf-8").rstrip("\0").split("\0")) - {""}


def module_path(name):
    path = PurePosixPath(name)
    return (
        any(path.is_relative_to(folder) for folder in ("internal/buildingenergy", "cmd/energy-preview", "cmd/energy-simgen", "docs/energy-analysis"))
        or (path.parent.as_posix() == "web/src/pages" and path.name.startswith("BuildingEnergy"))
        or name in SHARED_UI
        or name in FORMAL_AUTH_FILES
        or name in ROOT_DOCS
        or name in SCRIPTS
    )


def relevant_source(name):
    path = PurePosixPath(name)
    return (
        (path.suffix == ".go" and any(path.is_relative_to(folder) for folder in ("internal/buildingenergy", "cmd/energy-preview", "cmd/energy-simgen")))
        or (path.suffix == ".sql" and path.is_relative_to("internal/buildingenergy/migrations"))
        or (path.parent.as_posix() == "web/src/pages" and path.name.startswith("BuildingEnergy") and path.suffix == ".tsx")
        or name in SHARED_UI
        or name in FORMAL_AUTH_FILES
        or name in SCRIPTS
    )


def project_doc(name):
    path = PurePosixPath(name)
    return (
        name in ROOT_DOCS
        or (path.parent.as_posix() == "docs/energy-analysis" and path.suffix == ".md")
        or (path.parent.as_posix() == "docs/energy-analysis/review" and path.suffix == ".sql")
    )


def audit_changes(root, base, output, exclusions):
    if git(root, "ls-files", "-u", "-z"):
        raise ValueError("Resolve unmerged paths before packaging")
    untracked = path_set(git(root, "ls-files", "-z", "--others", "--exclude-standard"))
    sources = {
        "committed_since_base": path_set(git(root, "diff", "--name-only", "--no-renames", "-z", base, "HEAD", "--")),
        "staged": path_set(git(root, "diff", "--cached", "--name-only", "--no-renames", "-z", "--")),
        "unstaged": path_set(git(root, "diff", "--name-only", "--no-renames", "-z", "--")),
        "untracked": untracked,
    }
    changed = set().union(*sources.values())
    omitted = {name for name in changed if not (relevant_source(name) or project_doc(name))}
    decisions = {}
    if output.is_relative_to(root):
        output_name = output.relative_to(root).as_posix()
        if output_name in omitted and output_name in untracked and not any(
            output_name in paths for kind, paths in sources.items() if kind != "untracked"
        ):
            with ZipFile(output) as previous:
                manifest = json.loads(previous.read("MANIFEST.json"))
                if HANDOFF not in previous.namelist() or "patch_base" not in manifest:
                    raise ValueError(f"Existing output is not an energy handoff archive: {output_name}")
            decisions[output_name] = "previous handoff output"
        elif output_name in changed or output_name in path_set(git(root, "ls-files", "-z", "--", output_name)):
            raise ValueError(f"Refusing to replace a tracked archive: {output_name}")
    bad_exclusions = []
    for item in exclusions:
        path, sep, reason = item.partition("=")
        if not sep or not reason.strip() or path in decisions or path not in omitted or module_path(path):
            bad_exclusions.append(path)
        else:
            decisions[path] = reason.strip()
    unresolved = omitted - decisions.keys()
    if omitted:
        for path in sorted(omitted):
            print(f"Omitted: {path} | {decisions.get(path, 'unclassified; blocks delivery')}", flush=True)
    if bad_exclusions:
        raise ValueError(f"Exclusions must name distinct unrelated changed paths and give reasons: {bad_exclusions}")
    if unresolved:
        raise ValueError("Classify every omitted change. Add related files to the package scope")
    entries = [
        {"path": path, "changes": [kind for kind, paths in sources.items() if path in paths],
         "selected": path not in omitted, **({"exclusion_reason": decisions[path]} if path in decisions else {})}
        for path in sorted(changed)
    ]
    return changed - omitted, untracked, entries


def bundle_link_targets(root, base_paths, contents, statuses, audit):
    changed_paths = {entry["path"] for entry in audit}
    for name, data in tuple(contents.items()):
        if not name.startswith("docs/energy-analysis/") or not name.endswith(".md"):
            continue
        for href in MARKDOWN_LINK.findall(data.decode("utf-8")):
            href = href.removeprefix("<").removesuffix(">")
            if "\\" in href or re.match(r"^[A-Za-z]:", href) or href.startswith(("file:", "/")):
                raise ValueError(f"Local absolute link in {name}: {href}")
            parts = urlsplit(href)
            if parts.scheme or not parts.path:
                continue
            target = posixpath.normpath(posixpath.join(posixpath.dirname(name), unquote(parts.path)))
            if target in contents:
                continue
            if target in base_paths and target not in changed_paths and PurePosixPath(target).suffix in {".go", ".ts", ".tsx", ".sql", ".css"}:
                contents[target] = read_file(root / target, root)
                statuses[target] = "baseline_reference"
            else:
                raise ValueError(f"Missing portable link target in {name}: {href}")


def read_file(path, directory):
    if path.is_symlink() or not path.resolve().is_relative_to(directory) or not path.is_file():
        raise ValueError(f"Expected a regular file inside its input directory: {path.name}")
    return path.read_bytes()


def text_lines(data):
    return io.StringIO(data.decode("utf-8").replace("\r\n", "\n")).readlines()


def patch_for(name, original, current):
    if original == current:
        return ""
    before = text_lines(original) if original is not None else []
    after = text_lines(current) if current is not None else []
    if before == after and original is not None and current is not None:
        return ""
    patch = [f"diff --git a/{name} b/{name}\n"]
    if original is None:
        patch.append("new file mode 100644\n")
    elif current is None:
        patch.append("deleted file mode 100644\n")
    lines = difflib.unified_diff(
        before, after,
        f"a/{name}" if original is not None else "/dev/null",
        f"b/{name}" if current is not None else "/dev/null",
    )
    for line in lines:
        patch.append(line)
        if not line.endswith("\n"):
            patch.append("\n\\ No newline at end of file\n")
    return "".join(patch)


def verify_patch(patch, changes):
    with tempfile.TemporaryDirectory(prefix="energy-handoff-verify-") as folder:
        scratch = Path(folder)
        for name, (original, _) in changes.items():
            if original is not None:
                file = scratch / name
                file.parent.mkdir(parents=True, exist_ok=True)
                file.write_bytes(original)
        if patch:
            patch_path = scratch / "energy-analysis.patch"
            patch_path.write_bytes(patch)
            subprocess.run(("git", "apply", "--check", "--whitespace=nowarn", str(patch_path)), cwd=scratch, check=True)
            subprocess.run(("git", "apply", "--whitespace=nowarn", str(patch_path)), cwd=scratch, check=True)
        for name, (_, expected) in changes.items():
            file = scratch / name
            if expected is None:
                if file.exists():
                    raise ValueError(f"Patch did not delete {name}")
            elif not file.is_file() or text_lines(file.read_bytes()) != text_lines(expected):
                raise ValueError(f"Patch did not reproduce {name}")


def write_archive(output, contents):
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=output.parent, suffix=".zip", delete=False) as temp:
        pending = Path(temp.name)
    try:
        with ZipFile(pending, "w", ZIP_DEFLATED) as archive:
            for path, data in sorted(contents.items()):
                archive.writestr(path, data)
        with ZipFile(pending) as archive:
            if archive.testzip() is not None or set(archive.namelist()) != contents.keys():
                raise ValueError("ZIP failed integrity or inventory validation")
            manifest = json.loads(archive.read("MANIFEST.json"))
            for entry in manifest["files"]:
                digest = hashlib.sha256(archive.read(entry["path"])).hexdigest()
                if digest != entry["sha256"]:
                    raise ValueError(f"SHA-256 mismatch: {entry['path']}")
        os.replace(pending, output)
    finally:
        pending.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True, help="Destination ZIP, typically outside the worktree")
    parser.add_argument("--base", default="HEAD", help="Git commit to compare against")
    parser.add_argument("--evidence-dir", type=Path, help="Directory of review-safe Go and web verification logs")
    parser.add_argument("--screenshot", type=Path, action="append", default=[], help="Approved synthetic preview image")
    parser.add_argument("--exclude", action="append", default=[], metavar="PATH=REASON",
                        help="Record an explicitly unrelated omission outside the package scope")
    parser.add_argument("--prd", type=Path, help="Record the PRD filename and SHA-256 without copying its content")
    args = parser.parse_args()

    root = Path(git(Path.cwd(), "rev-parse", "--show-toplevel").decode().strip()).resolve()
    base = git(root, "rev-parse", "--verify", "--end-of-options", f"{args.base}^{{commit}}").decode().strip()
    head = git(root, "rev-parse", "HEAD").decode().strip()
    branch = git(root, "rev-parse", "--abbrev-ref", "HEAD").decode().strip()
    base_paths = path_set(git(root, "ls-tree", "-rz", "--name-only", base))
    tracked = path_set(git(root, "ls-files", "-z"))
    output = args.output.resolve()
    if output.name != "energy-analysis-handoff.zip":
        raise ValueError("Output filename must be energy-analysis-handoff.zip")
    changed, untracked, audit = audit_changes(root, base, output, args.exclude)
    selected = changed | {name for name in tracked if project_doc(name)}
    contents = {}
    statuses = {}
    deleted = []
    patch_parts = []
    patches = {}
    for name in sorted(selected):
        path = root / name
        current = read_file(path, root) if path.exists() or path.is_symlink() else None
        original = git(root, "show", f"{base}:{name}") if name in base_paths and name in changed else None
        if name in changed:
            patch_parts.append(patch_for(name, original, current))
            patches[name] = original, current
        if current is not None:
            contents[name] = current
            statuses[name] = "untracked" if name in untracked else ("changed" if name in changed else "tracked")
        else:
            deleted.append(name)
    if HANDOFF not in contents:
        raise ValueError(f"Missing {HANDOFF}")
    patch = "".join(patch_parts).encode("utf-8")
    verify_patch(patch, patches)
    if patch:
        contents["energy-analysis.patch"] = patch

    if args.evidence_dir:
        directory = args.evidence_dir.resolve(strict=True)
        for name in sorted(EVIDENCE_NAMES):
            if (directory / name).is_file():
                contents[f"evidence/{name}"] = read_file(directory / name, directory)
        if not any(name.startswith("evidence/") for name in contents):
            raise ValueError("Evidence directory contains none of the supported verification logs")
    for image in args.screenshot:
        signature = {".jpg": b"\xff\xd8\xff", ".jpeg": b"\xff\xd8\xff", ".png": b"\x89PNG\r\n\x1a\n"}.get(image.suffix.lower())
        member = f"evidence/screenshots/{image.name}"
        if signature is None or member in contents:
            raise ValueError(f"Unsupported image or duplicate screenshot name: {image.name}")
        data = read_file(image, image.parent.resolve())
        if not data.startswith(signature):
            raise ValueError(f"Image header does not match its extension: {image.name}")
        contents[member] = data

    bundle_link_targets(root, base_paths, contents, statuses, audit)
    for path, data in contents.items():
        if not path.startswith("evidence/screenshots/") and CREDENTIAL.search(data):
            raise ValueError(f"Credential-like content in {path}")
    prd = None
    if args.prd:
        prd = {"filename": args.prd.name,
               "sha256": hashlib.sha256(read_file(args.prd, args.prd.parent.resolve())).hexdigest()}
        if prd["sha256"].encode() not in contents[HANDOFF].lower():
            raise ValueError("PRD digest does not match HANDOFF.md")
    manifest = {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "branch": branch,
        "head": head,
        "patch_base": base,
        "prd": prd,
        "git_changes": audit,
        "deleted": deleted,
        "files": [
            {"path": name, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest(),
             "status": statuses.get(name, "artifact")}
            for name, data in sorted(contents.items())
        ],
    }
    contents["MANIFEST.json"] = (json.dumps(manifest, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    write_archive(output, contents)
    print(f"{output} ({len(contents)} entries, {len(audit)} Git paths, {sum(not e['selected'] for e in audit)} explicitly omitted)")
    print(f"ZIP SHA-256: {hashlib.sha256(output.read_bytes()).hexdigest()}")


if __name__ == "__main__":
    main()
