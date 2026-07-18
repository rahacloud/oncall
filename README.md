# oncall

On-call rotation reporting as code. The schedule lives in a version-controlled YAML file (`schedule.yaml`) that is the **system of record** — it replaces editing an on-call table in Confluence. A small Go CLI reads that file and tells you who was on call, exports it, and tallies days per person (splitting working days from Iranian public holidays).

Confluence is only involved once, as a migration source: `importer/` reads an existing rotation page and writes the initial `schedule.yaml`. After that, edit the YAML directly and review changes via pull request.

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
| `count START END` | Per-person day tally, split into working / holiday. |

Flags: `--schedule PATH` (or `$ONCALL_SCHEDULE`, default `schedule.yaml`) and
`--holidays PATH` (or `$ONCALL_HOLIDAYS`) to enable holiday classification.

## Holidays

Holidays are read from a **local file** you provide — there is no network
lookup. Without `--holidays`, classification is off and every day counts as a
working day. See [`holidays.example.yaml`](holidays.example.yaml):

```yaml
weekends: [Friday]        # recurring weekly non-working days
dates:                    # specific Jalali dates -> name (name optional)
  "1405-01-01": Nowruz
  "1405-01-12": Islamic Republic Day
```

A day is a holiday if its weekday is in `weekends` or its date is in `dates`.
Solar-calendar holidays are fixed; lunar (Islamic) holidays move each year, so
fill them in per year.

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

## Web service

`oncall serve` starts an HTTP server with a small web UI, a JSON API, and an ICS
calendar feed over the same `schedule.yaml`.

```bash
ONCALL_TOKEN=$(openssl rand -hex 16) \
  ./oncall serve --schedule schedule.yaml --addr :8080
# open http://localhost:8080
```

| Method & path | Auth | Purpose |
| --- | --- | --- |
| `GET /` | — | web UI |
| `GET /healthz` | — | liveness/readiness |
| `GET /api/current[.txt]` | — | who's on call now (or `?date=1405/4/14`) |
| `GET /api/range?start=&end=` | — | per-day resolution (JSON) |
| `GET /api/count?start=&end=` | — | per-person tally (working vs holiday) |
| `GET /api/schedule` | — | full schedule |
| `GET /calendar.ics` | — | subscribable calendar (one all-day event per shift) |
| `POST /api/overrides` | Bearer | add a swap `{start,end,person,note}` |
| `DELETE /api/overrides/{index}` | Bearer | remove a swap |
| `POST /api/shifts` | Bearer | add a shift |
| `PUT /api/people/{id}` | Bearer | upsert a person |

Mutations require `Authorization: Bearer $ONCALL_TOKEN` and persist back to
`schedule.yaml`. If `ONCALL_TOKEN` is unset the service is **read-only** (writes
return `403`). Env: `ONCALL_ADDR` (default `:8080`), `ONCALL_SCHEDULE`,
`ONCALL_HOLIDAYS`.

Slack "who's on call" is a one-liner against the text endpoint:

```bash
curl -s "$ONCALL_URL/api/current.txt"
```

## Deploying

```bash
# container (published to ghcr.io/rahacloud/oncall by CI)
docker build -t oncall .
docker run -p 8080:8080 -v "$PWD/schedule.yaml:/data/schedule.yaml:ro" oncall

# rahacloud / Kubernetes via Helm
helm upgrade --install oncall deploy/helm/oncall \
  --set-file schedule=schedule.yaml \
  --set ingress.enabled=true --set ingress.host=oncall.rahacloud.ir
```

The schedule is mounted from a ConfigMap (read-only, GitOps-friendly — render
your `schedule.yaml` into `existingConfigMap`). To enable the write API, set
`token` and `persistence.enabled=true` so the schedule lives on a writable volume.

## Bring your own data (build on the official image)

The published image ships **no schedule or holidays** — those are yours. Keep
them in your own (private) repo and build a personal image `FROM` the official
one. The base image's entrypoint is already `oncall serve`, and it looks for
`/data/schedule.yaml` and `/data/holidays.yaml`:

```dockerfile
# your-team/oncall-deploy/Dockerfile
FROM ghcr.io/rahacloud/oncall:latest
COPY schedule.yaml  /data/schedule.yaml
COPY holidays.yaml  /data/holidays.yaml   # optional; omit for no holidays
```

```bash
docker build -t registry.example.com/team/oncall .
docker push  registry.example.com/team/oncall
```

Your data stays in your registry/repo; you only depend on the upstream binary.
This is the recommended pattern for private rotations — no forking, and you pick
up new upstream releases by rebuilding. (Equivalently, mount the two files as
volumes or a ConfigMap instead of baking them in.)

## Roadmap

- [x] **Phase 1 — own the data:** schedule-as-code YAML, `show`/`csv`/`count`, Confluence importer.
- [x] **Phase 2 — read API + UI:** HTTP service ("who's on call now / for a range"), holidays, counts, web page.
- [x] **Phase 3 — management:** add/remove overrides & shifts with a Bearer token — replaces editing in Confluence.
- [x] **Phase 4 — integrations:** ICS calendar feed, Slack `current.txt`, Docker image, Helm chart for rahacloud.

## Development

```bash
go vet ./...
go build ./...
go test ./...
```

## License

MIT — see [LICENSE](LICENSE).
