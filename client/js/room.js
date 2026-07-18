// Room management logic for NodeCrypt web client
// NodeCrypt 网页客户端的房间管理逻辑

import {
	createAvatarSVG
} from './util.avatar.js';
import {
	renderChatArea,
	addSystemMsg,
	updateChatInputStyle
} from './chat.js';
import {
	renderMainHeader,
	renderUserList
} from './ui.js';
import {
	escapeHTML
} from './util.string.js';
import {
	$id,
	createElement
} from './util.dom.js';
import { t } from './util.i18n.js';
let roomsData = [];
let activeRoomIndex = -1;

// Get a new room data object
// 获取一个新的房间数据对象
export function getNewRoomData() {
	return {
		roomName: '',
		userList: [],
		userMap: {},
		myId: null,
		myUserName: '',
		chat: null,
		messages: [],
		historyMessageIds: new Set(),
		historyBefore: { server: null, local: null },
		historyHasMore: { server: false, local: false },
		historyLoading: false,
		historyInitialized: false,
		historySourcesInitialized: new Set(),
		historyNotices: new Set(),
		historyFileAutoPages: { server: 0, local: 0 },
		prevUserList: [],
		knownUserIds: new Set(),
		unreadCount: 0,
		activeConversationId: 'group',
		privateConversations: {},
		privateChatTargetId: null,
		privateChatTargetName: null
	}
}

// Switch to another room by index
// 切换到指定索引的房间
export function switchRoom(index) {
	switchConversation(index, 'group')
}

export function switchConversation(index, conversationId = 'group') {
	if (index < 0 || index >= roomsData.length) return;
	activeRoomIndex = index;
	const rd = roomsData[index];
	rd.activeConversationId = conversationId;
	if (conversationId === 'group') {
		rd.privateChatTargetId = null;
		rd.privateChatTargetName = null;
		rd.unreadCount = 0
	} else {
		const conversation = rd.privateConversations[conversationId];
		if (!conversation) return switchConversation(index, 'group');
		rd.privateChatTargetId = conversation.clientId || null;
		rd.privateChatTargetName = conversation.name;
		conversation.unreadCount = 0
	}
	const sidebarUsername = document.getElementById('sidebar-username');
	if (sidebarUsername) sidebarUsername.textContent = rd.myUserName;
	setSidebarAvatar(rd.myUserName);
	renderRooms(index);
	renderMainHeader();
	renderUserList(false);
	renderChatArea();
	updateChatInputStyle()
}

// Set the sidebar avatar
// 设置侧边栏头像
export function setSidebarAvatar(userName) {
	if (!userName) return;
	const svg = createAvatarSVG(userName);
	const el = $id('sidebar-user-avatar');
	if (el) {
		const cleanSvg = svg.replace(/<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/gi, '');
		el.innerHTML = cleanSvg
	}
}

// Render the room list
// 渲染房间列表
export function renderRooms(activeId = 0) {
	const roomList = $id('room-list');
	roomList.innerHTML = '';
	roomsData.forEach((rd, i) => {
		const div = createElement('div', {
			class: 'room group-conversation' + (i === activeId && rd.activeConversationId === 'group' ? ' active' : ''),
			onclick: () => switchConversation(i, 'group')
		});
		const safeRoomName = escapeHTML(rd.roomName);
		let unreadHtml = '';
		if (rd.unreadCount && i !== activeId) {
			unreadHtml = `<span class="room-unread-badge">${rd.unreadCount>99?'99+':rd.unreadCount}</span>`
		}
		div.innerHTML = `<div class="info"><div class="title">#${safeRoomName}</div></div>${unreadHtml}`;
		roomList.appendChild(div);
		const conversations = Object.values(rd.privateConversations)
			.sort((left, right) => (right.lastTimestamp || 0) - (left.lastTimestamp || 0));
		for (const conversation of conversations) {
			const privateDiv = createElement('div', {
				class: 'room private-conversation' + (i === activeId && rd.activeConversationId === conversation.id ? ' active' : '') + (!conversation.clientId ? ' offline' : ''),
				onclick: () => switchConversation(i, conversation.id)
			});
			const unread = conversation.unreadCount && !(i === activeId && rd.activeConversationId === conversation.id) ?
				`<span class="room-unread-badge">${conversation.unreadCount > 99 ? '99+' : conversation.unreadCount}</span>` : '';
			privateDiv.innerHTML = `<div class="conversation-symbol">@</div><div class="info"><div class="title">${escapeHTML(conversation.name)}</div><div class="lastmsg">${t('ui.private_chat', 'Private')} · #${safeRoomName}${conversation.clientId ? '' : ` · ${t('ui.offline', 'Offline')}`}</div></div>${unread}<button type="button" class="conversation-close" title="${t('ui.close_private', 'Close private chat')}" aria-label="${t('ui.close_private', 'Close private chat')}">×</button>`;
			privateDiv.querySelector('.conversation-close').onclick = (event) => {
				event.stopPropagation();
				delete rd.privateConversations[conversation.id];
				if (rd.activeConversationId === conversation.id) switchConversation(i, 'group');
				else renderRooms(activeRoomIndex)
			};
			roomList.appendChild(privateDiv)
		}
	})
}

