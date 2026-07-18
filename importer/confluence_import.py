#!/usr/bin/env python3
"""
One-shot migration tool: read an existing Confluence on-call rotation table and
emit the canonical schedule-as-code YAML that this project uses going forward.
After you have a good schedule.yaml, Confluence is no longer needed -- edit the
YAML directly (schedule-as-code) and let the `oncall` binary read it.

Usage:
    ./confluence_import.py > schedule.yaml
    ./confluence_import.py -o schedule.yaml

Configuration comes from the environment (or a local, gitignored .env file --
see ../.env.example). Nothing is hard-coded:

    CONFLUENCE_BASE        e.g. https://confluence.example.com   (required)
    CONFLUENCE_SPACE       Confluence space key                  (required)
    CONFLUENCE_USER        login for basic auth                  (required)
    ONCALL_PAGE_TITLE      exact title of the rotation page      (required)
    CONFLUENCE_PASSWORD    password/PAT for basic auth           (or gopass)
    CONFLUENCE_PASS_GOPASS gopass ref to read the password from  (optional)
    ONCALL_ENV             path to the .env file (default: ./.env)

Dependencies: Python stdlib + curl only. The corp proxy is stripped for the
curl calls (mirrors `env -u http_proxy ...`).
"""
import argparse
import html
import json
import os
import re
import subprocess
import sys


def load_dotenv():
    path = os.environ.get("ONCALL_ENV", ".env")
    try:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                k, _, v = line.partition("=")
                os.environ.setdefault(k.strip(), v.strip().strip('"').strip("'"))
    except OSError:
        pass


load_dotenv()

BASE = os.environ.get("CONFLUENCE_BASE")
SPACE = os.environ.get("CONFLUENCE_SPACE")
USERNAME = os.environ.get("CONFLUENCE_USER")
TITLE = os.environ.get("ONCALL_PAGE_TITLE")
GOPASS_REF = os.environ.get("CONFLUENCE_PASS_GOPASS")

JMONTHS = ["farvardin", "ordibehesht", "khordad", "tir", "mordad", "shahrivar",
           "mehr", "aban", "azar", "dey", "bahman", "esfand"]
JMONTH_ALIASES = {"farv": "farvardin", "ordi": "ordibehesht"}


def sh(args, timeout=40):
    env = {k: v for k, v in os.environ.items()
           if k.lower() not in {"http_proxy", "https_proxy", "all_proxy"}}
    return subprocess.run(args, capture_output=True, text=True, env=env,
                          timeout=timeout).stdout


def require_config():
    missing = [n for n, v in (("CONFLUENCE_BASE", BASE), ("CONFLUENCE_SPACE", SPACE),
                              ("CONFLUENCE_USER", USERNAME),
                              ("ONCALL_PAGE_TITLE", TITLE)) if not v]
    if missing:
        sys.exit("Missing required config (env or .env): " + ", ".join(missing))


def get_password():
    pw = os.environ.get("CONFLUENCE_PASSWORD")
    if pw:
        return pw
    if GOPASS_REF:
        out = sh(["gopass", "show", "-o", GOPASS_REF]).strip()
        if out:
            return out
    sys.exit("No password: set $CONFLUENCE_PASSWORD or $CONFLUENCE_PASS_GOPASS.")


def api(pw, path):
    out = sh(["curl", "-sS", "-m", "35", "-u", f"{USERNAME}:{pw}", f"{BASE}{path}"])
    return json.loads(out)


def fetch_body(pw):
    from urllib.parse import quote
    path = (f"/rest/api/content?spaceKey={SPACE}&title={quote(TITLE)}"
            f"&expand=body.storage&limit=1")
    d = api(pw, path)
    if not d.get("results"):
        sys.exit("Page not found (auth failed or title changed).")
    return d["results"][0]["body"]["storage"]["value"]


def clean(s):
    s = re.sub(r"<[^>]+>", " ", s)
    return re.sub(r"\s+", " ", html.unescape(s)).strip()


def parse_month(tok):
    tok = tok.strip().lower()
    tok = JMONTH_ALIASES.get(tok, tok)
    for i, m in enumerate(JMONTHS, 1):
        if m.startswith(tok) or tok.startswith(m):
            return i
    return None


def parse_shift_dates(text, year):
    text = clean(text)
    parts = re.split(r"[-–]", text)
    if len(parts) != 2:
        return None

    def one(p, default_month):
        m = re.match(r"(\d+)\s*([A-Za-z]+)?", p.strip())
        if not m:
            return None
        day = int(m.group(1))
        mon = parse_month(m.group(2)) if m.group(2) else default_month
        return day, mon

    r = one(parts[1], None)
    if not r or r[1] is None:
        return None
    rday, rmon = r
    l = one(parts[0], rmon)
    if not l:
        return None
    lday, lmon = l
    sy = ey = year
    if lmon and rmon and rmon < lmon:
        ey = year + 1
    return (sy, lmon, lday), (ey, rmon, rday)


def userkeys(name_cell):
    keys = re.findall(r'ri:userkey="([0-9a-f]+)"', name_cell)
    if not keys:
        return [], None
    handover = ">>" in html.unescape(name_cell)
    if handover:
        return [keys[-1]], keys[0] if len(keys) > 1 else None
    return [keys[0]], None


