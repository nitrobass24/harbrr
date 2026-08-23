#!/usr/bin/env python3
"""Compare GitHub Actions run timings — machine time, wall time, and per-step cost.

Machine time is the sum of every job's duration (what the fleet actually burns).
Wall time is first job start to last job finish (what you wait for). They move
independently: a change can cut machine time and leave wall time flat, or blow up
wall time purely by starving one job on the critical path of a runner.

Usage (RUN is a run id, optionally id@attempt, optionally id@attempt:label):

  # summarise runs side by side
  scripts/ci-timings.py summary 32584011627:main 32662468177@2:bun

  # per-job breakdown, slowest first
  scripts/ci-timings.py jobs 32662468177@2

  # when each job started relative to the run, to spot runner starvation
  scripts/ci-timings.py stagger 32662468177@2

  # cost of specific steps, summed across jobs — the low-variance measurement
  scripts/ci-timings.py steps --match install,corepack 32584011627 32662468177@2

  # one job's duration across many runs, to establish a noise band
  scripts/ci-timings.py band --jobs web,web-test,web-lint 32584011627 32542120274

  # how many jobs are in flight over the run
  scripts/ci-timings.py occupancy 32662468177@2

Defaults to the origin remote's repo; override with --repo owner/name.

Caveat: `occupancy` counts overlapping job windows, which is a LOWER BOUND on
runners busy — a runner is also occupied by setup and cleanup outside that
window. It has reported "peak 11 of 12" for a run whose `runner_name` data
showed all 12 engaged. When the exact count matters, use `stagger`'s runner
column.
"""
import argparse, json, subprocess, sys
from datetime import datetime

def ts(s): return datetime.strptime(s, "%Y-%m-%dT%H:%M:%SZ")

def default_repo():
    try:
        url = subprocess.check_output(["git", "remote", "get-url", "origin"], text=True).strip()
    except subprocess.CalledProcessError:
        sys.exit("no origin remote; pass --repo owner/name")
    return url.removesuffix(".git").split("github.com")[-1].lstrip(":/")

def parse(spec):
    """id[@attempt][:label] -> (id, attempt|None, label)"""
    head, _, label = spec.partition(":")
    rid, _, att = head.partition("@")
    return rid, (att or None), (label or head)

def fetch(repo, rid, att):
    path = f"/repos/{repo}/actions/runs/{rid}"
    path += f"/attempts/{att}/jobs" if att else "/jobs"
    out = subprocess.check_output(["gh", "api", f"{path}?per_page=100"], text=True)
    jobs = json.loads(out)["jobs"]
    # Skipped jobs have no duration and would drag the wall window sideways.
    return [j for j in jobs
            if j.get("started_at") and j.get("completed_at") and j["conclusion"] != "skipped"]

def durations(jobs):
    return [(j["name"], (ts(j["completed_at"]) - ts(j["started_at"])).total_seconds(), j["conclusion"])
            for j in jobs]

def totals(jobs):
    machine = sum(d for _, d, _ in durations(jobs))
    wall = (max(ts(j["completed_at"]) for j in jobs)
            - min(ts(j["started_at"]) for j in jobs)).total_seconds()
    return machine, wall

def cmd_summary(repo, specs, _a):
    print(f"{'run':<30}{'machine':>9}{'wall':>7}{'jobs':>6}  failed")
    for spec in specs:
        rid, att, label = parse(spec)
        jobs = fetch(repo, rid, att)
        machine, wall = totals(jobs)
        bad = [n for n, _, c in durations(jobs) if c != "success"]
        print(f"{label:<30}{machine:>9.0f}{wall:>7.0f}{len(jobs):>6}  {','.join(bad) or '-'}")
    print("\nmachine = sum of all job durations; wall = first start -> last finish")

def cmd_jobs(repo, specs, _a):
    for spec in specs:
        rid, att, label = parse(spec)
        jobs = fetch(repo, rid, att)
        machine, wall = totals(jobs)
        print(f"\n=== {label} ===  machine {machine:.0f}s  wall {wall:.0f}s")
        for n, d, c in sorted(durations(jobs), key=lambda r: -r[1]):
            flag = "" if c == "success" else f"  <- {c}"
            print(f"  {n:<34}{d:>6.0f}s{flag}")

def cmd_stagger(repo, specs, _a):
    """Start offsets expose queueing: a big start+ means the job waited for a runner,
    not that it ran slowly. If that job gates others via needs:, it sets wall time."""
    for spec in specs:
        rid, att, label = parse(spec)
        jobs = fetch(repo, rid, att)
        t0 = min(ts(j["started_at"]) for j in jobs)
        machine, wall = totals(jobs)
        print(f"\n=== {label} ===  machine {machine:.0f}s  wall {wall:.0f}s")
        print(f"  {'job':<34}{'start+':>8}{'dur':>6}{'end+':>7}  runner")
        for j in sorted(jobs, key=lambda j: ts(j["started_at"])):
            s, e = ts(j["started_at"]), ts(j["completed_at"])
            # runner_name is the field that distinguishes "fleet was full" from
            # "a free runner sat there and the job still wasn't dispatched".
            print(f"  {j['name']:<34}{(s-t0).total_seconds():>8.0f}"
                  f"{(e-s).total_seconds():>6.0f}{(e-t0).total_seconds():>7.0f}"
                  f"  {j.get('runner_name') or '-'}")