function privateConversationID(name) {
	return `private:${String(name || '').trim().toLowerCase()}`
}

function ensurePrivateConversation(rd, targetId, targetName) {
	const id = privateConversationID(targetName);
	let conversation = rd.privateConversations[id];
	if (!conversation) {
		conversation = { id, name: targetName, clientId: targetId || null, unreadCount: 0, lastTimestamp: Date.now() };
		rd.privateConversations[id] = conversation
	} else {
		conversation.name = targetName || conversation.name;
		if (targetId) conversation.clientId = targetId
	}
	return conversation
}

// Join a room
// 加入一个房间
export function joinRoom(userName, roomName, password, modal = null, onResult) {
	const newRd = getNewRoomData();
	newRd.roomName = roomName;
	newRd.myUserName = userName;
	newRd.password = password;
	roomsData.push(newRd);
	const idx = roomsData.length - 1;
	switchRoom(idx);
	const sidebarUsername = $id('sidebar-username');
	if (sidebarUsername) sidebarUsername.textContent = userName;
	setSidebarAvatar(userName);
	let closed = false;
	const callbacks = {
		onServerClosed: () => {
			setStatus('Node connection closed');
			if (onResult && !closed) {
				closed = true;
			onResult(false)
			}
		},
		onJoinRejected: (reason) => {
			roomsData.splice(idx, 1);
			activeRoomIndex = roomsData.length ? Math.min(idx, roomsData.length - 1) : -1;
			renderRooms(activeRoomIndex);
			if (onResult && !closed) {
				closed = true;
				onResult(false, reason)
			}
		},
		onServerSecured: () => {
			if (modal) modal.remove();
			else {
				const loginContainer = $id('login-container');
				if (loginContainer) loginContainer.style.display = 'none';
				const chatContainer = $id('chat-container');
				if (chatContainer) chatContainer.style.display = '';
				

			}
			if (onResult && !closed) {
				closed = true;
				onResult(true)
			}
			const desktopApp = window.go && window.go.main && window.go.main.App;
			if (desktopApp && typeof desktopApp.SaveRecentRoom === 'function') {
				desktopApp.SaveRecentRoom(window.nodeCryptEndpoint || '', userName, roomName, password)
					.catch((error) => console.warn('Unable to save recent room', error))
			}
			addSystemMsg(t('system.secured', 'connection secured'));
			newRd.historyLoading = true;
			chatInst.requestHistory().then((sent) => {
				if (!sent) newRd.historyLoading = false
			})
		},
		onClientSecured: (user) => handleClientSecured(idx, user),
		onClientList: (list, selfId) => handleClientList(idx, list, selfId),
		onClientLeft: (clientId) => handleClientLeft(idx, clientId),
		onClientMessage: (msg) => handleClientMessage(idx, msg),
		onHistoryMessages: (page) => handleHistoryMessages(idx, page)
	};
	const chatInst = new window.NodeCrypt(window.config, callbacks);
	chatInst.setCredentials(userName, roomName, password);
	chatInst.connect();
	roomsData[idx].chat = chatInst
}