def parse_shift_dates_title_start(title):
    m = re.match(r"\s*\d+\s*([A-Za-z]+)", clean(title))
    return parse_month(m.group(1)) if m else None


def build_rotations(body):
    heads = list(re.finditer(r"Oncall rotation \(([^<)]*)\)", body))
    tables = [(mt.start(), mt.group(0))
              for mt in re.finditer(r"<table.*?</table>", body, re.S)]

    secs = []
    for i, m in enumerate(heads):
        title = m.group(1)
        nxt = heads[i + 1].start() if i + 1 < len(heads) else len(body)
        tbl = next((t for p, t in tables if m.start() < p < nxt), None)
        sm = parse_shift_dates_title_start(title)
        if sm is None and "nowruz" in title.lower():
            sm = 1
        secs.append([title, sm, tbl,
                     (int(re.search(r"\b(\d{4})\b", title).group(1))
                      if re.search(r"\b(\d{4})\b", title) else None)])

    years = [s[3] for s in secs]
    anchors = [i for i, y in enumerate(years) if y is not None]
    if not anchors:
        return
    for start in anchors:
        for i in range(start + 1, len(secs)):
            if years[i] is not None:
                break
            pm, prev = secs[i][1], secs[i - 1][1]
            years[i] = years[i - 1] + (1 if pm and prev and pm < prev else 0)
    for i in range(anchors[0] - 1, -1, -1):
        pm, nxt = secs[i + 1][1], secs[i][1]
        years[i] = years[i + 1] - (1 if pm and nxt and pm < nxt else 0)

    for (title, sm, tbl, _), year in zip(secs, years):
        if tbl is None or year is None:
            continue
        rows = []
        for tr in re.findall(r"<tr.*?</tr>", tbl, re.S):
            cells = re.findall(r"<t[dh][^>]*>(.*?)</t[dh]>", tr, re.S)
            if len(cells) >= 3 and clean(cells[0]).isdigit():
                rows.append((cells[1], cells[2]))
        yield title, year, rows


def yaml_scalar(s):
    """Quote a YAML scalar when needed."""
    if s == "" or re.search(r'[:#\[\]{}",&*!|>%@`]', s) or s != s.strip():
        return '"' + s.replace('\\', '\\\\').replace('"', '\\"') + '"'
    return s


def jdate(t):
    return f"{t[0]:04d}-{t[1]:02d}-{t[2]:02d}"


def main():
    ap = argparse.ArgumentParser(description="Confluence -> schedule.yaml importer")
    ap.add_argument("-o", "--out", help="output file (default: stdout)")
    a = ap.parse_args()

    require_config()
    pw = get_password()
    body = fetch_body(pw)

    name_cache = {}

    def resolve(k):
        if k not in name_cache:
            try:
                u = api(pw, f"/rest/api/user?key={k}")
                name_cache[k] = (u.get("username") or k, u.get("displayName") or "")
            except Exception:
                name_cache[k] = (k, "")
        return name_cache[k]

    people = {}   # id -> display name
    shifts = []   # dicts: start,end,person,rotation,handover_from

    def note_person(k):
        uid, disp = resolve(k)
        people.setdefault(uid, disp or uid)
        return uid

    for title, year, rows in build_rotations(body):
        rotation = clean(title)
        for name_cell, date_text in rows:
            dates = parse_shift_dates(date_text, year)
            if not dates:
                continue
            (s, e) = dates
            eff, handed_from = userkeys(name_cell)
            if not eff:
                continue
            person = note_person(eff[0])
            row = {"start": jdate(s), "end": jdate(e), "person": person,
                   "rotation": rotation}
            if handed_from:
                row["handover_from"] = note_person(handed_from)
            shifts.append(row)

    # emit YAML
    lines = ["# Generated by importer/confluence_import.py -- edit freely.",
             "# Dates are Jalali YYYY-MM-DD, ranges inclusive.", "", "people:"]
    for uid in sorted(people):
        lines.append(f"  {yaml_scalar(uid)}:")
        lines.append(f"    name: {yaml_scalar(people[uid] or uid)}")
    lines.append("")
    lines.append("shifts:")
    for sh_ in shifts:
        lines.append(f'  - start: "{sh_["start"]}"')
        lines.append(f'    end: "{sh_["end"]}"')
        lines.append(f"    person: {yaml_scalar(sh_['person'])}")
        if sh_.get("rotation"):
            lines.append(f"    rotation: {yaml_scalar(sh_['rotation'])}")
        if sh_.get("handover_from"):
            lines.append(f"    handover_from: {yaml_scalar(sh_['handover_from'])}")
    lines.append("")
    lines.append("overrides: []")
    lines.append("")
    text = "\n".join(lines)

    if a.out:
        with open(a.out, "w") as f:
            f.write(text)
        print(f"wrote {a.out} ({len(shifts)} shifts, {len(people)} people)",
              file=sys.stderr)
    else:
        sys.stdout.write(text)


if __name__ == "__main__":
    main()
