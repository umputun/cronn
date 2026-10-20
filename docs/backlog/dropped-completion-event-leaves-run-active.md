---
worth: maybe
where: app/web/jobs.go:OnJobComplete
added: 2026-10-01
---
# dropped completion event leaves a run active forever

`OnJobComplete` sends to `eventChan` without blocking and drops the event with a warning when the
channel is full. The run is then never recorded, its entry in `Server.active` never clears, the job keeps
showing running, and the inspector's live output for that run polls every 5s until the process restarts
instead of turning into the recorded run. Start events are dropped the same way, which only loses the
live view.

Surfaced by Copilot on PR #72 (live output). Deferred there: the drop predates that PR, and a full channel
needs 1000 events queued faster than `processEvents` drains them, which has not been observed. Options
are a blocking send with a timeout for completion events only, or a larger buffer; either changes how a
stalled web server can hold back the scheduler, which is the decision to make first.
