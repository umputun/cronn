// browser behaviour htmx has no attribute for: holding polls while a button is pressed, the shared blink
// phase, dialogs and focus, live log scrolling, tooltips, the clipboard, edited-command marking, menus,
// marking a failed poll, and the keyboard map

// a click needs press and release on the same element, and htmx cannot defer a swap, so every poll filter
// reads uiHeld and skips its tick while the primary button is down. htmx looks filter symbols up on window
window.uiHeld = false;
document.addEventListener('pointerdown', function (evt) {
    if (evt.button === 0) window.uiHeld = true;
}, true);
// a release outside the window or after a context menu never reaches the page
['pointerup', 'pointercancel', 'blur', 'contextmenu'].forEach(function (name) {
    window.addEventListener(name, function () { window.uiHeld = false; }, true);
});

// a CSS animation restarts on every swap, so running dots in freshly polled rows would blink out of phase
setInterval(function () { document.documentElement.classList.toggle('blink-off'); }, 600);

// Tab enables keyboard focus rings until pointer input. Esc alone must not give mouse-opened controls a ring
// when focus is restored or polling replaces them
document.documentElement.dataset.keynav = 'false';
document.addEventListener('keydown', function (evt) {
    if (evt.key === 'Tab') document.documentElement.dataset.keynav = 'true';
}, true);
document.addEventListener('pointerdown', function () {
    document.documentElement.dataset.keynav = 'false';
}, true);

// uiDialogSwapped opens a dialog swapped into the slot and remembers what opened it. It runs after settle
// because htmx binds the dialog's hx-on:close only then, and a dialog closed before that stays in the slot.
// A response that empties the slot (stop accepted, run gone) returns focus to the opener. The opener is
// the request's own element (a form's submit button), since Safari does not focus a clicked button
function uiDialogSwapped(slot, evt) {
    const dlg = slot.querySelector('dialog');
    if (!dlg) {
        uiRestoreFocus(slot.dataset.opener);
        slot.dataset.opener = '';
        return;
    }
    if (!slot.dataset.opener) {
        const cfg = evt && evt.detail && evt.detail.requestConfig;
        const src = cfg && ((cfg.triggeringEvent && cfg.triggeringEvent.submitter) || cfg.elt);
        const opener = src && src.id ? src : document.activeElement;
        if (opener && opener.id) slot.dataset.opener = opener.id;
    }
    if (!dlg.open) dlg.showModal();
}

// uiDialogClosed removes a dialog closed by Esc or Cancel and returns focus to its opener
function uiDialogClosed(dlg) {
    const slot = dlg.parentElement;
    dlg.remove();
    if (!slot) return;
    uiRestoreFocus(slot.dataset.opener);
    slot.dataset.opener = '';
}

// uiRestoreFocus focuses the opener, re-found by id because polling replaces it, else the search field
function uiRestoreFocus(id) {
    const target = (id && document.getElementById(id)) || document.getElementById('search');
    if (target) target.focus();
}

// the live log keeps the reader's place across its polls and follows new lines only when it was already at
// its end; a log opened fresh starts at its end when the run is in progress
let uiLogScroll = null;
const uiLogTargets = ['live-out', 'joutput'];
document.addEventListener('htmx:beforeSwap', function (evt) {
    const target = evt.detail.target;
    if (!evt.detail.shouldSwap || !uiLogTargets.includes(target.id)) return;
    const log = target.querySelector('.log');
    uiLogScroll = target.id === 'live-out' && log
        ? {top: log.scrollTop, left: log.scrollLeft, pinned: log.scrollHeight - log.scrollTop - log.clientHeight < 4}
        : {pinned: true, fresh: true};
});
document.addEventListener('htmx:afterSettle', function (evt) {
    if (!uiLogScroll || !uiLogTargets.includes(evt.detail.target.id)) return;
    const log = document.querySelector('#joutput .log');
    const live = !!document.getElementById('live-out');
    if (log && uiLogScroll.pinned && (live || !uiLogScroll.fresh)) {
        log.scrollTop = log.scrollHeight;
    } else if (log && !uiLogScroll.pinned) {
        log.scrollTop = uiLogScroll.top;
    }
    if (log && !uiLogScroll.fresh) log.scrollLeft = uiLogScroll.left;
    uiLogScroll = null;
});
document.addEventListener('DOMContentLoaded', function () {
    const log = document.querySelector('#live-out .log');
    if (log) log.scrollTop = log.scrollHeight;
});

