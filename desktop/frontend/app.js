const list = document.getElementById('node-list');
const status = document.getElementById('status');
const nameInput = document.getElementById('node-name');
const addressInput = document.getElementById('direct-address');
const addressError = document.getElementById('address-error');
const networkDetail = document.getElementById('network-detail');
const firewallButton = document.getElementById('configure-firewall');
const firewallRemoveButton = document.getElementById('remove-firewall');
const firewallProgram = document.getElementById('firewall-program');
const recentRooms = document.getElementById('recent-rooms');
const recentRoomList = document.getElementById('recent-room-list');
let initialisedName = false;

async function refreshNetworkStatus() {
	if (!window.go || !window.go.main || !window.go.main.App) return;
	try {
		const network = await window.go.main.App.GetNetworkStatus();
		const category = network.networkCategory === 'Private' ? '专用网络' :
			network.networkCategory === 'Public' ? '公用网络' : '未知网络';
		const discovery = network.systemDiscoveryEnabled ? '系统发现已启用' : 'UDP 兼容发现';
		const firewall = network.firewallConfigured ? '防火墙已放行' : '防火墙未配置';
		networkDetail.textContent = `${network.networkName || '当前网络'} · ${category} · ${discovery} · ${firewall}`;
		firewallButton.textContent = network.firewallConfigured ? '更新规则' : '添加规则';
		firewallRemoveButton.disabled = !network.firewallTcpConfigured && !network.firewallUdpConfigured;
		firewallProgram.textContent = network.firewallProgram ? `程序：${network.firewallProgram}` : '';
		firewallProgram.title = network.firewallProgram || ''
	} catch (error) {
		networkDetail.textContent = '无法读取网络状态'
	}
}

function escapeHTML(value) {
	return String(value).replace(/[&<>'"]/g, character => ({
		'&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;'
	})[character])
}

async function refreshRecentRooms() {
	try {
		const rooms = await window.go.main.App.ListRecentRooms();
		recentRooms.hidden = !rooms.length;
		if (!rooms.length) {
			recentRoomList.innerHTML = '';
			return
		}
		recentRoomList.innerHTML = rooms.map(room => {
			const updated = room.updatedAt ? new Date(room.updatedAt).toLocaleString('zh-CN', {
				month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit'
			}) : '';
			let nodeAddress = room.nodeUrl;
			try { nodeAddress = new URL(room.nodeUrl).host } catch (error) {}
			return `
			<div class="recent-room-row">
				<div class="recent-room-mark">#</div>
				<div class="recent-room-info">
					<div class="recent-room-name">${escapeHTML(room.roomName)}${room.hasPassword ? '<span class="password-mark">有密码</span>' : '<span class="password-mark open">无密码</span>'}</div>
					<div class="recent-room-meta">用户名 ${escapeHTML(room.userName)} · 节点 ${escapeHTML(nodeAddress)} · ${escapeHTML(updated)}</div>
				</div>
				<button type="button" class="recent-enter" data-recent-id="${escapeHTML(room.id)}">进入</button>
				<button type="button" class="recent-delete" data-delete-recent="${escapeHTML(room.id)}" title="删除记录" aria-label="删除记录">×</button>
			</div>`
		}).join('');
		for (const button of recentRoomList.querySelectorAll('[data-recent-id]')) {
			button.addEventListener('click', () => window.go.main.App.OpenRecentRoom(button.dataset.recentId))
		}
		for (const button of recentRoomList.querySelectorAll('[data-delete-recent]')) {
			button.addEventListener('click', async () => {
				await window.go.main.App.DeleteRecentRoom(button.dataset.deleteRecent);
				refreshRecentRooms()
			})
		}
	} catch (error) {
		recentRooms.hidden = true
	}
}

async function refreshNodes() {
	if (!window.go || !window.go.main || !window.go.main.App) {
		setTimeout(refreshNodes, 300);
		return
	}
	try {
		const nodes = await window.go.main.App.ListNodes();
		status.textContent = `${nodes.length} 个在线节点`;
		const local = nodes.find(node => node.local);
		if (local && !initialisedName) {
			nameInput.value = local.name;
			initialisedName = true
		}
		if (!nodes.length) {
			list.innerHTML = '<div class="empty">暂无在线节点</div>';
			return
		}
		list.innerHTML = nodes.map(node => {
			const addresses = node.addresses && node.addresses.length ? node.addresses : [node.address];
			return `
			<div class="node-row">
				<div class="node-icon">${escapeHTML(node.name.slice(0, 2).toUpperCase())}</div>
				<div>
					<div class="node-name">${escapeHTML(node.name)}${node.local ? '<span class="node-local">本机</span>' : ''}</div>
					<div class="node-addresses">${addresses.map(address => `
						<span>${node.local ? '局域网 IP ' : ''}${escapeHTML(address)}:${node.port}</span>
					`).join('')}</div>
				</div>
				<button type="button" data-url="${escapeHTML(node.url)}">连接</button>
			</div>
		`}).join('');
		for (const button of list.querySelectorAll('[data-url]')) {
			button.addEventListener('click', () => window.go.main.App.ConnectToNode(button.dataset.url))
		}
	} catch (error) {
		status.textContent = '节点服务未就绪'
	}
}

document.getElementById('refresh').addEventListener('click', async () => {
	await window.go.main.App.RefreshDiscovery();
	setTimeout(refreshNodes, 250)
});

document.getElementById('save-name').addEventListener('click', async () => {
	const saved = await window.go.main.App.SetNodeName(nameInput.value);
	if (!saved) {
		nameInput.focus();
		return
	}
	initialisedName = false;
	refreshNodes()
});

async function connectToAddress() {
	addressError.textContent = '';
	const connected = await window.go.main.App.ConnectToAddress(addressInput.value);
	if (!connected) {
		addressError.textContent = '请输入有效的 IPv4 地址或完整房间链接';
		addressInput.focus()
	}
}

document.getElementById('direct-connect').addEventListener('click', connectToAddress);
addressInput.addEventListener('keydown', event => {
	if (event.key === 'Enter') connectToAddress()
});

firewallButton.addEventListener('click', async () => {
	firewallButton.disabled = true;
	networkDetail.textContent = '等待 Windows 管理员确认';
	try {
		const configured = await window.go.main.App.ConfigureFirewall();
		if (!configured) networkDetail.textContent = '配置已取消或失败';
		else await refreshNetworkStatus()
	} finally {
		firewallButton.disabled = false
	}
});

firewallRemoveButton.addEventListener('click', async () => {
	firewallRemoveButton.disabled = true;
	networkDetail.textContent = '等待 Windows 管理员确认删除';
	try {
		const removed = await window.go.main.App.RemoveFirewall();
		if (!removed) networkDetail.textContent = '删除已取消或失败';
		else await refreshNetworkStatus()
	} finally {
		await refreshNetworkStatus()
	}
});

refreshNodes();
refreshNetworkStatus();
refreshRecentRooms();
setInterval(refreshNodes, 1500);
