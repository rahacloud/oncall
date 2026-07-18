# oncall

On-call rotation reporting as code. The schedule lives in a version-controlled
YAML file (`schedule.yaml`) that is the **system of record** — it replaces
editing an on-call table in Confluence. A small Go CLI reads that file and tells
you who was on call, exports it, and tallies days per person (splitting working
days from Iranian public holidays).

Confluence is only involved once, as a migration source: `importer/` reads an
existing rotation page and writes the initial `schedule.yaml`. After that, edit
the YAML directly and review changes via pull request.

> **Status:** Phase 1 — own the data. The CLI + schedule format + importer are
> here. A read-only HTTP API and a web UI are on the roadmap (see below).

## Quick start

```bash
go build -o oncall .

# try it against the bundled sample schedule
./oncall show  1405/3/21 1405/4/20 --schedule schedule.example.yaml
./oncall count 1405/3/21 1405/4/20 --schedule schedule.example.yaml
./oncall csv   1405/3/21 1405/4/20 --schedule schedule.example.yaml -o out.csv
```

Copy `schedule.example.yaml` to `schedule.yaml` (gitignored) and make it yours,
or generate it from Confluence (see [Importing](#importing-from-confluence)).

## Commands

Dates are Jalali (e.g. `1405/3/21`); ranges are inclusive. Default subcommand is
`show`, so `oncall 1405/3/21 1405/4/20` works too.

| Command | Output |
| --- | --- |
| `show START END` | Per-shift printout, grouped by rotation, with mid-window flags. |
| `csv START END [-o FILE]` | One row per day: Jalali + Gregorian date, weekday, person, shift, source, holiday flag/name, note. |
| `count START END` | Per-person day tally, split into working / holiday / unknown. |

Flags: `--schedule PATH` (or `$ONCALL_SCHEDULE`, default `schedule.yaml`) and
`--no-holidays` (skip the holiday lookup and count every day as working).

Holidays come from [holidayapi.ir](https://holidayapi.ir) and are cached under
`~/.cache/oncall/holidays.json`. Fridays and official holidays both count as
holidays.

## Schedule format

See [`schedule.example.yaml`](schedule.example.yaml). In short:

```yaml
people:
  ali.karimi: { name: Ali Karimi }
shifts:
  - start: "1405-03-16"   # inclusive Jalali range
    end:   "1405-03-22"
    person: ali.karimi
    rotation: "16 Khordad - 5 Tir"   # optional grouping label
    handover_from: reza.hosseini     # optional
overrides:                            # win over shifts on overlapping days
  - start: "1405-04-02"
    end:   "1405-04-03"
    person: sara.ahmadi
    note: "swap"
```

## Importing from Confluence

The importer is a one-shot migration. Configure it via a `.env` file (see
[`.env.example`](.env.example)), then:

```bash
cd importer
./confluence_import.py -o ../schedule.yaml
```

It resolves every shift to absolute Jalali dates and every user key to a
username, then writes the canonical YAML. The generated `schedule.yaml` is
gitignored because it contains real names — keep it in a private location if you
need to share it.

## Roadmap

- [x] **Phase 1 — own the data:** schedule-as-code YAML, `show`/`csv`/`count`, Confluence importer.
- [ ] **Phase 2 — read API + UI:** HTTP service ("who's on call now / for a range"), holidays, counts; minimal web page.
- [ ] **Phase 3 — management:** CRUD for rotations & overrides with auth — fully replaces editing in Confluence.
- [ ] **Phase 4 — integrations:** ICS calendar feed, Slack "current on-call", API tokens, deploy to rahacloud.

## Development

```bash
go vet ./...
go build ./...
go test ./...
```

## License

MIT — see [LICENSE](LICENSE).
