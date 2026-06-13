const socket = io();

let selectedTargetType = 'none';
let selectedTargetValue = null;
let clients = {};
let currentHistoryData = [];
let currentSortOrder = 'newest';
let groupClientsCache = { windows: [], linux: [], all: [] };
let groupCurrentIndex = { windows: 0, linux: 0, all: 0 };
let masterInfo = null;
let pendingMasterClientId = null;

const startBtn = document.getElementById('start-server');
const stopBtn = document.getElementById('stop-server');
const serverStatusBadge = document.getElementById('server-status-badge');
const serverPortSpan = document.getElementById('server-port');
const clientListEl = document.getElementById('client-list');
const logContainer = document.getElementById('log-container');
const messageInput = document.getElementById('message-input');
const sendBtn = document.getElementById('send-btn');
const clearLogBtn = document.getElementById('clear-log');
const themeToggle = document.getElementById('theme-toggle');
const historyPanelBody = document.getElementById('history-panel-body');
const historyPanelTitle = document.getElementById('history-panel-title');
const openFullHistoryBtn = document.getElementById('open-full-history');
const sortNewestBtn = document.getElementById('sort-newest-first');
const sortOldestBtn = document.getElementById('sort-oldest-first');

const dropdownToggle = document.getElementById('target-dropdown-toggle');
const dropdownMenu = document.getElementById('target-dropdown-menu');
const dropdownToggleText = document.getElementById('target-dropdown-text');
const groupNavigator = document.getElementById('group-navigator');
const groupPrevBtn = document.getElementById('group-prev');
const groupNextBtn = document.getElementById('group-next');
const groupClientLabel = document.getElementById('group-client-label');

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

