#!/usr/bin/env python3
"""generate the cronn redesign proposal page and a width-test harness from one job list."""
from html import escape as e
from pathlib import Path

HERE = Path(__file__).parent
OUT = HERE.parent / "shots"
DASH_CSS = (HERE / "dash.css").read_text()
PAGE_CSS = (HERE / "page.css").read_text()

JOBS = [
    dict(st="fail", name="Vendor feed import", cmd="sh -c 'echo fetching; sleep 2; echo boom >&2; exit 3'",
         sh="Every minute", cron="*/1 * * * *", last="1m ago", meta="exit 3 · 2.0s", bad=True, nxt="in 8s", nabs="16:53", sel=True),
    dict(st="ok", name="Quote sync", cmd="echo sync ok {{.YYYYMMDD}}", sh="Every minute", cron="*/1 * * * *",
         last="1m ago", meta="exit 0 · 0.1s", nxt="in 8s", nabs="16:53"),
    dict(st="running", name="Report rebuild", cmd="sh -c 'echo long job; sleep 100'", sh="Every 2 minutes", cron="*/2 * * * *",
         running="0:47", nxt="in 1m", nabs="16:54"),
    dict(st="ok", name="Business hours sync", cmd="rsync -a /srv/files remote:/srv/files", sh=":00 and :30, 09–17, Mon–Fri",
         cron="0,30 9-17 * * 1-5", last="22m ago", meta="exit 0 · 38s", nxt="in 8m", nabs="17:00"),
    dict(st="ok", name="Health check", cmd="curl -fsS http://localhost:8080/ping", sh="Hourly", cron="@hourly",
         last="52m ago", meta="exit 0 · 0.2s", nxt="in 8m", nabs="17:00"),
    dict(st="never", name=None, cmd="sh -c 'echo eod {{.YYYYMMDDEOD}}'", sh="Hourly at :15", cron="15 * * * *",
         nxt="in 23m", nabs="17:15"),
    dict(st="never", name="Search reindex", cmd="reindex --full", sh="Every 1h 15m", cron="@every 1h15m",
         nxt="in 1h 12m", nabs="18:04"),
    dict(st="ok", name="Nightly backup", cmd="backup /data --target s3://bk/nightly", sh="Daily at 02:00", cron="0 2 * * *",
         last="14h ago", meta="exit 0 · 4m 12s", nxt="in 9h", nabs="Sep 30 02:00"),
    dict(st="off", name="Cache cleanup", cmd="find /tmp/cache -mtime +7 -delete", sh="Daily at 00:00", cron="@midnight",
         last="3d ago", meta="exit 0 · 1.4s", nxt="—", nabs="disabled", off=True),
]

LOGO = ('<svg width="20" height="20" viewBox="0 0 24 24" fill="none"><circle cx="12" cy="12" r="9.5" stroke="var(--accent)" '
        'stroke-width="2"/><path d="M12 7v5l3.5 2" stroke="var(--accent)" stroke-width="2" stroke-linecap="round"/></svg>')