// one tooltip under the hovered [data-tip], flipped above near the bottom edge; the native title delay is
// too slow for scanning history bars
const uiTip = document.getElementById('tip');
document.addEventListener('mouseover', function (evt) {
    const el = evt.target.closest && evt.target.closest('[data-tip]');
    if (!el) {
        uiTip.style.display = 'none';
        return;
    }
    uiTip.textContent = el.dataset.tip;
    uiTip.style.display = 'block';
    const r = el.getBoundingClientRect();
    const left = Math.max(8, Math.min(r.left + r.width / 2 - uiTip.offsetWidth / 2, innerWidth - uiTip.offsetWidth - 8));
    const below = r.bottom + 8 + uiTip.offsetHeight <= innerHeight - 8;
    uiTip.style.left = left + 'px';
    uiTip.style.top = (below ? r.bottom + 8 : r.top - uiTip.offsetHeight - 8) + 'px';
});
document.addEventListener('htmx:beforeSwap', function () { uiTip.style.display = 'none'; });

// uiToast shows a short note in the toast region
function uiToast(text) {
    const toasts = document.getElementById('toasts');
    const t = document.createElement('div');
    t.className = 't';
    t.textContent = text;
    toasts.replaceChildren(t);
}

// uiCopyCommand copies the command field's value; there is no htmx attribute for the clipboard
function uiCopyCommand() {
    const field = document.getElementById('cmdin');
    if (!field || !navigator.clipboard) {
        uiToast('Copy is not available here');
        return;
    }
    navigator.clipboard.writeText(field.value).then(
        function () { uiToast('Command copied'); },
        function () { uiToast('Copy failed'); });
}

// uiMarkEdited marks the command and date fields that differ from the job's own and relabels Run, which
// polls re-render
function uiMarkEdited() {
    const cmd = document.getElementById('cmdin');
    const date = document.getElementById('datein');
    if (!cmd) return;
    const edited = cmd.value !== cmd.dataset.orig;
    cmd.classList.toggle('edited', edited);
    if (date) date.classList.toggle('edited', date.value.trim() !== '');
    const label = document.querySelector('#run-now .lbl');
    if (label) label.textContent = edited ? 'Run edited' : 'Run now';
}
document.addEventListener('htmx:oobAfterSwap', function (evt) {
    if (evt.detail.target && evt.detail.target.id === 'runact') uiMarkEdited();
});

// menus are <details>: an outside click, a choice inside, or Esc closes the open one
document.addEventListener('click', function (evt) {
    document.querySelectorAll('details.menu[open]').forEach(function (menu) {
        const inside = menu.contains(evt.target);
        if (!inside || evt.target.closest('.tb-menu button, .tb-menu a')) menu.open = false;
    });
});
document.addEventListener('keydown', function (evt) {
    if (evt.key !== 'Escape') return;
    document.querySelectorAll('details.menu[open]').forEach(function (menu) {
        menu.open = false;
        menu.querySelector('summary').focus();
    });
});

// the selected run stays in view in the runs list after a bar, a row or a key selected it
document.body.addEventListener('htmx:afterSettle', function (evt) {
    if (evt.detail.target.id !== 'jruns') return;
    const sel = document.querySelector('#jruns .jr.sel');
    if (sel) sel.scrollIntoView({block: 'nearest', inline: 'nearest'});
});

// uiPollDone marks the page when a poll itself failed; requests from controls inside bubble here too
function uiPollDone(container, evt) {
    if (evt.detail.requestConfig.elt !== container) return;
    const app = container.closest('.app');
    if (app) app.classList.toggle('poll-failed', !evt.detail.successful);
}

// the keyboard map: each key focuses and clicks the element the mouse would use, so a key sends the same
// request a click does. The index cursor row is kept by job id because polls replace the rows
let uiCursor = '';
let uiOpenFirst = false;

function uiClick(el) {
    if (!el) return;
    el.focus();
    el.click();
}

function uiRows() {
    return Array.from(document.querySelectorAll('#rows .it'));
}

function uiCursorRow() {
    return uiRows().find(function (row) { return row.dataset.jobId === uiCursor; });
}

function uiMarkCursor() {
    uiRows().forEach(function (row) { row.classList.toggle('cur', row.dataset.jobId === uiCursor); });
}

// uiMoveCursor moves the index cursor by step rows, starting at either end
function uiMoveCursor(step) {
    const rows = uiRows();
    if (!rows.length) return;
    const at = rows.findIndex(function (row) { return row.dataset.jobId === uiCursor; });
    const next = at < 0 ? (step > 0 ? 0 : rows.length - 1) : Math.max(0, Math.min(rows.length - 1, at + step));
    uiCursor = rows[next].dataset.jobId;
    uiMarkCursor();
    rows[next].scrollIntoView({block: 'nearest'});
}

// uiRunStep selects the newer (-1) or older (1) run next to the one the output shows; the output carries the
// selection, the runs list highlight follows it a request later
function uiRunStep(step) {
    const runs = Array.from(document.querySelectorAll('#jruns .jr'));
    const shown = document.querySelector('#joutput [name="selected-run"]');
    let at = shown ? runs.findIndex(function (run) { return run.dataset.run === shown.value; }) : -1;
    if (at < 0) at = step > 0 ? -1 : runs.length;
    uiClick(runs[at + step]);
}