function animateButton(btn) {
    btn.classList.add('clicked');
    setTimeout(() => btn.classList.remove('clicked'), 200);
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
            if (entry.prompt !== undefined) {
                html += `<div class="history-prompt"><i class="fa-regular fa-message"></i> ${escapeHtml(entry.prompt)}</div>`;
                html += `<div class="history-command"><i class="fa-solid fa-terminal"></i> ${escapeHtml(entry.command)}</div>`;
            } else {
                html += `<div class="history-command"><i class="fa-solid fa-terminal"></i> ${escapeHtml(entry.command)}</div>`;
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

function updateGroupNavigator() {
    if (selectedTargetType === 'group' || selectedTargetType === 'all') {
        const group = selectedTargetValue || 'all';
        const groupList = groupClientsCache[group] || [];
        if (groupList.length > 1) {
            groupNavigator.style.display = 'flex';
            const currentClientId = groupList[groupCurrentIndex[group]];
            const client = clients[currentClientId];
            if (client) {
                const display = client.remote_addr ? client.remote_addr.split(':')[0] : currentClientId;
                groupClientLabel.textContent = `${display} (${groupCurrentIndex[group]+1}/${groupList.length})`;
            } else {
                groupClientLabel.textContent = `${currentClientId} (${groupCurrentIndex[group]+1}/${groupList.length})`;
            }
            loadHistoryForClientId(currentClientId);
        } else {
            groupNavigator.style.display = 'none';
            if (groupList.length === 1) {
                loadHistoryForClientId(groupList[0]);
            } else {
                currentHistoryData = null;
                renderHistoryPanel();
            }
        }
    } else if (selectedTargetType === 'single') {
        groupNavigator.style.display = 'none';
        loadHistoryForClientId(selectedTargetValue);
    } else {
        groupNavigator.style.display = 'none';
        currentHistoryData = null;
        historyPanelBody.innerHTML = '<div class="log-entry info">Select a target to view history</div>';
        historyPanelTitle.textContent = 'Command history';
        openFullHistoryBtn.style.display = 'none';
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

function updateMasterButton(clientId, isMaster, currentModel) {
    const existingBtn = document.querySelector(`.master-btn[data-client-id="${clientId}"]`);
    if (!existingBtn) return;

    existingBtn.classList.remove('active', 'loading');

    if (!isMaster) {
        existingBtn.innerHTML = '<i class="fa-solid fa-crown"></i> Make Master';
        return;
    }

    if (currentModel && currentModel !== '') {
        existingBtn.classList.add('active');
        existingBtn.innerHTML = '<i class="fa-solid fa-crown"></i> LLM Master';
    } else {
        existingBtn.classList.add('loading');
        existingBtn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Setting up...';
    }
}

function updateAllMasterButtons() {
    for (const id in clients) {
        let isMaster = false;
        let currentModel = '';
        if (masterInfo) {
            isMaster = masterInfo.client_id === id;
            currentModel = isMaster ? (masterInfo.model || clients[id]?.current_model || '') : '';
        } else {
            if (pendingMasterClientId === id) {
                isMaster = true;
                currentModel = '';
            }
        }
        updateMasterButton(id, isMaster, currentModel);
    }
}

function updateDropdownMenu() {
    const allClients = Object.values(clients);
    const connectedClients = allClients.filter(c => c.status === 'connected');
    const hasWindows = groupClientsCache.windows.length > 0;
    const hasLinux = groupClientsCache.linux.length > 0;

    let html = `<div class="dropdown-item" data-type="none" data-value="">None</div>`;
    html += `<div class="dropdown-item" data-type="all" data-value="">ALL</div>`;
    connectedClients.forEach(c => {
        const display = truncateClientId(c.client_id);
        html += `<div class="dropdown-item" data-type="single" data-value="${escapeHtml(c.client_id)}">${escapeHtml(display)}</div>`;
    });
    if (hasWindows) {
        html += `<div class="dropdown-divider"></div>`;
        html += `<div class="dropdown-item" data-type="group" data-value="windows"><i class="fab fa-windows"></i> Windows clients</div>`;
    }
    if (hasLinux) {
        if (!hasWindows) html += `<div class="dropdown-divider"></div>`;
        else html += `<div class="dropdown-divider"></div>`;
        html += `<div class="dropdown-item" data-type="group" data-value="linux"><i class="fab fa-linux"></i> Linux clients</div>`;
    }
    dropdownMenu.innerHTML = html;

    document.querySelectorAll('.dropdown-item').forEach(item => {
        item.addEventListener('click', (e) => {
            e.stopPropagation();
            const type = item.dataset.type;
            const value = item.dataset.value;
            setSelectedTarget(type, value);
            dropdownMenu.classList.remove('show');
        });
    });
}

function setSelectedTarget(type, value) {
    selectedTargetType = type;
    selectedTargetValue = value;

    let displayText = 'None';
    if (type === 'none') {
        displayText = 'None';
        messageInput.disabled = true;
        sendBtn.disabled = true;
    } else if (type === 'all') {
        displayText = 'ALL';
        messageInput.disabled = false;
        sendBtn.disabled = false;
    } else if (type === 'single') {
        const c = clients[value];
        displayText = c ? truncateClientId(c.client_id) : truncateClientId(value);
        messageInput.disabled = false;
        sendBtn.disabled = false;
    } else if (type === 'group') {
        displayText = (value === 'windows') ? 'Windows clients' : 'Linux clients';
        messageInput.disabled = false;
        sendBtn.disabled = false;
    }
    dropdownToggleText.textContent = displayText;

    document.querySelectorAll('#client-list .node-item').forEach(item => {
        item.classList.remove('active');
        if (type === 'single' && item.dataset.clientId === value) {
            item.classList.add('active');
        }
    });

    if (type === 'group' || type === 'all') {
        const group = type === 'all' ? 'all' : value;
        groupCurrentIndex[group] = 0;
        updateGroupNavigator();
    } else if (type === 'single') {
        updateGroupNavigator();
    } else {
        groupNavigator.style.display = 'none';
        currentHistoryData = null;
        historyPanelBody.innerHTML = '<div class="log-entry info">Select a target to view history</div>';
        historyPanelTitle.textContent = 'Command history';
        openFullHistoryBtn.style.display = 'none';
    }
}

function updateClientList(clientsArray) {
    clients = {};
    clientListEl.innerHTML = '';

    clientsArray.forEach(client => {
        clients[client.client_id] = client;
        if (client.setup_progress || client._setupProgress) {
            client._setupProgress = client.setup_progress || client._setupProgress;
            if (!masterInfo && !pendingMasterClientId) {
                pendingMasterClientId = client.client_id;
            }
        }
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
        let currentModelHtml = '';
        if (client.status === 'connected') {
            const modelDisplay = client.current_model || 'No model';
            currentModelHtml = `<div class="node-model"><i class="fa-solid fa-microchip"></i> ${escapeHtml(modelDisplay)}</div>`;
        }
        let modelSelectorHtml = '';
        const isMaster = masterInfo && masterInfo.client_id === client.client_id;
        if (client.status === 'connected' && client.available_models && client.available_models.length > 0) {
            modelSelectorHtml = `<select class="model-selector" data-client-id="${escapeHtml(client.client_id)}">`;
            client.available_models.forEach(model => {
                const selected = model === client.current_model ? 'selected' : '';
                modelSelectorHtml += `<option value="${escapeHtml(model)}" ${selected}>${escapeHtml(model)}</option>`;
            });
            modelSelectorHtml += `</select>`;
        } else if (client.status === 'connected') {
            modelSelectorHtml = `<select class="model-selector" disabled><option>No models</option></select>`;
        }

        let masterButtonHtml = '';
        if (client.status === 'connected') {
            masterButtonHtml = `<button class="master-btn compact-btn" data-client-id="${escapeHtml(client.client_id)}" type="button">
                <i class="fa-regular fa-crown"></i> Make Master
            </button>`;
        }
        let progressHtml = '';
        if (client.status === 'connected' && client._setupProgress) {
            progressHtml = `<div class="setup-progress-bar">
                <div class="progress-fill" style="width:${client._setupProgress.percent || 0}%"></div>
                <span class="progress-label">${escapeHtml(client._setupProgress.message || '')}</span>
            </div>`;
        }
        item.innerHTML = `
            <div class="node-main">
                <div class="node-icon"><i class="fas fa-laptop"></i></div>
                <div>
                    <div class="node-name">${escapeHtml(client.client_id)}</div>
                    <div class="node-meta">${escapeHtml(client.user || '?')}@${escapeHtml(client.hostname || '?')} (${escapeHtml(client.remote_addr.split(':')[0] || '?')})</div>
                    ${currentModelHtml}
                    ${progressHtml}
                </div>
            </div>
            <div class="node-actions">
                ${historyButtonHtml}
                ${modelSelectorHtml}
                ${masterButtonHtml}
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
        const modelSelector = item.querySelector('.model-selector');
        if (modelSelector) {
            modelSelector.addEventListener('change', (e) => {
                e.stopPropagation();
                const newModel = modelSelector.value;
                socket.emit('change_model', { client_id: client.client_id, model: newModel });
                addLogEntry(`Requested model change for ${client.client_id} to ${newModel}`, 'info');
            });
            modelSelector.addEventListener('click', (e) => e.stopPropagation());
        }
        const masterBtn = item.querySelector('.master-btn');
        if (masterBtn) {
            masterBtn.addEventListener('click', (e) => {
                e.stopPropagation();
                if (masterInfo && masterInfo.client_id === client.client_id) {
                    socket.emit('deselect_master');
                    pendingMasterClientId = null;
                } else {
                    socket.emit('select_master', { client_id: client.client_id });
                    pendingMasterClientId = client.client_id;
                    updateAllMasterButtons();
                }
            });
        }
        item.addEventListener('click', (e) => {
            if (historyBtn && (e.target === historyBtn || historyBtn.contains(e.target))) return;
            if (modelSelector && (e.target === modelSelector || modelSelector.contains(e.target))) return;
            if (masterBtn && (e.target === masterBtn || masterBtn.contains(e.target))) return;
            const clientId = item.dataset.clientId;
            if (client.status !== 'connected') return;
            if (selectedTargetType === 'single' && selectedTargetValue === clientId) {
                setSelectedTarget('none', null);
            } else {
                setSelectedTarget('single', clientId);
            }
        });
        clientListEl.appendChild(item);
    });

    rebuildGroupCaches();
    updateAllMasterButtons();
    updateDropdownMenu();

    if (selectedTargetType === 'single' && selectedTargetValue && !clients[selectedTargetValue]) {
        setSelectedTarget('none', null);
    } else if (selectedTargetType === 'group' || selectedTargetType === 'all') {
        const group = selectedTargetType === 'all' ? 'all' : selectedTargetValue;
        if (groupClientsCache[group].length === 0) {
            setSelectedTarget('none', null);
        } else {
            if (!groupClientsCache[group].includes(selectedTargetValue)) {
                groupCurrentIndex[group] = 0;
            }
            updateGroupNavigator();
        }
    } else if (selectedTargetType === 'single') {
        const activeItem = Array.from(document.querySelectorAll('#client-list .node-item')).find(
            item => item.dataset.clientId === selectedTargetValue
        );
        if (activeItem) activeItem.classList.add('active');
        updateGroupNavigator();
    } else {
        updateGroupNavigator();
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
    if (selectedTargetType !== 'group' && selectedTargetType !== 'all') return;
    const group = selectedTargetType === 'all' ? 'all' : selectedTargetValue;
    const list = groupClientsCache[group] || [];
    if (list.length === 0) return;
    groupCurrentIndex[group] = (groupCurrentIndex[group] - 1 + list.length) % list.length;
    updateGroupNavigator();
});

groupNextBtn.addEventListener('click', () => {
    if (selectedTargetType !== 'group' && selectedTargetType !== 'all') return;
    const group = selectedTargetType === 'all' ? 'all' : selectedTargetValue;
    const list = groupClientsCache[group] || [];
    if (list.length === 0) return;
    groupCurrentIndex[group] = (groupCurrentIndex[group] + 1) % list.length;
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
        setSelectedTarget('none', null);
        masterInfo = null;
        pendingMasterClientId = null;
        updateAllMasterButtons();
    }
});

socket.on('master_info', (info) => {
    masterInfo = info;
    pendingMasterClientId = info ? info.client_id : null;
    if (info && info.client_id && info.model) {
        if (clients[info.client_id] && clients[info.client_id]._setupProgress) {
            delete clients[info.client_id]._setupProgress;
        }
    } else if (!info) {
        for (const id in clients) {
            if (clients[id]._setupProgress) delete clients[id]._setupProgress;
        }
    }
    updateAllMasterButtons();
    updateClientList(Object.values(clients));
});

socket.on('master_setup_progress', (data) => {
    const client = clients[data.client_id];
    if (!client) return;
    if (!client._setupProgress) {
        client._setupProgress = {};
    }
    const stageMap = {
        'start': 0,
        'checking_ollama': 10,
        'checking_vcredist': 20,
        'installing_vcredist': 30,
        'installing_ollama': 50,
        'checking_models': 60,
        'pulling_model': 70,
        'configuring_service': 90,
        'finished': 100
    };
    const percent = stageMap[data.stage] || 50;
    client._setupProgress.percent = percent;
    client._setupProgress.message = data.message || data.stage;
    if (data.status === 'error') {
        client._setupProgress.isError = true;
    }
    updateClientList(Object.values(clients));
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
    if (selectedTargetType === 'single' && selectedTargetValue === data.client_id) {
        loadHistoryForClientId(data.client_id);
    } else if (selectedTargetType === 'group' || selectedTargetType === 'all') {
        const group = selectedTargetType === 'all' ? 'all' : selectedTargetValue;
        const currentClientId = groupClientsCache[group]?.[groupCurrentIndex[group]];
        if (currentClientId === data.client_id) {
            loadHistoryForClientId(data.client_id);
        }
    }
});

startBtn.addEventListener('click', () => {
    animateButton(startBtn);
    socket.emit('start_server');
});

stopBtn.addEventListener('click', () => {
    animateButton(stopBtn);
    socket.emit('stop_server');
});

sendBtn.addEventListener('click', () => {
    const msg = messageInput.value.trim();
    if (!msg) return;
    if (selectedTargetType === 'none') {
        addLogEntry('No target selected', 'warning');
        return;
    }
    if (selectedTargetType === 'all') {
        socket.emit('send_to_all', { message: msg });
        addLogEntry(`Broadcasting prompt to ALL: ${msg}`, 'info');
    } else if (selectedTargetType === 'single') {
        const c = clients[selectedTargetValue];
        if (c && c.status === 'connected') {
            socket.emit('send_message', { client_id: selectedTargetValue, message: msg });
            addLogEntry(`Sent to ${selectedTargetValue}: ${msg}`, 'info');
        } else {
            addLogEntry(`Cannot send: client disconnected`, 'error');
            setSelectedTarget('none', null);
        }
    } else if (selectedTargetType === 'group') {
        const group = selectedTargetValue;
        const targets = groupClientsCache[group] || [];
        const onlineTargets = targets.filter(id => clients[id] && clients[id].status === 'connected');
        if (onlineTargets.length === 0) {
            addLogEntry(`No online ${group} clients`, 'warning');
            return;
        }
        onlineTargets.forEach(id => {
            socket.emit('send_message', { client_id: id, message: msg });
        });
        addLogEntry(`Sent to ${onlineTargets.length} online ${group} client(s): ${msg}`, 'info');
    }
    messageInput.value = '';
    animateButton(sendBtn);
});

messageInput.addEventListener('keypress', (e) => {
    if (e.key === 'Enter') sendBtn.click();
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
setSelectedTarget('none', null);