def row(j, sel=True):
    cls = "row" + (" sel" if sel and j.get("sel") else "") + (" off" if j.get("off") else "")
    if j["name"]:
        name = f'<div class="name"><span>{e(j["name"])}</span>'
        if j.get("off"):
            name += '<span class="tag off">disabled</span>'
        name += f'</div><div class="cmd">{e(j["cmd"])}</div>'
    else:
        name = f'<div class="name unnamed"><span>{e(j["cmd"])}</span></div>'
    fold = f'<div class="fold-sched">{e(j["sh"])} · <span class="mono">{e(j["cron"])}</span></div>'
    bad = " bad" if j.get("bad") else ""
    if j.get("running"):
        last = f'<span class="elapsed">running {j["running"]}</span>'
        tlast = f'<span class="elapsed">running {j["running"]}</span>'
    elif j.get("last"):
        last = f'{j["last"]}<span class="meta{bad}">{e(j["meta"])}</span>'
        code = j["meta"].split(" · ")[0]
        tlast = f'{j["last"]} · <span class="mono{bad}">{code}</span>'
    else:
        last = '<span class="never-txt">never ran</span>'
        tlast = '<span class="never-txt">never ran</span>'
    nxt = "disabled" if j.get("off") else j["nxt"]
    if j.get("off"):
        act = '<span class="btn ghost">Enable</span>'
    else:
        dis = " dis" if j.get("running") else ""
        act = f'<span class="btn{dis}" title="Run now">▶<span class="lbl"> Run</span></span>'
    return (f'<tr class="{cls}"><td class="c-st"><span class="dot {j["st"]}"></span></td>'
            f'<td class="c-job">{name}{fold}</td>'
            f'<td class="c-sched">{e(j["sh"])}<span class="mono">{e(j["cron"])}</span></td>'
            f'<td class="c-last">{last}</td>'
            f'<td class="c-next">{j["nxt"]}<small>{j["nabs"]}</small></td>'
            f'<td class="c-time"><div><span class="k">Last</span>{tlast}</div><div><span class="k">Next</span>{nxt}</div></td>'
            f'<td class="c-act">{act}<span class="more">⋯</span></td></tr>')


def table(jobs, sel=True):
    head = ('<thead><tr><th class="c-st"></th><th class="c-job">Job</th><th class="c-sched">Schedule</th><th class="c-last">Last run</th><th class="c-next sorted">Next</th>'
            '<th class="c-time">When</th><th></th></tr></thead>')
    return f'<table class="jobs">{head}<tbody>{"".join(row(j, sel) for j in jobs)}</tbody></table>'


def topbar(fresh="updated |3s ago", fresh_bad=False, search=None):
    s = (f'<div class="search filled">{e(search)}<kbd>esc</kbd></div>' if search
         else '<div class="search">⌕&nbsp; Filter by name or command <kbd>/</kbd></div>')
    fb = " bad" if fresh_bad else ""
    a, b = fresh.split("|")
    fresh_html = f'<span class="fl">{e(a)}</span>{e(b)}' if not fresh_bad else f'{e(a)}<span class="fl"> {e(b)}</span>'
    return (f'<div class="top"><div class="brand">{LOGO}<span class="bt">cronn</span></div><span class="host">bigmac</span>{s}'
            f'<div class="fresh{fb}">{fresh_html}</div><div class="iconbtn theme" title="Theme">☾</div><div class="iconbtn">⋯</div></div>')


def tabs(on="All"):
    items = [("All", "9", ""), ("Failed", "1", "background:var(--fail)"), ("Running", "1", "background:var(--run)"),
             ("Succeeded", "4", "background:var(--ok)"), ("Never ran", "2", "border:1.5px solid var(--never)"),
             ("Disabled", "1", "border:1.5px dashed var(--off)")]
    out = []
    for label, n, style in items:
        d = f'<span class="d" style="{style}"></span>' if style else ""
        out.append(f'<div class="tab{" on" if label == on else ""}">{d}{label} <em>{n}</em></div>')
    return f'<div class="tabs">{"".join(out)}</div>'


def bar(count="9 jobs · select a row to inspect"):
    return f'<div class="bar">{tabs()}<span class="count">{count}</span><div class="select">Sort<b>Next run</b>▾</div></div>'


ALERT = ('<div class="alert"><span class="dot fail"></span><strong>1 job failing</strong> Vendor feed import '
         '<span class="mono">exit 3 · 1m ago</span><span class="go">Inspect →</span></div>')

TOASTS = ('<div class="toast">'
          '<div class="t"><span class="spin"></span>Sending run request: <b>Health check</b></div>'
          '<div class="t"><span class="dot ok"></span>Run request accepted: <b>Quote sync</b></div>'
          '<div class="t"><span class="dot fail"></span>Not started: <b>Report rebuild</b> <span class="mono">Job already running</span></div>'
          '</div>')