// Handle the client list update
// 处理客户端列表更新
export function handleClientList(idx, list, selfId) {
	const rd = roomsData[idx];
	if (!rd) return;
	const oldUserIds = new Set((rd.userList || []).map(u => u.clientId));
	const newUserIds = new Set(list.map(u => u.clientId));
	for (const oldId of oldUserIds) {
		if (!newUserIds.has(oldId)) {
			handleClientLeft(idx, oldId)
		}
	}
	rd.userList = list;
	rd.userMap = {};
	list.forEach(u => {
		rd.userMap[u.clientId] = u
	});
	rd.myId = selfId || '__nodecrypt_self__';
	if (activeRoomIndex === idx) {
		renderUserList(false);
		renderMainHeader();
		renderRooms(activeRoomIndex)
	}
	rd.initCount = (rd.initCount || 0) + 1;
	if (rd.initCount === 2) {
		rd.isInitialized = true;
		rd.knownUserIds = new Set(list.map(u => u.clientId))
	}
}

// Handle client secured event
// 处理客户端安全连接事件
export function handleClientSecured(idx, user) {
	const rd = roomsData[idx];
	if (!rd) return;
	const securedName = user.userName || user.username || user.name || '';
	const existingConversation = rd.privateConversations[privateConversationID(securedName)];
	if (existingConversation) {
		existingConversation.clientId = user.clientId;
		if (rd.activeConversationId === existingConversation.id) rd.privateChatTargetId = user.clientId
	}
	rd.userMap[user.clientId] = user;
	const existingUserIndex = rd.userList.findIndex(u => u.clientId === user.clientId);
	if (existingUserIndex === -1) {
		rd.userList.push(user)
	} else {
		rd.userList[existingUserIndex] = user
	}
	if (activeRoomIndex === idx) {
		renderUserList(false);
		renderMainHeader();
		renderRooms(activeRoomIndex)
	}
	if (!rd.isInitialized) {
		return
	}
	const isNew = !rd.knownUserIds.has(user.clientId);
	if (isNew) {
		rd.knownUserIds.add(user.clientId);		const name = user.userName || user.username || user.name || t('ui.anonymous', 'Anonymous');
		const msg = `${name} ${t('system.joined', 'joined the conversation')}`;
			rd.messages.push({
				type: 'system',
				text: msg,
				timestamp: Date.now(),
				conversationId: 'group'
			});
			if (activeRoomIndex === idx && rd.activeConversationId === 'group') addSystemMsg(msg, true);
		if (window.notifyMessage) {
			window.notifyMessage(rd.roomName, 'system', msg)
		}
	}
}

// Handle client left event
// 处理客户端离开事件
export function handleClientLeft(idx, clientId) {
	const rd = roomsData[idx];
	if (!rd) return;
	for (const conversation of Object.values(rd.privateConversations)) {
		if (conversation.clientId === clientId) conversation.clientId = null
	}
	if (rd.privateChatTargetId === clientId) {
		rd.privateChatTargetId = null;
		if (activeRoomIndex === idx) {
			updateChatInputStyle()
		}
	}
	const user = rd.userMap[clientId];
	const name = user ? (user.userName || user.username || user.name || 'Anonymous') : 'Anonymous';
	const msg = `${name} ${t('system.left', 'left the conversation')}`;
	rd.messages.push({
		type: 'system',
		text: msg,
		timestamp: Date.now(),
		conversationId: 'group'
	});
	if (activeRoomIndex === idx && rd.activeConversationId === 'group') addSystemMsg(msg, true);
	rd.userList = rd.userList.filter(u => u.clientId !== clientId);
	delete rd.userMap[clientId];
	if (activeRoomIndex === idx) {
		renderUserList(false);
		renderMainHeader();
		renderRooms(activeRoomIndex)
	}
}

