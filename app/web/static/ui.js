// browser behaviour htmx has no attribute for: holding polls while a button is pressed, the shared blink
// phase, dialogs and focus, live log scrolling, tooltips, the clipboard, edited-command marking, menus, and
// marking a failed poll

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
// A response that empties the slot (stop accepted, run gone) returns focus to the opener
function uiDialogSwapped(slot) {
    const dlg = slot.querySelector('dialog');
    if (!dlg) {
        uiRestoreFocus(slot.dataset.opener);
        slot.dataset.opener = '';
        return;
    }
    if (!slot.dataset.opener && document.activeElement && document.activeElement.id) {
        slot.dataset.opener = document.activeElement.id;
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