INSPECTOR = ('<aside class="insp">'
             '<div class="insp-back">‹ Jobs <span>· Vendor feed import</span></div>'
             '<div class="insp-h"><div class="row2"><span class="dot fail"></span><h3>Vendor feed import</h3><span class="x">×</span></div>'
             '<div class="chips"><span class="chip fail">Last run failed · exit 3</span><span class="chip on">Enabled</span>'
             '<span class="chip">Every minute · <span class="mono">*/1 * * * *</span></span></div>'
             '<div class="cmdlbl">Job command</div>'
             "<div class=\"cmdbox\">sh -c 'echo fetching; sleep 2; echo boom &gt;&amp;2; exit 3'<span class=\"copy\">copy</span></div>"
             '<div class="actions"><span class="btn primary">▶ Run now…</span><span class="btn">Disable</span></div></div>'
             '<div class="kv"><div><span>Last run</span>16:52:00 · 1m ago</div><div><span>Next run</span>16:53:00 · in 8s</div></div>'
             '<div class="runs-h">Runs <small>newest first · latest 50 shown</small></div><div class="runs">'
             '<div class="run"><span class="dot fail"></span>Sep 29 16:52:00<span>2.0s</span><span class="ex bad">exit 3</span></div>'
             '<div class="run"><span class="dot fail"></span>Sep 29 16:51:00<span>2.0s</span><span class="ex bad">exit 3</span></div>'
             '<div class="run sel"><span class="dot ok"></span><span>Sep 29 16:50:12 <span class="tag man">manual</span></span><span>1.9s</span><span class="ex">exit 0</span></div>'
             '<div class="run"><span class="dot fail"></span>Sep 29 16:50:00<span>2.1s</span><span class="ex bad">exit 3</span></div>'
             '<div class="run"><span class="dot ok"></span>Sep 29 16:49:00<span>2.0s</span><span class="ex">exit 0</span></div></div>'
             '<div class="out"><div class="out-h"><b>Run 16:50:12</b><span>manual</span><span class="r">exit 0 · 1.9s</span></div>'
             "<div class=\"out-cmd\"><b>Executed:</b> sh -c 'echo fetching; sleep 2; exit 0'</div>"
             '<pre>fetching\nfeed: 1,204 rows from https://vendor.example.com/export/daily?format=csv&amp;since=2026-09-29T16:50:00Z</pre></div>'
             '</aside>')


def app(insp=False, alert=True, toasts=False, fresh="updated |3s ago", fresh_bad=False, extra_cls=""):
    cls = "app" + (" has-insp" if insp else "") + (f" {extra_cls}" if extra_cls else "")
    work = f'<div>{table(JOBS, sel=insp)}{TOASTS if toasts else ""}</div>'
    if insp:
        work += '<div class="scrim"></div>' + INSPECTOR
    return (f'<div class="{cls}">{topbar(fresh, fresh_bad)}{ALERT if alert else ""}{bar()}'
            f'<div class="work">{work}</div></div>')


def runform(sheet=False, focus=False):
    cmd = ("backup /data --target s3://bk/adhoc-{{.YYYYMMDD}} --exclude /data/tmp --exclude /data/cache "
           "--bandwidth-limit 40M --notify ops@example.com")
    grab = '<div class="grab"></div>' if sheet else ""
    date_cls = "field focus" if focus else "field"
    return (f'<div class="runform">{grab}<div class="rf-h"><h4>Run “Nightly backup” now</h4>'
            '<p>Requests a one-time run. The schedule and crontab stay as they are.</p></div>'
            '<div class="rf-b">'
            f'<div><div class="lbl">Command <small>edited, this run only</small></div><div class="field edited">{e(cmd)}</div></div>'
            f'<div><div class="lbl">Date for templates</div><div class="{date_cls}">20260915</div>'
            '<div class="hint">Empty uses the current time. When a date is supplied, EOD templates resolve to the business day before it.</div></div>'
            '<div class="dlg-err">Not started: Job already running. Your edits are kept.</div>'
            '</div><div class="rf-f"><span class="btn ghost">Cancel</span><span class="btn primary">▶ Run once</span></div></div>')


