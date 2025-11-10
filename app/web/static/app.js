// handle refresh-jobs event
document.body.addEventListener('refresh-jobs', function(evt) {
    const searchInput = document.querySelector('[name="search"]');
    const searchParam = searchInput && searchInput.value ? '?search=' + encodeURIComponent(searchInput.value) : '';
    htmx.ajax('GET', '/api/jobs' + searchParam, {target: '#jobs-container', swap: 'innerHTML'});
});

// close modal on ESC key and confirm on Enter
document.addEventListener('keydown', function(e) {
    const modal = document.getElementById('job-modal');
    const historyModal = document.getElementById('history-modal');
    const confirmDialog = document.getElementById('confirm-dialog');
    const dialogVisible = confirmDialog && confirmDialog.style.display !== 'none';

    if (e.key === 'Escape') {
        if (modal && modal.style.display !== 'none') {
            modal.style.display = 'none';
        }
        if (historyModal && historyModal.style.display !== 'none') {
            historyModal.style.display = 'none';
        }
        if (dialogVisible && typeof window.confirmResolve === 'function') {
            window.confirmResolve(false);
        }
    }

    if (
        dialogVisible &&
        e.key === 'Enter' &&
        !e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey &&
        typeof window.confirmResolve === 'function'
    ) {
        e.preventDefault();
        window.confirmResolve(true);
    }
});

// custom confirm dialog with command editing
window.customConfirm = function(command, postUrl) {
    return new Promise(function(resolve) {
        const dialog = document.getElementById('confirm-dialog');
        const commandTextarea = document.getElementById('confirm-command');
        const dateField = document.getElementById('confirm-date-field');
        const dateInput = document.getElementById('confirm-date');
        const postUrlInput = document.getElementById('confirm-post-url');

        // populate command and URL
        commandTextarea.value = command;
        postUrlInput.value = postUrl;

        // show date field if command contains templates
        const hasTemplates = command.indexOf('{' + '{') !== -1 || command.indexOf('[' + '[') !== -1;
        dateField.style.display = hasTemplates ? 'block' : 'none';

        // clear date input
        dateInput.value = '';

        dialog.style.display = 'flex';

        window.confirmResolve = function(result) {
            if (result) {
                // get edited values
                const editedCommand = commandTextarea.value;
                const customDate = dateInput.value;

                // validate date if provided
                if (customDate && !/^\d{8}$/.test(customDate)) {
                    dateInput.setCustomValidity('Date must be exactly 8 digits in YYYYMMDD format (e.g., 20241225)');
                    dateInput.reportValidity();
                    return;
                }
                dateInput.setCustomValidity('');

                dialog.style.display = 'none';
                resolve({confirmed: true, command: editedCommand, date: customDate});
            } else {
                dialog.style.display = 'none';
                resolve({confirmed: false});
            }
        };
    });
};

// override HTMX confirm behavior - capture command and URL, show custom dialog
document.body.addEventListener('htmx:confirm', function(evt) {
    const question = evt.detail && evt.detail.question;
    if (!question) {
        return;
    }
    evt.preventDefault();

    // capture the POST URL and command from the triggering element
    const target = evt.detail.target;
    const postUrl = target.getAttribute('hx-post');
    const command = question; // command is passed as the confirm message

    window.customConfirm(command, postUrl).then(function(result) {
        if (result.confirmed && postUrl) {
            // create form data with command and optional date
            const formData = new FormData();
            formData.append('command', result.command);
            if (result.date) {
                formData.append('date', result.date);
            }

            // issue POST request with form data
            htmx.ajax('POST', postUrl, {
                target: 'body',
                swap: 'none',
                values: {
                    command: result.command,
                    date: result.date || ''
                }
            });
        }
    });
});

// track currently selected history row
let selectedHistoryRow = null;

// toggle between base and executed command in history modal
window.toggleHistoryCommand = function(row) {
    // find the command element in the history modal
    const historyModal = document.getElementById('history-content');
    if (!historyModal) {
        return;
    }

    const commandElement = historyModal.querySelector('.history-command');
    if (!commandElement) {
        return;
    }

    const baseCommand = row.getAttribute('data-base-command');
    const executedCommand = row.getAttribute('data-executed-command');

    // check if clicking the same row
    if (selectedHistoryRow === row) {
        // toggle between base and executed
        const isShowingExecuted = commandElement.classList.contains('showing-executed');
        if (isShowingExecuted) {
            commandElement.textContent = baseCommand;
            commandElement.classList.remove('showing-executed');
            row.classList.remove('history-row-selected');
            selectedHistoryRow = null;
        } else {
            commandElement.textContent = executedCommand;
            commandElement.classList.add('showing-executed');
        }
    } else {
        // different row clicked - show executed command
        // remove selection from previous row
        if (selectedHistoryRow) {
            selectedHistoryRow.classList.remove('history-row-selected');
        }

        commandElement.textContent = executedCommand;
        commandElement.classList.add('showing-executed');
        row.classList.add('history-row-selected');
        selectedHistoryRow = row;
    }
};

// reset selection when history modal closes
document.getElementById('history-modal').addEventListener('click', function(e) {
    if (e.target === this) {
        selectedHistoryRow = null;
        const commandElement = document.querySelector('#history-content .history-command');
        if (commandElement) {
            const baseCommand = commandElement.getAttribute('data-base-command');
            if (baseCommand) {
                commandElement.textContent = baseCommand;
                commandElement.classList.remove('showing-executed');
            }
        }
    }
});
