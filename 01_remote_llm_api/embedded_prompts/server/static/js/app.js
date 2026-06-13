const socket = io();

let selectedClientId = null;
let clients = {};
let currentHistoryData = [];
let currentSortOrder = 'newest';
let groupClientsCache = { windows: [], linux: [], all: [] };
let groupCurrentIndex = { windows: 0, linux: 0, all: 0 };
let selectedGroup = 'all';

const startBtn = document.getElementById('start-server');
const stopBtn = document.getElementById('stop-server');
const serverStatusBadge = document.getElementById('server-status-badge');
const serverPortSpan = document.getElementById('server-port');
const clientListEl = document.getElementById('client-list');
const logContainer = document.getElementById('log-container');
const clearLogBtn = document.getElementById('clear-log');
const themeToggle = document.getElementById('theme-toggle');
const historyPanelBody = document.getElementById('history-panel-body');
const historyPanelTitle = document.getElementById('history-panel-title');
const openFullHistoryBtn = document.getElementById('open-full-history');
const sortNewestBtn = document.getElementById('sort-newest-first');
const sortOldestBtn = document.getElementById('sort-oldest-first');
const groupNavigator = document.getElementById('group-navigator');
const groupPrevBtn = document.getElementById('group-prev');
const groupNextBtn = document.getElementById('group-next');
const groupClientLabel = document.getElementById('group-client-label');
const dropdownToggle = document.getElementById('target-dropdown-toggle');
const dropdownMenu = document.getElementById('target-dropdown-menu');
const dropdownToggleText = document.getElementById('target-dropdown-text');

function getOSIcon(os) {
    os = (os || '').toLowerCase();
    if (os.includes('win')) return 'fab fa-windows';
    if (os.includes('linux')) return 'fab fa-linux';
    if (os.includes('mac') || os.includes('darwin')) return 'fab fa-apple';
    return 'fas fa-laptop';
}

function truncateClientId(id) {
    if (!id) return '';
    if (id.length <= 16) return id;
    return id.substring(0, 3) + '...' + id.substring(id.length - 3);
}

function addLogEntry(text, type = 'info') {
    const entry = document.createElement('div');
    entry.className = `log-entry ${type}`;
    entry.textContent = text;
    logContainer.appendChild(entry);
    logContainer.scrollTop = logContainer.scrollHeight;
}

function clearLog() {
    logContainer.innerHTML = '';
}

function escapeHtml(str) {
    if (!str) return '';
    return String(str).replace(/[&<>]/g, function(m) {
        if (m === '&') return '&amp;';
        if (m === '<') return '&lt;';
        if (m === '>') return '&gt;';
        return m;
    });
}

function formatTimestamp(isoString) {
    const date = new Date(isoString);
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    const hours = String(date.getHours()).padStart(2, '0');
    const minutes = String(date.getMinutes()).padStart(2, '0');
    const seconds = String(date.getSeconds()).padStart(2, '0');
    return `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`;
}

function renderHistoryPanel() {
    if (!currentHistoryData) {
        historyPanelBody.innerHTML = '<div class="log-entry info">No history available</div>';
        return;
    }
    let entries = [...currentHistoryData];
    if (currentSortOrder === 'newest') {
        entries.reverse();
    }
    historyPanelBody.innerHTML = '';
    if (entries.length === 0) {
        historyPanelBody.innerHTML = '<div class="log-entry info">No commands executed yet</div>';
    } else {
        entries.forEach(entry => {
            const div = document.createElement('div');
            div.className = 'log-entry history-entry';
            const formattedTime = formatTimestamp(entry.timestamp);
            let html = `<span class="history-time">[${formattedTime}]</span>`;
            if (entry.prompt !== undefined && entry.prompt !== null) {
                html += `<div class="history-prompt"><i class="fa-regular fa-message"></i> ${escapeHtml(entry.prompt)}</div>`;
                html += `<div class="history-command"><i class="fa-solid fa-terminal"></i> ${escapeHtml(entry.command)}</div>`;
            } else {
                html += `<div class="history-command"><i class="fa-solid fa-terminal"></i> ${escapeHtml(entry.command || '')}</div>`;
            }
            html += `<div class="history-output"><pre>${escapeHtml(entry.output)}</pre></div>`;
            div.innerHTML = html;
            historyPanelBody.appendChild(div);
        });
    }
    if (currentSortOrder === 'newest') {
        historyPanelBody.scrollTop = 0;
    } else {
        historyPanelBody.scrollTop = historyPanelBody.scrollHeight;
    }
}