def frame(inner, label, width):
    return (f'<div class="frame"><div class="chrome"><i></i><i></i><i></i><span>bigmac:9095 / cronn</span>'
            f'<span class="w">{e(label)} · {width}</span></div>{inner}</div>')


def keyboard():
    rows = ['<div>' + '<i></i>' * 10 + '</div>', '<div>' + '<i></i>' * 9 + '</div>',
            '<div>' + '<i></i>' * 9 + '</div>', '<div class="wide"><i style="flex:1"></i><i style="flex:4"></i><i style="flex:1"></i></div>']
    return '<div class="keyboard">' + "".join(rows) + '</div>'


ZERO = ('<div class="app"><div class="top"><div class="brand">' + LOGO + '<span class="bt">cronn</span></div><span class="host">bigmac</span>'
        '<div class="search filled">rsync nightly<kbd>esc</kbd></div></div>'
        '<div class="bar"><div class="tabs"><div class="tab">All <em>9</em></div><div class="tab on">'
        '<span class="d" style="background:var(--fail)"></span>Failed <em>1</em></div></div><span class="count">0 of 9 match</span></div>'
        '<div class="zero"><b>No failed jobs match “rsync nightly”</b>1 failed job is hidden by the search.'
        '<div class="acts"><span class="btn">Clear search</span><span class="btn ghost">Show all jobs</span></div></div></div>')

SILENT = ('<div class="app"><div class="out" style="margin:14px"><div class="out-h"><b>Run 03:10:00</b><span>scheduled</span>'
          '<span class="r bad">exit 137 · 30m 0s</span></div>'
          '<div class="out-cmd"><b>Job command:</b> pg_dump main | gzip &gt; /backups/main.sql.gz</div>'
          '<div class="empty-out">No output captured. The run failed with exit code 137.</div></div></div>')

