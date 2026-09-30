// browser behaviour htmx has no attribute for: focus around the run dialog and the inspector, closing the
// inspector without a request (it must work while polls fail), and marking a failed poll

// uiRestoreFocus focuses the job's row button, re-found by id because polling replaces the opener. A row that
// is gone (filtered out, removed) or covered by the inspector falls back to the inspector heading, then search
function uiRestoreFocus(jobID) {
    const row = jobID && document.querySelector('[data-job-id="' + CSS.escape(jobID) + '"] .job-open');
    const usable = row && !row.closest('[inert]') ? row : null;
    const target = usable || document.querySelector('#inspector [data-focus]') || document.getElementById('search');
    if (target) target.focus();
}

// uiDialogSwapped opens a dialog swapped into the slot, or restores focus when an accepted run emptied it;
// emptying the slot removes an open dialog without firing its close event. It runs after settle because
// htmx binds the dialog's hx-on:close only then, and a dialog closed before that stays in the slot
function uiDialogSwapped(slot) {
    const dlg = slot.querySelector('dialog');
    if (!dlg) {
        uiRestoreFocus(slot.dataset.jobId);
        slot.dataset.jobId = '';
        return;
    }
    slot.dataset.jobId = dlg.dataset.jobId || '';
    if (!dlg.open) dlg.showModal();
}

// uiDialogClosed removes a dialog closed by Esc or Cancel and returns focus to its job
function uiDialogClosed(dlg) {
    const slot = dlg.parentElement;
    const jobID = dlg.dataset.jobId;
    dlg.remove();
    if (slot) slot.dataset.jobId = '';
    uiRestoreFocus(jobID);
}

// the page behind the inspector when it overlays (tablet drawer, phone full screen)
const uiCovered = ['.top', '#failing-alert', '#jobs-controls', '#jobs-container', '.footer'];

function uiSetCovered(covered) {
    uiCovered.forEach(function (sel) {
        const el = document.querySelector(sel);
        if (el) el.inert = covered;
    });
}

// uiSyncCovered marks the page inert while the inspector overlays it (the CSS decides by width, so this runs on
// open and on every resize). Focus left on a control that just became covered moves to the inspector heading
function uiSyncCovered() {
    const insp = document.querySelector('#inspector .insp');
    const covered = !!insp && getComputedStyle(insp).position === 'fixed';
    const active = document.activeElement;
    uiSetCovered(covered);
    if (!covered || !active || active === document.body || insp.contains(active) || active.closest('dialog')) return;
    const heading = insp.querySelector('[data-focus]');
    if (heading) heading.focus();
}

let uiResizePending = false;
window.addEventListener('resize', function () {
    if (uiResizePending) return;
    uiResizePending = true;
    requestAnimationFrame(function () {
        uiResizePending = false;
        uiSyncCovered();
    });
});

// uiInspectorSwapped moves focus into a newly opened inspector, or back to search when a removed job closed it;
// its own polling swaps are ignored
function uiInspectorSwapped(panel, evt) {
    if (evt.detail.target !== panel) return;
    uiSyncCovered();
    const heading = panel.querySelector('[data-focus]');
    if (heading) {
        heading.focus();
        return;
    }
    if (!document.activeElement || document.activeElement === document.body) uiRestoreFocus('');
}

// uiCloseInspector empties the inspector and clears the selection locally; the next poll sends the cleared ids
function uiCloseInspector() {
    const selected = document.getElementById('selected-job');
    const jobID = selected ? selected.value : '';
    document.getElementById('inspector').innerHTML = '';
    uiSetCovered(false);
    if (selected) selected.value = '';
    const run = document.getElementById('selected-run');
    if (run) run.value = '';
    document.querySelectorAll('tr.row.sel').forEach(function (row) { row.classList.remove('sel'); });
    uiRestoreFocus(jobID);
}

// uiPollDone marks the page when the table poll itself failed; requests from controls inside bubble here too
function uiPollDone(container, evt) {
    if (evt.detail.requestConfig.elt !== container) return;
    const app = container.closest('.app');
    if (app) app.classList.toggle('poll-failed', !evt.detail.successful);
}