async function loadHistoryForClientId(clientId) {
    if (!clientId) {
        currentHistoryData = null;
        historyPanelBody.innerHTML = '<div class="log-entry info">No client selected</div>';
        historyPanelTitle.textContent = 'Command history';
        openFullHistoryBtn.style.display = 'none';
        groupNavigator.style.display = 'none';
        return;
    }
    const client = clients[clientId];
    if (!client) {
        currentHistoryData = null;
        historyPanelBody.innerHTML = '<div class="log-entry error">Client not found</div>';
        historyPanelTitle.textContent = 'Command history';
        openFullHistoryBtn.style.display = 'none';
        groupNavigator.style.display = 'none';
        return;
    }
    historyPanelTitle.textContent = `History: ${escapeHtml(clientId)}`;
    openFullHistoryBtn.style.display = 'inline-flex';
    openFullHistoryBtn.onclick = () => {
        window.open(`/history/${encodeURIComponent(clientId)}`, '_blank');
    };
    try {
        const response = await fetch(`/api/history/${encodeURIComponent(clientId)}`);
        const history = await response.json();
        currentHistoryData = history;
        renderHistoryPanel();
    } catch (err) {
        currentHistoryData = null;
        historyPanelBody.innerHTML = '<div class="log-entry error">Failed to load history</div>';
    }
}

function rebuildGroupCaches() {
    groupClientsCache.windows = [];
    groupClientsCache.linux = [];
    groupClientsCache.all = [];
    for (const id in clients) {
        const c = clients[id];
        groupClientsCache.all.push(id);
        const osLower = (c.os || '').toLowerCase();
        if (osLower.includes('win')) {
            groupClientsCache.windows.push(id);
        } else if (osLower.includes('linux')) {
            groupClientsCache.linux.push(id);
        }
    }
}

function updateDropdownMenu() {
    rebuildGroupCaches();
    const hasWindows = groupClientsCache.windows.length > 0;
    const hasLinux = groupClientsCache.linux.length > 0;
    let html = `<div class="dropdown-item" data-group="all">ALL (${groupClientsCache.all.length})</div>`;
    if (hasWindows) {
        html += `<div class="dropdown-item" data-group="windows"><i class="fab fa-windows"></i> Windows (${groupClientsCache.windows.length})</div>`;
    }
    if (hasLinux) {
        html += `<div class="dropdown-item" data-group="linux"><i class="fab fa-linux"></i> Linux (${groupClientsCache.linux.length})</div>`;
    }
    dropdownMenu.innerHTML = html;
    document.querySelectorAll('.dropdown-item').forEach(item => {
        item.addEventListener('click', (e) => {
            e.stopPropagation();
            const group = item.dataset.group;
            setSelectedGroup(group);
            dropdownMenu.classList.remove('show');
        });
    });
    const currentList = groupClientsCache[selectedGroup] || [];
    let displayText = 'Select group';
    if (selectedGroup === 'all') displayText = `ALL (${currentList.length})`;
    else if (selectedGroup === 'windows') displayText = `Windows (${currentList.length})`;
    else if (selectedGroup === 'linux') displayText = `Linux (${currentList.length})`;
    dropdownToggleText.textContent = displayText;
}