BODY = f"""
<div class="doc">
<h1>Cronn dashboard: redesign proposal</h1>
<p class="lede">The dashboard shows a grid of raw cron expressions and clipped commands, and splits "why did this job fail?" across three views that don't connect well. The proposal is one responsive job list plus an inspector: find the failing job, see exactly which command it is, read its exit code and output, rerun it, and see whether the request was accepted or rejected, on a wide monitor, a laptop, a tablet or a phone.</p>

<h2>Today</h2>
<div class="before">
  <figure><img src="cards.png" alt="current card view"><figcaption>Card view: the stats block takes 190px for six values, each card leads with its cron expression, and 7 of 9 jobs get an amber marker that reads as a warning only because they have not run yet.</figcaption></figure>
  <figure><img src="history.png" alt="history dialog"><figcaption>History is a separate dialog opened from an unlabelled clock icon. It shows no exit code.</figcaption></figure>
  <figure><img src="mobile.png" alt="current phone layout" style="max-height:420px;object-fit:cover;object-position:top"><figcaption>On a phone the header wraps to three rows and the table scrolls sideways, hiding times and actions.</figcaption></figure>
</div>

<h2>Five problems, ranked</h2>
<div class="problems">
  <div class="problem"><b>Diagnosis is split across three views</b><p>Details, history and logs are separate dialogs. Details is a dead end: no history link, no actions. Exit code appears only in logs, and logs are unreachable for a run with no output.</p><div class="ref">partials/jobs.html:375-638</div></div>
  <div class="problem"><b>Jobs have no readable identity</b><p>YAML <code>name</code> is parsed but never reaches the web layer. Cards lead with <code>*/1 * * * *</code> and a command cut to 60 chars. Job details open from a small (i) icon.</p><div class="ref">crontab.go:57 · sqlite.go:22 · jobs.html:108</div></div>
  <div class="problem"><b>Run now doesn't report rejections</b><p>The dialog closes before the POST, which uses <code>hx-swap="none"</code>. Server rejections (already running, disabled, busy, an impossible date like 20260231) show nothing. Acceptance only refreshes the list.</p><div class="ref">app.js:61-115 · handlers.go:570-646</div></div>
  <div class="problem"><b>State markers mislead</b><p>"Never ran" is drawn in amber and reads as a warning. A disabled job is only dimmed and keeps its Success badge, so a job that will not run looks healthy. There is no disabled count or filter.</p><div class="ref">style.css:651 · jobs.go:356-383</div></div>
  <div class="problem"><b>Controls cycle without showing choices</b><p>Sort and filter buttons step through 3 and 5 modes, one per click. Under search the counts stay global, no match count is shown, zero matches leave a blank area, and "Next run" doesn't name the job.</p><div class="ref">jobs.html:240-274 · handlers.go:50-107</div></div>
</div>
<p class="note">Also found but not ranked: dialogs have no focus handling, nothing shows when the 5s refresh fails, and timestamps have no time zone.</p>

<h2>Wide screen: table with the inspector docked</h2>
{frame(app(insp=True, toasts=True), "wide", "1440px")}
<p class="cap">The inspector docks beside the table only while both fit (dashboard width 1180px and up). The selected run is a manual one, so the panel shows the command it actually executed next to its output. Scheduled runs don't record their resolved command today; for those the panel shows the job command under that label. Long output scrolls sideways inside its own box, never the page.</p>

<h2>Laptop and tablet: inspector slides over the table</h2>
<div class="pair">
  <div>{frame(app(fresh="refresh failed|· retrying", fresh_bad=True), "tablet", "820px")}<p class="cap">Below 1180px the schedule moves under the command and Last/Next share one labelled column; rows grow instead of truncating the schedule. Every control is 44px for touch. The header shows when a refresh fails instead of silently keeping stale data.</p></div>
  <div>{frame(app(insp=True), "tablet", "820px")}<p class="cap">The inspector overlays the table (a pushed table would be ~350px wide), takes the screen height and scrolls on its own; its Back bar stays at the top. Clicking Run or ⋯ on a row does not open the inspector.</p></div>
</div>

<h2>Phone</h2>
<div class="phones">
  <div><div class="phone">{app()}</div><p class="cap phone-cap">Search stays visible, filters wrap, each job is a two-line row with labelled Last/Next and a 44px Run button.</p></div>
  <div><div class="phone">{app(insp=True, alert=False)}</div><p class="cap phone-cap">Tapping a row opens the inspector full screen with a Back bar. Commands wrap; output scrolls sideways inside its box.</p></div>
  <div><div class="phone kb"><div class="under">{app(alert=False)}</div><div class="sheet">{runform(sheet=True, focus=True)}</div>{keyboard()}</div><p class="cap phone-cap">Run now is the same form shown as a bottom sheet. With the keyboard up, the fields scroll and Cancel/Run stay visible above it.</p></div>
</div>

<h2>Run now dialog, edge states, light theme</h2>
<div class="edges">
  <div>{runform()}<p class="cap">The same form as a dialog. A server rejection keeps it open with the edits; acceptance closes it and shows the toast.</p></div>
  <div><div class="card">{ZERO}</div><p class="cap">Search plus a status filter with no matches says what is hiding the jobs and offers a reset.</p></div>
  <div><div class="card">{SILENT}</div><p class="cap">A failed run that printed nothing still shows its exit code and duration.</p></div>
  <div><div class="card light">{app(alert=False, extra_cls="")}</div><p class="cap">Light theme, same tokens, at a narrow width.</p></div>
</div>

<h2>What it costs</h2>
<table class="cost">
  <thead><tr><th>Change</th><th>Scope</th><th>What it needs</th></tr></thead>
  <tbody>
  <tr><td>Responsive job list: one template, three layouts</td><td><span class="lvl t">templates + CSS</span></td><td class="muted">Container queries on the dashboard width: docked inspector at 1180px and up, overlay below, full screen under 600px. 44px touch targets below 1180px. Card view either goes or stays as a compact alternative (open question).</td></tr>
  <tr><td>State markers, direct filter tabs, sort select, match count, empty state, refresh-failed notice</td><td><span class="lvl t">templates + CSS + JS</span></td><td class="muted">Tabs and the sort select post to the existing filter and sort endpoints. Match count is the length of the filtered list. Refresh failure uses htmx's error event.</td></tr>
  <tr><td>Run now feedback, dialog and bottom sheet</td><td><span class="lvl t">templates + JS</span></td><td class="muted">One form in two presentations. Show the handler's existing 202 or error text; keep the form open with the edits on a rejection. htmx doesn't swap 4xx/5xx responses by default, so this needs a small error handler. The toast says "accepted", never "started".</td></tr>
  <tr><td>One inspector: details, runs, output, actions</td><td><span class="lvl b">templates + handler + JS</span></td><td class="muted">The panel sits outside the polled table and refreshes on its own without replacing the selected output. The selected job and run IDs survive the table's 5s re-render. Focus moves into the drawer or sheet and back on close. Starts from the existing history handler, which already returns runs with output.</td></tr>
  <tr><td>Job names</td><td><span class="lvl b">backend, small</span></td><td class="muted">Carry crontab <code>name</code> into <code>JobInfo</code> and the jobs table (column migration, same pattern as existing ones). Search matches names too. Unnamed jobs show their command.</td></tr>
  <tr><td>Disabled as its own filter and count</td><td><span class="lvl b">backend, small</span></td><td class="muted">New filter mode. Disabled jobs drop out of the Failed/Succeeded/Never ran counts, which changes what those counts mean today.</td></tr>
  <tr><td>Last exit code and duration on each row</td><td><span class="lvl b">backend, small</span></td><td class="muted">New query for the latest run of every job; the store only offers per-job history today.</td></tr>
  <tr><td>Readable schedule ("Daily at 02:00")</td><td><span class="lvl b">new dependency</span></td><td class="muted"><code>github.com/lnquy/cron</code> v1.1.1 describes cron expressions; the raw expression stays underneath. It has no handling for <code>@every</code>/<code>@hourly</code>, so those few are formatted by hand.</td></tr>
  <tr><td>Deferred: last-20 run strip, failure streak, success rate, "will run" preview, resolved command for scheduled runs</td><td><span class="lvl l">later</span></td><td class="muted">Each adds a query, a new endpoint or a stored field and is not needed to fix diagnosis and reruns.</td></tr>
  </tbody>
</table>
<p class="note">Each UI change also needs e2e Playwright tests under the repo rules, including runs at phone and tablet viewports; the existing e2e selectors (<code>.job-card</code>, <code>.history-btn</code>, <code>.btn-compact</code>, <code>.job-row</code>) change with the markup.</p>
<p class="note" id="verified">VERIFY_NOTE</p>
</div>
"""


def page(title, body):
    return (f'<!doctype html><html lang="en"><head><meta charset="utf-8">'
            f'<meta name="viewport" content="width=device-width, initial-scale=1"><title>{title}</title>'
            f'<style>{DASH_CSS}\n{PAGE_CSS}</style></head><body>{body}</body></html>')


def main():
    import sys
    note = sys.argv[1] if len(sys.argv) > 1 else ""
    (OUT / "proposal.html").write_text(page("Cronn Dashboard Redesign", BODY.replace("VERIFY_NOTE", e(note))))
    harness = app(toasts=True) + app(insp=True) + '<div style="padding:16px">' + runform() + "</div>"
    (OUT / "harness.html").write_text(page("Cronn Layout Harness", harness))


if __name__ == "__main__":
    main()