def cmd_steps(repo, specs, args):
    """Whole-job times swing with fleet load; individual steps barely move. If you
    want to prove a build-tooling change did something, measure the steps."""
    needles = [m.strip().lower() for m in args.match.split(",") if m.strip()]
    show_all = args.match.strip() in ("all", "*")
    only = [j.strip() for j in args.jobs.split(",")] if args.jobs else None
    for spec in specs:
        rid, att, label = parse(spec)
        jobs = fetch(repo, rid, att)
        print(f"\n=== {label} ===")
        total = 0.0
        for j in sorted(jobs, key=lambda j: j["name"]):
            if only and j["name"] not in only:
                continue
            for s in j.get("steps", []):
                if not s.get("completed_at") or not s.get("started_at"):
                    continue
                if not show_all and not any(n in s["name"].lower() for n in needles):
                    continue
                d = (ts(s["completed_at"]) - ts(s["started_at"])).total_seconds()
                total += d
                # Step names run long (they document themselves); clip so columns line up.
                print(f"  {j['name']:<22}{s['name'][:40]:<42}{d:>6.0f}s")
        print(f"  {'':<22}{'TOTAL':<34}{total:>6.0f}s")

def cmd_band(repo, specs, args):
    """Same jobs across several runs, so you can see the noise band before
    claiming a delta. A 3% change is unprovable if the band is +/-20%."""
    wanted = [j.strip() for j in args.jobs.split(",")] if args.jobs else None
    rows, labels = {}, []
    for spec in specs:
        rid, att, label = parse(spec)
        labels.append(label)
        for n, d, _ in durations(fetch(repo, rid, att)):
            if wanted and n not in wanted:
                continue
            rows.setdefault(n, {})[label] = d
    names = wanted if wanted else sorted(rows)
    w = max((len(n) for n in names), default=10) + 2
    print(f"{'job':<{w}}" + "".join(f"{l[:13]:>14}" for l in labels))
    for n in names:
        if n not in rows: continue
        print(f"{n:<{w}}" + "".join(f"{rows[n].get(l, float('nan')):>14.0f}" for l in labels))
    print("-" * (w + 14 * len(labels)))
    print(f"{'TOTAL':<{w}}" + "".join(
        f"{sum(rows[n].get(l, 0) for n in names if n in rows):>14.0f}" for l in labels))

COMMANDS = {"summary": cmd_summary, "jobs": cmd_jobs, "stagger": cmd_stagger,
            "steps": cmd_steps, "band": cmd_band}


def cmd_occupancy(repo, specs, args):
    """How many jobs are in flight, second by second. Wall time only improves from
    freeing a slot if the fleet is actually saturated -- if concurrency never reaches
    the runner count, a job that finishes earlier just idles a runner earlier.

    Lower bound only: a runner is busy with setup/cleanup outside the job window,
    so a "peak 11 of 12" here can still be all 12 engaged. Cross-check with stagger."""
    cap = args.runners
    for spec in specs:
        rid, att, label = parse(spec)
        jobs = fetch(repo, rid, att)
        t0 = min(ts(j["started_at"]) for j in jobs)
        span = int(max(ts(j["completed_at"]) for j in jobs).timestamp() - t0.timestamp())
        iv = [(ts(j["started_at"]).timestamp() - t0.timestamp(),
               ts(j["completed_at"]).timestamp() - t0.timestamp()) for j in jobs]
        conc = [sum(1 for a, b in iv if a <= t < b) for t in range(span + 1)]
        at_cap = sum(1 for c in conc if c >= cap)
        print(f"\n=== {label} ===  peak {max(conc)} of {cap} runners, "
              f"{at_cap}s at capacity ({100*at_cap/max(span,1):.0f}% of the run)")
        print("  (job windows only — a lower bound; cross-check with stagger's runner column)")
        step = max(1, span // 60)
        for t in range(0, span + 1, step):
            c = conc[t]
            bar = "#" * c + ("!" if c >= cap else "")
            print(f"  t+{t:>4}s {c:>3} {bar}")

COMMANDS["occupancy"] = cmd_occupancy

def main():
    p = argparse.ArgumentParser(description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("command", choices=COMMANDS)
    p.add_argument("runs", nargs="+", metavar="RUN", help="id[@attempt][:label]")
    p.add_argument("--repo", default=None, help="owner/name (default: origin)")
    p.add_argument("--match", default="install", help="steps: substrings, comma-separated, or 'all'")
    p.add_argument("--runners", type=int, default=12, help="occupancy: fleet size")
    p.add_argument("--jobs", default=None, help="band/steps: job names, comma-separated")
    a = p.parse_args()
    COMMANDS[a.command](a.repo or default_repo(), a.runs, a)

if __name__ == "__main__":
    main()