function setSelectedGroup(group) {
    selectedGroup = group;
    let displayText = 'Select group';
    if (group === 'all') displayText = `ALL (${groupClientsCache.all.length})`;
    else if (group === 'windows') displayText = `Windows (${groupClientsCache.windows.length})`;
    else if (group === 'linux') displayText = `Linux (${groupClientsCache.linux.length})`;
    dropdownToggleText.textContent = displayText;

    const list = groupClientsCache[group] || [];
    if (list.length > 0) {
        groupCurrentIndex[group] = 0;
        const firstId = list[0];
        selectClient(firstId);
        updateGroupNavigator();
    } else {
        selectedClientId = null;
        loadHistoryForClientId(null);
        groupNavigator.style.display = 'none';
    }
}

function selectClient(clientId) {
    if (!clientId || !clients[clientId]) {
        selectedClientId = null;
        loadHistoryForClientId(null);
        groupNavigator.style.display = 'none';
        return;
    }
    selectedClientId = clientId;
    loadHistoryForClientId(clientId);
    document.querySelectorAll('#client-list .node-item').forEach(item => {
        item.classList.remove('active');
        if (item.dataset.clientId === clientId) {
            item.classList.add('active');
        }
    });
}

function updateGroupNavigator() {
    if (!selectedClientId) {
        groupNavigator.style.display = 'none';
        return;
    }
    const list = groupClientsCache[selectedGroup] || [];
    if (list.length <= 1) {
        groupNavigator.style.display = 'none';
        return;
    }
    const currentIdx = list.indexOf(selectedClientId);
    if (currentIdx === -1) {
        groupNavigator.style.display = 'none';
        return;
    }
    groupCurrentIndex[selectedGroup] = currentIdx;
    const client = clients[selectedClientId];
    const display = client ? (client.remote_addr ? client.remote_addr.split(':')[0] : selectedClientId) : selectedClientId;
    groupClientLabel.textContent = `${display} (${currentIdx+1}/${list.length})`;
    groupNavigator.style.display = 'flex';
}

function updateClientList(clientsArray) {
    clients = {};
    clientListEl.innerHTML = '';
    clientsArray.forEach(client => {
        clients[client.client_id] = client;
        const item = document.createElement('button');
        item.className = `node-item ${client.status}`;
        item.dataset.clientId = client.client_id;
        const osIcon = getOSIcon(client.os);
        const statusText = client.status === 'connected' ? 'Connected' : 'Disconnected';
        const statusClass = client.status === 'connected' ? 'node-status' : 'node-status disconnected';
        let historyButtonHtml = '';
        if (client.has_history) {
            historyButtonHtml = `<button class="history-btn" data-client="${escapeHtml(client.client_id)}"><i class="fa-regular fa-clock"></i> History</button>`;
        }
        item.innerHTML = `
            <div class="node-main">
                <div class="node-icon"><i class="fas fa-laptop"></i></div>
                <div>
                    <div class="node-name">${escapeHtml(client.client_id)}</div>
                    <div class="node-meta">${escapeHtml(client.user || '?')}@${escapeHtml(client.hostname || '?')} (${escapeHtml(client.remote_addr.split(':')[0] || '?')})</div>
                </div>
            </div>
            <div class="node-actions">
                ${historyButtonHtml}
                <div class="${statusClass}"><i class="${osIcon}"></i> ${statusText}</div>
            </div>
        `;
        const historyBtn = item.querySelector('.history-btn');
        if (historyBtn) {
            historyBtn.addEventListener('click', (e) => {
                e.stopPropagation();
                window.open(`/history/${encodeURIComponent(client.client_id)}`, '_blank');
            });
        }
        item.addEventListener('click', (e) => {
            if (historyBtn && (e.target === historyBtn || historyBtn.contains(e.target))) return;
            selectClient(client.client_id);
            if (selectedGroup !== 'all' && !groupClientsCache[selectedGroup].includes(client.client_id)) {
                selectedGroup = 'all';
                dropdownToggleText.textContent = `ALL (${groupClientsCache.all.length})`;
            }
            updateGroupNavigator();
        });
        clientListEl.appendChild(item);
    });

    updateDropdownMenu();

    if (selectedClientId && clients[selectedClientId]) {
        selectClient(selectedClientId);
        updateGroupNavigator();
    } else {
        if (groupClientsCache[selectedGroup].length > 0) {
            const firstId = groupClientsCache[selectedGroup][0];
            selectClient(firstId);
            updateGroupNavigator();
        } else {
            selectClient(null);
        }
    }
}