// Merge decrypted SQLite history into the current in-memory room.
export function handleHistoryMessages(idx, page) {
	const rd = roomsData[idx];
	if (!rd || !page || !Array.isArray(page.messages)) return;
	const source = page.source === 'local' ? 'local' : 'server';
	const chatArea = activeRoomIndex === idx ? $id('chat-area') : null;
	const previousHeight = chatArea ? chatArea.scrollHeight : 0;
	const loadingOlder = rd.historySourcesInitialized.has(source);
	for (const msg of page.messages) {
		if (!msg.messageId || rd.historyMessageIds.has(msg.messageId)) continue;
		rd.historyMessageIds.add(msg.messageId);
		const isMine = msg.userName === rd.myUserName;
		if (msg.type && msg.type.startsWith('file_')) {
			if (window.handleFileMessage && msg.data) {
				window.handleFileMessage(msg.data, false, true, true)
			}
			if (msg.type !== 'file_start') continue;
			rd.messages.push({
				type: isMine ? 'me' : 'other',
				text: msg.data,
				userName: msg.userName,
				avatar: msg.userName,
				msgType: 'file',
				timestamp: msg.timestamp,
				messageId: msg.messageId,
				conversationId: 'group'
			});
			continue
		}
		rd.messages.push({
			type: isMine ? 'me' : 'other',
			text: msg.data,
			userName: msg.userName,
			avatar: msg.userName,
			msgType: msg.type || 'text',
			timestamp: msg.timestamp,
			messageId: msg.messageId,
			conversationId: 'group'
		})
	}
	rd.messages.sort((a, b) => (a.timestamp || 0) - (b.timestamp || 0));
	rd.historyBefore[source] = page.before;
	rd.historyHasMore[source] = page.hasMore === true;
	rd.historySourcesInitialized.add(source);
	rd.historyLoading = false;
	rd.historyInitialized = true;
	let notice = '';
	if (page.status === 'password_mismatch') {
		notice = source === 'local' ?
			t('history.local_password_mismatch', '本机存在该房间的历史记录，但房间密码不一致') :
			t('history.node_password_mismatch', '当前节点存在该房间的历史记录，但房间密码不一致')
	} else if (page.status === 'query_failed' || page.status === 'unavailable') {
		notice = source === 'local' ?
			t('history.local_load_failed', '本机历史记录读取失败') :
			t('history.node_load_failed', '当前节点历史记录读取失败')
	} else if (page.decryptFailed > 0) {
		notice = t('history.decrypt_failed', '部分历史记录无法解密，请确认房间密码未变更')
	}
	const noticeKey = `${source}:${page.status}:${page.decryptFailed > 0}`;
	if (notice && !rd.historyNotices.has(noticeKey)) {
		rd.historyNotices.add(noticeKey);
		rd.messages.push({ type: 'system', text: notice, timestamp: Date.now(), conversationId: 'group' })
	}
	if (activeRoomIndex === idx) {
		renderChatArea();
		if (loadingOlder && chatArea) {
			chatArea.scrollTop = Math.max(0, chatArea.scrollHeight - previousHeight)
		}
	}
	if (window.hasIncompleteFileHistory && window.hasIncompleteFileHistory() && page.hasMore === true &&
		Number.isFinite(page.before) && rd.historyFileAutoPages[source] < 12) {
		rd.historyFileAutoPages[source]++;
		setTimeout(() => rd.chat && rd.chat.requestHistory({ [source]: page.before }), 0)
	}
}