// uiJumpMove moves the jump list cursor by step and returns the item under it
function uiJumpMove(step) {
    const items = Array.from(document.querySelectorAll('#jdrop a.ji'));
    if (!items.length) return null;
    let at = items.findIndex(function (item) { return item.classList.contains('cur'); });
    at = at < 0 ? 0 : Math.max(0, Math.min(items.length - 1, at + step));
    items.forEach(function (item, n) { item.classList.toggle('cur', n === at); });
    items[at].scrollIntoView({block: 'nearest'});
    return items[at];
}

// uiJumpConfirm opens the run confirmation of a jump list job, carrying the list state like a Run click
function uiJumpConfirm(item) {
    if (!item.dataset.confirm) return;
    const values = {};
    ['filter-mode', 'sort-mode', 'sort-rev', 'list-search'].forEach(function (id) {
        const el = document.getElementById(id);
        if (el) values[el.name] = el.value;
    });
    htmx.ajax('POST', item.dataset.confirm, {target: '#dialog-slot', swap: 'innerHTML', values: values});
}

function uiShowKeys() {
    document.getElementById('keys-dialog').showModal();
}

document.addEventListener('htmx:afterSettle', function (evt) {
    const id = evt.detail.target.id;
    if (id === 'jdrop') uiJumpMove(0);
    if (id !== 'rows') return;
    uiMarkCursor();
    if (!uiOpenFirst) return;
    uiOpenFirst = false;
    const first = document.querySelector('#rows .it .nm');
    if (first) first.click();
});

// uiFieldKey handles the keys a text field gives the map: Esc leaves the field, Enter in the index search
// opens the first match once the filtered rows arrive, and the jump field drives its list
function uiFieldKey(evt) {
    const field = evt.target;
    const jump = field.id === 'search' && document.getElementById('jdrop');
    if (jump && (evt.key === 'ArrowDown' || evt.key === 'ArrowUp')) {
        evt.preventDefault();
        uiJumpMove(evt.key === 'ArrowDown' ? 1 : -1);
        return;
    }
    if (jump && evt.key === 'Enter') {
        evt.preventDefault();
        const item = document.querySelector('#jdrop a.ji.cur') || document.querySelector('#jdrop a.ji');
        if (!item) return;
        if (evt.shiftKey) uiJumpConfirm(item);
        else item.click();
        return;
    }
    if (field.id === 'search' && evt.key === 'Enter') {
        evt.preventDefault();
        uiOpenFirst = true;
        htmx.trigger(field, 'search');
        return;
    }
    // a filled index search keeps the browser's own Esc, which clears it; Esc on an empty one leaves it
    if (evt.key === 'Escape' && (jump || field.id !== 'search' || !field.value)) field.blur();
}

const uiKeys = {
    '/': function () { uiClick(document.getElementById('search')); },
    j: function (onJob) { if (onJob) uiClick(document.getElementById('next-job')); else uiMoveCursor(1); },
    k: function (onJob) { if (onJob) uiClick(document.getElementById('prev-job')); else uiMoveCursor(-1); },
    Enter: function (onJob) {
        const row = !onJob && uiCursorRow();
        if (row) row.querySelector('.nm').click();
    },
    r: function (onJob) {
        if (onJob) uiClick(document.getElementById('run-now'));
        else if (uiCursorRow()) uiClick(document.getElementById('run-' + uiCursor));
    },
    '[': function (onJob) { if (onJob) uiRunStep(-1); },
    ']': function (onJob) { if (onJob) uiRunStep(1); },
    E: function (onJob) { if (onJob) uiClick(document.querySelector('#facts .sw')); },
    Escape: function (onJob) { if (onJob) document.getElementById('back-link').click(); },
    t: function () { uiClick(document.getElementById('theme-toggle')); },
    '?': uiShowKeys,
};

// capture phase, so Esc with a menu open is left to the menu listener alone
document.addEventListener('keydown', function (evt) {
    if (document.querySelector('dialog[open]')) return;
    if ((evt.metaKey || evt.ctrlKey) && !evt.altKey && evt.key.toLowerCase() === 'k') {
        evt.preventDefault();
        uiKeys['/']();
        return;
    }
    if (evt.metaKey || evt.ctrlKey || evt.altKey) return;
    const target = evt.target;
    if (target.matches('input:not([type="checkbox"]), textarea, select')) {
        uiFieldKey(evt);
        return;
    }
    if (evt.key === 'Escape' && document.querySelector('details.menu[open]')) return;
    if (evt.key === 'Enter' && target.closest('a, button, summary')) return;
    const key = evt.key === 'E' && !evt.shiftKey ? '' : evt.key;
    const act = uiKeys[key];
    if (!act) return;
    evt.preventDefault();
    act(!!document.getElementById('jp'));
}, true);