function updateSortButtons() {
    sortNewestBtn.classList.toggle('active', currentSortOrder === 'newest');
    sortOldestBtn.classList.toggle('active', currentSortOrder === 'oldest');
}

sortNewestBtn.addEventListener('click', () => {
    currentSortOrder = 'newest';
    updateSortButtons();
    renderHistoryPanel();
});
sortOldestBtn.addEventListener('click', () => {
    currentSortOrder = 'oldest';
    updateSortButtons();
    renderHistoryPanel();
});

groupPrevBtn.addEventListener('click', () => {
    const list = groupClientsCache[selectedGroup] || [];
    if (list.length === 0) return;
    let currentIdx = list.indexOf(selectedClientId);
    if (currentIdx === -1) currentIdx = 0;
    const newIdx = (currentIdx - 1 + list.length) % list.length;
    selectClient(list[newIdx]);
    updateGroupNavigator();
});

groupNextBtn.addEventListener('click', () => {
    const list = groupClientsCache[selectedGroup] || [];
    if (list.length === 0) return;
    let currentIdx = list.indexOf(selectedClientId);
    if (currentIdx === -1) currentIdx = 0;
    const newIdx = (currentIdx + 1) % list.length;
    selectClient(list[newIdx]);
    updateGroupNavigator();
});

dropdownToggle.addEventListener('click', (e) => {
    e.stopPropagation();
    dropdownMenu.classList.toggle('show');
});

document.addEventListener('click', (e) => {
    if (!dropdownToggle.contains(e.target) && !dropdownMenu.contains(e.target)) {
        dropdownMenu.classList.remove('show');
    }
});

socket.on('connect', () => {
    addLogEntry('Connected to web server', 'success');
});

socket.on('server_status', (data) => {
    if (data.running) {
        serverStatusBadge.textContent = 'Active';
        serverStatusBadge.dataset.status = 'active';
        serverPortSpan.textContent = `Port: ${data.port}`;
        startBtn.disabled = true;
        stopBtn.disabled = false;
    } else {
        serverStatusBadge.textContent = 'Stopped';
        serverStatusBadge.dataset.status = 'stopped';
        serverPortSpan.textContent = 'Port: 9000';
        startBtn.disabled = false;
        stopBtn.disabled = true;
        selectedClientId = null;
        loadHistoryForClientId(null);
    }
});

socket.on('client_list', (clientsArray) => {
    updateClientList(clientsArray);
});

socket.on('client_added', (client) => {
    addLogEntry(`New client: ${client.client_id}`, 'success');
    socket.emit('get_clients');
});

socket.on('client_removed', (clientId) => {
    addLogEntry(`Client disconnected: ${clientId}`, 'warning');
    socket.emit('get_clients');
});

socket.on('log', (message) => {
    addLogEntry(message, 'info');
});

socket.on('command_output', (data) => {
    if (selectedClientId === data.client_id) {
        loadHistoryForClientId(data.client_id);
    }
});

startBtn.addEventListener('click', () => {
    socket.emit('start_server');
});

stopBtn.addEventListener('click', () => {
    socket.emit('stop_server');
});

clearLogBtn.addEventListener('click', clearLog);

themeToggle.addEventListener('click', () => {
    document.body.classList.toggle('light-mode');
    document.body.classList.toggle('dark-mode');
    const icon = themeToggle.querySelector('i');
    if (document.body.classList.contains('light-mode')) {
        icon.className = 'fa-solid fa-moon';
    } else {
        icon.className = 'fa-solid fa-sun';
    }
});

updateSortButtons();
document.body.classList.add('dark-mode');
themeToggle.querySelector('i').className = 'fa-solid fa-sun';
setSelectedGroup('all');