// Handle client message event
// 处理客户端消息事件
export function handleClientMessage(idx, msg) {
	const newRd = roomsData[idx];
	if (!newRd) return;
	const incomingType = msg.type || 'text';
	if (msg.clientId === newRd.myId && msg.userName === newRd.myUserName && !incomingType.includes('_private')) {
		return;
	}
	if (msg.messageId) {
		if (newRd.historyMessageIds.has(msg.messageId)) return;
		newRd.historyMessageIds.add(msg.messageId)
	}

	let msgType = incomingType;
	let realUserName = msg.userName || msg.username;
	if (!realUserName && msg.clientId && newRd.userMap[msg.clientId]) {
		realUserName = newRd.userMap[msg.clientId].userName || newRd.userMap[msg.clientId].username || newRd.userMap[msg.clientId].name
	}
	realUserName = realUserName || t('ui.anonymous', 'Anonymous');
	const isPrivate = msgType.includes('_private');
	const privateConversation = isPrivate ? ensurePrivateConversation(newRd, msg.clientId, realUserName) : null;
	const conversationId = privateConversation ? privateConversation.id : 'group';
	const timestamp = msg.timestamp || Date.now();
	if (privateConversation) privateConversation.lastTimestamp = timestamp;
	const conversationActive = activeRoomIndex === idx && newRd.activeConversationId === conversationId;

	if (msgType.startsWith('file_')) {
		if (window.handleFileMessage && msg.data) {
			window.handleFileMessage(msg.data, isPrivate, true)
		}
		if (msgType === 'file_start' || msgType === 'file_start_private') {
			const historyMsgType = isPrivate ? 'file_private' : 'file';
			const fileId = msg.data && msg.data.fileId;
			if (fileId) {
				const messageAlreadyInHistory = newRd.messages.some(
					m => m.msgType === historyMsgType && m.text && m.text.fileId === fileId && m.conversationId === conversationId
				);
				if (!messageAlreadyInHistory) {
					newRd.messages.push({
						type: 'other',
						text: msg.data,
						userName: realUserName,
						avatar: realUserName,
						msgType: historyMsgType,
						timestamp,
						messageId: msg.messageId || null,
						conversationId
					});
				}
			}
			if (!conversationActive) {
				if (privateConversation) privateConversation.unreadCount++;
				else newRd.unreadCount++
			}
			const notificationMsgType = isPrivate ? 'private file' : 'file';
			if (window.notifyMessage && msg.data && msg.data.fileName) {
				window.notifyMessage(newRd.roomName, notificationMsgType, `${msg.data.fileName}`, realUserName)
			}
		}
		if (conversationActive && (msgType === 'file_start' || msgType === 'file_start_private')) renderChatArea();
		renderRooms(activeRoomIndex);
		return
	}

	// Handle image messages (both new and legacy formats)
	if (msgType === 'image' || msgType === 'image_private') {
		// Already has correct type
	} else if (!msgType.includes('_private')) {
		// Handle legacy image detection
		if (msg.data && typeof msg.data === 'string' && msg.data.startsWith('data:image/')) {
			msgType = 'image';
		} else if (msg.data && typeof msg.data === 'object' && msg.data.image) {
			msgType = 'image';
		}
	}
	roomsData[idx].messages.push({
		type: 'other',
		text: msg.data,
		userName: realUserName,
		avatar: realUserName,
		msgType: msgType,
		timestamp,
		messageId: msg.messageId || null,
		conversationId
	});
	if (conversationActive) {
		if (window.addOtherMsg) {
			window.addOtherMsg(msg.data, realUserName, realUserName, false, msgType, timestamp)
		}
	} else {
		if (privateConversation) privateConversation.unreadCount++;
		else newRd.unreadCount++;
		renderRooms(activeRoomIndex)
	}

	const notificationMsgType = msgType.includes('_private') ? `private ${msgType.split('_')[0]}` : msgType;
	if (window.notifyMessage) {
		window.notifyMessage(newRd.roomName, notificationMsgType, msg.data, realUserName);
	}
}

// Toggle private chat with a user
// 切换与某用户的私聊
export function togglePrivateChat(targetId, targetName) {
	const rd = roomsData[activeRoomIndex];
	if (!rd) return;
	const conversation = ensurePrivateConversation(rd, targetId, targetName);
	switchConversation(activeRoomIndex, conversation.id)
}


// Exit the current room
// 退出当前房间
export function exitRoom() {
	if (activeRoomIndex >= 0 && roomsData[activeRoomIndex]) {
		const chatInst = roomsData[activeRoomIndex].chat;
		if (chatInst && typeof chatInst.destruct === 'function') {
			chatInst.destruct()
		} else if (chatInst && typeof chatInst.disconnect === 'function') {
			chatInst.disconnect()
		}
		roomsData[activeRoomIndex].chat = null;
		roomsData.splice(activeRoomIndex, 1);
		if (roomsData.length > 0) {
			switchRoom(0);
			return true
		} else {
			return false
		}
	}
	return false
}

export { roomsData, activeRoomIndex };

// Listen for sidebar username update event
// 监听侧边栏用户名更新事件
window.addEventListener('updateSidebarUsername', () => {
	if (activeRoomIndex >= 0 && roomsData[activeRoomIndex]) {
		const rd = roomsData[activeRoomIndex];
		const sidebarUsername = document.getElementById('sidebar-username');
		if (sidebarUsername && rd.myUserName) {
			sidebarUsername.textContent = rd.myUserName;
		}
		// Also update the avatar to ensure consistency
		if (rd.myUserName) {
			setSidebarAvatar(rd.myUserName);
		}
	}
});
