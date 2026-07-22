// NodeCrypt core cryptographic client for secure chat
// NodeCrypt 安全聊天的核心加密客户端

import {
	sha256
} from 'js-sha256';
import {
	ec as elliptic
} from 'elliptic';
import {
	ModeOfOperation
} from 'aes-js';
import chacha from 'js-chacha20';
import {
	Buffer
} from 'buffer';
import { gcm } from '@noble/ciphers/aes.js';
import { pbkdf2Async } from '@noble/hashes/pbkdf2.js';
import { sha256 as nobleSha256 } from '@noble/hashes/sha2.js';
window.Buffer = Buffer;

// Main NodeCrypt class for secure communication
// 用于安全通信的 NodeCrypt 主类
class NodeCrypt {
	// Initialize NodeCrypt instance
	// 初始化 NodeCrypt 实例
	constructor(config = {}, callbacks = {}) {
		this.config = {
			rsaPublic: config.rsaPublic || '',
			wsAddress: config.wsAddress || '',
			reconnectDelay: config.reconnectDelay || 3000,
			pingInterval: config.pingInterval || 20000,
			connectionTimeout: config.connectionTimeout || 10000,
			debug: config.debug || false,
		};
		this.callbacks = {
			onServerClosed: callbacks.onServerClosed || null,
			onServerSecured: callbacks.onServerSecured || null,
			onClientSecured: callbacks.onClientSecured || null,
			onClientList: callbacks.onClientList || null,
			onClientMessage: callbacks.onClientMessage || null,
			onHistoryMessages: callbacks.onHistoryMessages || null,
			onJoinRejected: callbacks.onJoinRejected || null,
		};
		this.SERVER_KEY_STORAGE = 'nodecrypt_server_key';
		try {
			this.clientEc = new elliptic('curve25519');
			this.serverEc = new elliptic('p384')
		} catch (error) {
			this.logEvent('constructor', error, 'error')
		}
		this.serverKeys = null;
		this.serverShared = null;
		this.historyKeyPromise = null;
		this.historyAuthTokenPromise = null;
		this.joinConfirmed = false;
		this.hasJoined = false;
		this.credentials = null;
		this.connection = null;
		this.connectTimer = null;
		this.reconnect = null;
		this.ping = null;
		this.channel = {};
		this.setCredentials = this.setCredentials.bind(this);
		this.connect = this.connect.bind(this);
		this.destruct = this.destruct.bind(this);
		this.onOpen = this.onOpen.bind(this);
		this.onMessage = this.onMessage.bind(this);
		this.onError = this.onError.bind(this);
		this.onClose = this.onClose.bind(this);
		this.logEvent = this.logEvent.bind(this);
		this.isOpen = this.isOpen.bind(this);
		this.isClosed = this.isClosed.bind(this);
		this.startReconnect = this.startReconnect.bind(this);
		this.stopReconnect = this.stopReconnect.bind(this);
		this.startPing = this.startPing.bind(this);
		this.stopPing = this.stopPing.bind(this);
		this.disconnect = this.disconnect.bind(this);
		this.sendMessage = this.sendMessage.bind(this);
		this.sendChannelMessage = this.sendChannelMessage.bind(this);
		this.encryptServerMessage = this.encryptServerMessage.bind(this);
		this.decryptServerMessage = this.decryptServerMessage.bind(this);
		this.encryptClientMessage = this.encryptClientMessage.bind(this);
		this.decryptClientMessage = this.decryptClientMessage.bind(this)
	}

	// Set user credentials (username, channel, password)
	// 设置用户凭证（用户名、频道、密码）
	setCredentials(username, channel, password) {
		this.logEvent('setCredentials');
		try {
			const channelHash = sha256(channel);
			this.credentials = {
				username: username,
				channel: channelHash,
				password: sha256(password),
				roomScope: sha256(`nodecrypt-room-scope|${sha256(password)}`),
				userClaim: sha256(`nodecrypt-user|${channelHash}|${sha256(password)}|${username.trim().toLowerCase()}`)
			};
			const historySecrets = this.deriveHistorySecrets(password, channelHash);
			this.historyKeyPromise = historySecrets.then((secrets) => secrets.key);
			this.historyAuthTokenPromise = historySecrets.then((secrets) => secrets.authToken)
		} catch (error) {
			this.logEvent('setCredentials', error, 'error');
			return (false)
		}
		return (true)
	}

	// Connect to the server
	// 连接到服务器
	connect() {
		if (!this.credentials) {
			return (false)
		}
		this.logEvent('connect', this.config.wsAddress);
		this.stopReconnect();
		this.stopPing();
		this.stopConnectTimer();
		this.serverKeys = null;
		this.serverShared = null;
		this.channel = {};
		this.joinConfirmed = false;
		try {
			this.connection = new WebSocket(this.config.wsAddress);
			this.connection.onopen = this.onOpen;
			this.connection.onmessage = this.onMessage;
			this.connection.onerror = this.onError;
			this.connection.onclose = this.onClose;
			this.connectTimer = setTimeout(() => {
				if (this.joinConfirmed || !this.connection) return;
				if (!this.hasJoined) this.credentials = null;
				try {
					this.connection.close()
				} catch (error) {
					this.logEvent('connect-timeout', error, 'error');
					if (this.callbacks.onServerClosed) this.callbacks.onServerClosed()
				}
			}, this.config.connectionTimeout)
		} catch (error) {
			this.logEvent('connect', error, 'error');
			return (false)
		}
		return (true)
	}

	// Clean up and disconnect
	// 清理并断开连接
	destruct() {
		this.logEvent('destruct');
		this.stopReconnect();
		this.stopPing();
		this.stopConnectTimer();
		this.reconnect = null;
		this.ping = null;
		this.config = {
			rsaPublic: '',
			wsAddress: '',
			reconnectDelay: 3000,
			pingInterval: 15000,
			debug: false,
		};
		this.callbacks.onServerClosed = null;
		this.callbacks.onServerSecured = null;
		this.callbacks.onClientSecured = null;
		this.callbacks.onClientList = null;
		this.callbacks.onClientMessage = null;
		this.callbacks.onHistoryMessages = null;
		this.callbacks.onJoinRejected = null;
		this.clientEc = null;
		this.serverEc = null;
		this.serverKeys = null;
		this.serverShared = null;
		this.historyKeyPromise = null;
		this.historyAuthTokenPromise = null;
		this.joinConfirmed = false;
		this.hasJoined = false;
		this.credentials = null;
		this.connection.onopen = null;
		this.connection.onmessage = null;
		this.connection.onerror = null;
		this.connection.onclose = null;
		try {
			this.connection.removeAllListeners()
		} catch (error) {
			this.logEvent('destruct', error, 'error')
		}
		try {
			this.connection.close()
		} catch (error) {
			this.logEvent('destruct', error, 'error')
		}
		this.connection = null;
		this.channel = {};
		return (true)
	}

	// WebSocket open event handler
	// WebSocket 连接打开事件处理
	canUseSubtleCrypto() {
		return window.isSecureContext !== false && Boolean(crypto.subtle)
	}

	async onOpen() {
		this.logEvent('onOpen');
		this.startPing();
		try {
			if (this.canUseSubtleCrypto()) {
				this.serverKeys = await crypto.subtle.generateKey({
					name: 'ECDH',
					namedCurve: 'P-384'
				}, false, ['deriveKey', 'deriveBits'])
			} else {
				this.serverKeys = {
					fallback: true,
					keys: this.serverEc.genKeyPair()
				}
			}
			this.serverShared = null;
			const publicKey = this.serverKeys.fallback ?
				this.serverKeys.keys.getPublic().encode('hex', false) :
				Buffer.from(await crypto.subtle.exportKey('raw', this.serverKeys.publicKey)).toString('hex');
			this.sendMessage(publicKey)
		} catch (error) {
			this.logEvent('onOpen', error, 'error')
		}
	}

	async establishServerShared(publicKeyHex, signature) {
		if (this.serverKeys && this.serverKeys.fallback) {
			const publicKey = this.serverEc.keyFromPublic(publicKeyHex, 'hex').getPublic();
			this.serverShared = this.serverKeys.keys.derive(publicKey).toArrayLike(Buffer, 'be', 48).slice(8, 40);
			return true
		}
		if (!this.canUseSubtleCrypto()) return false;
		const verified = await crypto.subtle.verify({
			name: 'RSASSA-PKCS1-v1_5'
		}, await crypto.subtle.importKey('spki', Buffer.from(this.config.rsaPublic, 'base64'), {
			name: 'RSASSA-PKCS1-v1_5',
			hash: { name: 'SHA-256' }
		}, false, ['verify']), Buffer.from(signature, 'base64'), Buffer.from(publicKeyHex, 'hex'));
		if (!verified) return false;
		this.serverShared = Buffer.from(await crypto.subtle.deriveBits({
			name: 'ECDH',
			namedCurve: 'P-384',
			public: await crypto.subtle.importKey('raw', Buffer.from(publicKeyHex, 'hex'), {
				name: 'ECDH',
				namedCurve: 'P-384'
			}, true, [])
		}, this.serverKeys.privateKey, 384)).slice(8, 40);
		return true
	}

	// WebSocket message event handler
	// WebSocket 消息事件处理
	async onMessage(event) {
		if (!event || !this.isString(event.data)) {
			return
		}
		if (event.data === 'pong') {
			return
		}
		this.logEvent('onMessage', event.data);
		try {
			const data = JSON.parse(event.data);
			if (data.type === 'server-key') {
				const result = await this.handleServerKey(data.key);
				if (!result) {
					return
				}
			}
		} catch (e) {}
		if (!this.serverShared) {
			const parts = event.data.split('|');
			if (!parts[0] || !parts[1]) {
				return
			}
			try {
				if (await this.establishServerShared(parts[0], parts[1])) {
					this.sendMessage(this.encryptServerMessage({
						a: 'j',
						p: this.credentials.channel,
						g: this.credentials.roomScope,
						u: this.credentials.userClaim
					}, this.serverShared));
				}
			} catch (error) {
				this.logEvent('onMessage', error, 'error')
			}
			return
		}
		const serverDecrypted = this.decryptServerMessage(event.data, this.serverShared);
		this.logEvent('onMessage-server-decrypted', serverDecrypted);
		if (!this.isObject(serverDecrypted) || !this.isString(serverDecrypted.a)) {
			return
		}
		if (serverDecrypted.a === 'j' && this.isObject(serverDecrypted.p)) {
			if (serverDecrypted.p.o === true) {
				this.confirmJoin()
			} else if (serverDecrypted.p.o === false) {
				const reason = this.isString(serverDecrypted.p.r) ? serverDecrypted.p.r : 'join_rejected';
				if (this.callbacks.onJoinRejected) {
					try {
						this.callbacks.onJoinRejected(reason)
					} catch (error) {
						this.logEvent('onMessage-join-rejected-callback', error, 'error')
					}
				}
				this.credentials = null;
				this.stopReconnect();
				this.disconnect()
			}
			return
		}
		if (serverDecrypted.a === 'l' && this.isArray(serverDecrypted.p)) {
			// Compatibility fallback for servers released before join acknowledgements.
			this.confirmJoin();
			try {
				for (const clientId in this.channel) {
					if (serverDecrypted.p.indexOf(clientId) < 0) {
						delete(this.channel[clientId])
					}
				}
				let payloads = {};
				for (const clientId of serverDecrypted.p) {
					if (!this.channel[clientId]) {
						this.channel[clientId] = {
							username: null,
							keys: this.clientEc.genKeyPair(),
							shared: null,
						};
						payloads[clientId] = this.channel[clientId].keys.getPublic('hex')
					}
				}
				if (Object.keys(payloads).length > 0) {
					this.sendMessage(this.encryptServerMessage({
						a: 'w',
						p: payloads,
					}, this.serverShared))
				}
			} catch (error) {
				this.logEvent('onMessage-list', error, 'error')
			}
			if (this.callbacks.onClientList) {
				let clients = [];
				for (const clientId in this.channel) {
					clients.push({
						clientId,
						username: this.channel[clientId].username || '',
						pending: !this.channel[clientId].shared || !this.channel[clientId].username
					})
				}
				try {
					this.callbacks.onClientList(clients)
				} catch (error) {
					this.logEvent('onMessage-client-list-callback', error, 'error')
				}
			}
			return
		}
		if (serverDecrypted.a === 'h' && this.isObject(serverDecrypted.p) && this.isArray(serverDecrypted.p.m)) {
			await this.handleHistoryPage(serverDecrypted.p, 'server');
			return
		}
		if (!this.isString(serverDecrypted.p) || !this.isString(serverDecrypted.c)) {
			return
		}
		if (serverDecrypted.a === 'c' && (!this.channel[serverDecrypted.c] || !this.channel[serverDecrypted.c].shared)) {
			try {
				if (!this.channel[serverDecrypted.c]) {
					this.channel[serverDecrypted.c] = {
						username: null,
						keys: this.clientEc.genKeyPair(),
						shared: null,
					};
					this.sendMessage(this.encryptServerMessage({
						a: 'c',
						p: this.channel[serverDecrypted.c].keys.getPublic('hex'),
						c: serverDecrypted.c
					}, this.serverShared))
				}
				this.channel[serverDecrypted.c].shared = Buffer.from(this.xorHex(this.channel[serverDecrypted.c].keys.derive(this.clientEc.keyFromPublic(serverDecrypted.p, 'hex').getPublic()).toString('hex').padEnd(64, '8').substr(0, 64), this.credentials.password), 'hex');
				this.sendMessage(this.encryptServerMessage({
					a: 'c',
					p: this.encryptClientMessage({
						a: 'u',
						p: this.credentials.username
					}, this.channel[serverDecrypted.c].shared),
					c: serverDecrypted.c
				}, this.serverShared))
			} catch (error) {
				this.logEvent('onMessage-client', error, 'error')
			}
			return
		}
		if (serverDecrypted.a === 'c' && this.channel[serverDecrypted.c] && this.channel[serverDecrypted.c].shared) {
			const clientDecrypted = this.decryptClientMessage(serverDecrypted.p, this.channel[serverDecrypted.c].shared);
			this.logEvent('onMessage-client-decrypted', clientDecrypted);
			if (!this.isObject(clientDecrypted) || !this.isString(clientDecrypted.a)) {
				return
			}
			if (clientDecrypted.a === 'u' && this.isString(clientDecrypted.p) && clientDecrypted.p.match(/\S+/) && !this.channel[serverDecrypted.c].username) {
				this.channel[serverDecrypted.c].username = clientDecrypted.p.replace(/^\s+/, '').replace(/\s+$/, '');
				if (this.callbacks.onClientSecured) {
					try {
						this.callbacks.onClientSecured({
							clientId: serverDecrypted.c,
							username: this.channel[serverDecrypted.c].username
						})
					} catch (error) {
						this.logEvent('onMessage-client-secured-callback', error, 'error')
					}
				}
				return
			}			if (!this.channel[serverDecrypted.c].username) {
				return
			}
			if (clientDecrypted.a === 'm' && this.isString(clientDecrypted.t) && (this.isString(clientDecrypted.d) || this.isObject(clientDecrypted.d))) {
				const senderName = this.channel[serverDecrypted.c].username;
				const messageId = this.isString(clientDecrypted.i) ? clientDecrypted.i : null;
				const timestamp = Number.isFinite(clientDecrypted.ts) ? clientDecrypted.ts : null;
				const persistentGroupTypes = ['text', 'image', 'file_start', 'file_volume', 'file_complete'];
				if (messageId && timestamp && persistentGroupTypes.includes(clientDecrypted.t)) {
					this.storeLocalPlainHistory(senderName, clientDecrypted.t, clientDecrypted.d, {
						messageId,
						timestamp
					}).catch((error) => this.logEvent('storeIncomingLocalHistory', error, 'error'))
				}
				if (this.callbacks.onClientMessage) {
					try {
						this.callbacks.onClientMessage({
							clientId: serverDecrypted.c,
							username: senderName,
							type: clientDecrypted.t,
							data: clientDecrypted.d,
							messageId,
							timestamp
						})
					} catch (error) {
						this.logEvent('onMessage-client-message-callback', error, 'error')
					}
				}
				return
			}
		}
	}

	confirmJoin() {
		if (this.joinConfirmed) return;
		this.joinConfirmed = true;
		this.hasJoined = true;
		this.stopConnectTimer();
		if (this.callbacks.onServerSecured) {
			try {
				this.callbacks.onServerSecured()
			} catch (error) {
				this.logEvent('confirmJoin', error, 'error')
			}
		}
	}

	// WebSocket error event handler
	// WebSocket 错误事件处理
	async onError(event) {
		this.logEvent('onError', event, 'error');
		this.stopConnectTimer();
		this.disconnect();
		if (this.credentials) {
			this.startReconnect()
		}
		if (this.callbacks.onServerClosed) {
			try {
				this.callbacks.onServerClosed()
			} catch (error) {
				this.logEvent('onError-server-closed-callback', error, 'error')
			}
		}
	}

	// WebSocket close event handler
	// WebSocket 关闭事件处理
	async onClose(event) {
		this.logEvent('onClose', event);
		this.stopConnectTimer();
		this.disconnect();
		if (this.credentials) {
			this.startReconnect()
		}
		if (this.callbacks.onServerClosed) {
			try {
				this.callbacks.onServerClosed()
			} catch (error) {
				this.logEvent('onClose-server-closed-callback', error, 'error')
			}
		}
	}

	// Log events for debugging
	// 记录事件日志用于调试
	logEvent(source, message, level) {
		if (this.config.debug) {
			const date = new Date(),
				dateString = date.getFullYear() + '-' + ('0' + (date.getMonth() + 1)).slice(-2) + '-' + ('0' + date.getDate()).slice(-2) + ' ' + ('0' + date.getHours()).slice(-2) + ':' + ('0' + date.getMinutes()).slice(-2) + ':' + ('0' + date.getSeconds()).slice(-2);
			console.log('[' + dateString + ']', (level ? level.toUpperCase() : 'INFO'), source + (message ? ':' : ''), (message ? message : ''))
		}
	}

	// Check if connection is open
	// 检查连接是否已打开
	isOpen() {
		return (this.connection && this.connection.readyState && this.connection.readyState === WebSocket.OPEN ? true : false)
	}

	// Check if connection is closed
	// 检查连接是否已关闭
	isClosed() {
		return (!this.connection || this.connection.readyState === WebSocket.CLOSED ? true : false)
	}

	stopConnectTimer() {
		if (this.connectTimer) {
			clearTimeout(this.connectTimer);
			this.connectTimer = null
		}
	}

	// Start reconnect timer
	// 启动重连定时器
	startReconnect() {
		this.stopReconnect();
		this.logEvent('startReconnect');
		this.reconnect = setTimeout(() => {
			this.reconnect = null;
			this.connect()
		}, this.config.reconnectDelay)
	}

	// Stop reconnect timer
	// 停止重连定时器
	stopReconnect() {
		if (this.reconnect) {
			this.logEvent('stopReconnect');
			clearTimeout(this.reconnect);
			this.reconnect = null
		}
	}

	// Start ping timer
	// 启动心跳定时器
	startPing() {
		this.stopPing();
		this.logEvent('startPing');
		this.ping = setInterval(() => {
			this.sendMessage('ping')
		}, this.config.pingInterval)
	}

	// Stop ping timer
	// 停止心跳定时器
	stopPing() {
		if (this.ping) {
			this.logEvent('stopPing');
			clearInterval(this.ping);
			this.ping = null
		}
	}

	// Disconnect from server
	// 从服务器断开连接
	disconnect() {
		this.stopReconnect();
		this.stopPing();
		this.stopConnectTimer();
		if (!this.isClosed()) {
			try {
				this.logEvent('disconnect');
				this.connection.close()
			} catch (error) {
				this.logEvent('disconnect', error, 'error')
			}
		}
	}

	// Send a message to the server
	// 向服务器发送消息
	sendMessage(message) {
		try {
			if (this.isOpen()) {
				this.connection.send(message);
				return (true)
			}
		} catch (error) {
			this.logEvent('sendMessage', error, 'error')
		}
		return (false)
	}

	// Send a message to all channels
	// 向所有频道发送消息
	sendChannelMessage(type, data, metadata = {}) {
		if (this.serverShared) {
			try {
				let payloads = {};
				for (const clientId in this.channel) {
					if (this.channel[clientId].shared && this.channel[clientId].username) {
						const message = {
							a: 'm',
							t: type,
							d: data
						};
						if (metadata.messageId) message.i = metadata.messageId;
						if (metadata.timestamp) message.ts = metadata.timestamp;
						payloads[clientId] = this.encryptClientMessage(message, this.channel[clientId].shared);
						if (payloads[clientId].length === 0) {
							return (false)
						}
					}
				}
				if (Object.keys(payloads).length > 0) {
					const payload = this.encryptServerMessage({
						a: 'w',
						p: payloads,
					}, this.serverShared);
					if (!this.isOpen() || payload.length === 0 || payload.length > (8 * 1024 * 1024)) {
						return (false)
					}
					this.connection.send(payload)
				}
				return (true)
			} catch (error) {
				this.logEvent('sendChannelMessage', error, 'error')
			}
		}
		return (false)
	}

	// Send a group message in real time and persist an independently encrypted history copy.
	sendPersistentChannelMessage(type, data) {
		const metadata = {
			messageId: this.generateMessageId(),
			timestamp: Date.now()
		};
		const sent = this.sendChannelMessage(type, data, metadata);
		if (sent) {
			this.storeHistoryMessage(type, data, metadata).catch((error) => {
				this.logEvent('storeHistoryMessage', error, 'error')
			})
		}
		return {
			...metadata,
			sent
		}
	}

	async deriveHistorySecrets(password, channel) {
		const salt = Buffer.from(sha256(`nodecrypt-history:${channel}`), 'hex');
		const derived = await pbkdf2Async(nobleSha256, Buffer.from(password, 'utf8'), salt, {
			c: 210000,
			dkLen: 64
		});
		return {
			key: derived.slice(0, 32),
			authToken: Buffer.from(derived.slice(32)).toString('base64')
		}
	}

	getHistoryAdditionalData(messageId, timestamp) {
		return Buffer.from(`nodecrypt-history|1|${this.credentials.channel}|${messageId}|${timestamp}`, 'utf8')
	}

	generateMessageId() {
		if (crypto.randomUUID) return crypto.randomUUID();
		return Buffer.from(crypto.getRandomValues(new Uint8Array(16))).toString('hex')
	}

	async encryptHistoryMessage(type, data, metadata, username = this.credentials.username) {
		const key = await this.historyKeyPromise;
		const nonce = crypto.getRandomValues(new Uint8Array(12));
		const plaintext = Buffer.from(JSON.stringify({
			i: metadata.messageId,
			ts: metadata.timestamp,
			u: username,
			t: type,
			d: data
		}), 'utf8');
		const ciphertext = gcm(
			key,
			nonce,
			this.getHistoryAdditionalData(metadata.messageId, metadata.timestamp)
		).encrypt(plaintext);
		return {
			v: 1,
			i: metadata.messageId,
			ts: metadata.timestamp,
			n: Buffer.from(nonce).toString('base64'),
			c: Buffer.from(ciphertext).toString('base64')
		}
	}

	async decryptHistoryMessage(envelope) {
		if (!this.isObject(envelope) || envelope.v !== 1 || !this.isString(envelope.i) ||
			!Number.isFinite(envelope.ts) || !this.isString(envelope.n) || !this.isString(envelope.c)) {
			return null
		}
		try {
			const key = await this.historyKeyPromise;
			const plaintext = gcm(
				key,
				Buffer.from(envelope.n, 'base64'),
				this.getHistoryAdditionalData(envelope.i, envelope.ts)
			).decrypt(Buffer.from(envelope.c, 'base64'));
			const message = JSON.parse(Buffer.from(plaintext).toString('utf8'));
			if (message.i !== envelope.i || message.ts !== envelope.ts || !this.isString(message.u) ||
				!this.isString(message.t) || (!this.isString(message.d) && !this.isObject(message.d))) {
				return null
			}
			return {
				messageId: message.i,
				timestamp: message.ts,
				userName: message.u,
				type: message.t,
				data: message.d
			}
		} catch (error) {
			this.logEvent('decryptHistoryMessage', error, 'error');
			return null
		}
	}

	async storeHistoryMessage(type, data, metadata) {
		if (!this.serverShared || !this.isOpen()) return false;
		const [envelope, authToken] = await Promise.all([
			this.encryptHistoryMessage(type, data, metadata),
			this.historyAuthTokenPromise
		]);
		const payload = this.encryptServerMessage({
			a: 's',
			p: envelope,
			k: authToken
		}, this.serverShared);
		const remoteStored = Boolean(payload && payload.length <= (8 * 1024 * 1024) && this.sendMessage(payload));
		const localStored = await this.storeLocalHistoryEnvelope(envelope, authToken);
		return remoteStored || localStored
	}

	getLocalHistoryAPI() {
		const app = window.go && window.go.main && window.go.main.App;
		if (!app || typeof app.LoadLocalHistory !== 'function' || typeof app.StoreLocalHistory !== 'function') return null;
		return app
	}

	async storeLocalHistoryEnvelope(envelope, authToken) {
		const app = this.getLocalHistoryAPI();
		if (!app) return false;
		try {
			return await app.StoreLocalHistory(
				this.credentials.channel,
				authToken,
				envelope.v,
				envelope.i,
				envelope.ts,
				envelope.n,
				envelope.c
			)
		} catch (error) {
			this.logEvent('storeLocalHistoryEnvelope', error, 'error');
			return false
		}
	}

	async storeLocalPlainHistory(username, type, data, metadata) {
		const [envelope, authToken] = await Promise.all([
			this.encryptHistoryMessage(type, data, metadata, username),
			this.historyAuthTokenPromise
		]);
		return this.storeLocalHistoryEnvelope(envelope, authToken)
	}

	async requestLocalHistory(before = null, limit = 50) {
		const app = this.getLocalHistoryAPI();
		if (!app) return false;
		try {
			const authToken = await this.historyAuthTokenPromise;
			const page = await app.LoadLocalHistory(
				this.credentials.channel,
				authToken,
				Number.isFinite(before) ? before : 0,
				Math.max(1, Math.min(Number(limit) || 50, 100))
			);
			if (!this.isObject(page) || !this.isArray(page.m)) return false;
			await this.handleHistoryPage(page, 'local');
			return true
		} catch (error) {
			this.logEvent('requestLocalHistory', error, 'error');
			return false
		}
	}

	async requestHistory(before = null, limit = 50) {
		if (!this.serverShared || !this.isOpen()) return false;
		const authToken = await this.historyAuthTokenPromise;
		if (!this.serverShared || !this.isOpen()) return false;
		const explicitSources = this.isObject(before);
		const cursors = explicitSources ? before : { server: before, local: before };
		const payload = this.encryptServerMessage({
			a: 'h',
			k: authToken,
			p: {
				b: Number.isFinite(cursors.server) ? cursors.server : null,
				l: Math.max(1, Math.min(Number(limit) || 50, 100))
			}
		}, this.serverShared);
		const requestServer = !explicitSources || Object.prototype.hasOwnProperty.call(cursors, 'server');
		const requestLocal = !explicitSources || Object.prototype.hasOwnProperty.call(cursors, 'local');
		const serverRequested = requestServer ? this.sendMessage(payload) : false;
		const localRequested = requestLocal ? await this.requestLocalHistory(cursors.local, limit) : false;
		return serverRequested || localRequested
	}

	async handleHistoryPage(page, source = 'server') {
		const decrypted = await Promise.all(page.m.map((envelope) => this.decryptHistoryMessage(envelope)));
		const messages = decrypted.filter(Boolean);
		if (this.callbacks.onHistoryMessages) {
			try {
				this.callbacks.onHistoryMessages({
					messages,
					before: Number.isFinite(page.b) ? page.b : null,
					hasMore: page.x === true,
					status: this.isString(page.r) ? page.r : 'ok',
					source,
					encryptedCount: page.m.length,
					decryptFailed: decrypted.length - messages.length
				})
			} catch (error) {
				this.logEvent('onHistoryMessages-callback', error, 'error')
			}
		}
	}

	// Encrypt a message for the server
	// 加密发送给服务器的消息
	encryptServerMessage(message, key) {
		let encrypted = '';
		try {
			message = Buffer.from(JSON.stringify(message), 'utf8');
			if ((message.length % 16) !== 0) {
				message = Buffer.from([...message, ...Buffer.alloc(16 - (message.length % 16))])
			}
			const iv = Buffer.from(crypto.getRandomValues(new Uint8Array(16)));
			const cipher = new ModeOfOperation.cbc(key, iv);
			encrypted = iv.toString('base64') + '|' + Buffer.from(cipher.encrypt(message)).toString('base64')
		} catch (error) {
			this.logEvent('encryptServerMessage', error, 'error')
		}
		return (encrypted)
	}

	// Decrypt a message from the server
	// 解密来自服务器的消息
	decryptServerMessage(message, key) {
		let decrypted = {};
		try {
			const parts = message.split('|');
			const decipher = new ModeOfOperation.cbc(key, Buffer.from(parts[0], 'base64'));
			decrypted = JSON.parse(Buffer.from(decipher.decrypt(Buffer.from(parts[1], 'base64'))).toString('utf8').replace(/\0+$/, ''))
		} catch (error) {
			this.logEvent('decryptServerMessage', error, 'error')
		}
		return (decrypted)
	}

	// Encrypt a message for a client
	// 加密发送给客户端的消息
	encryptClientMessage(message, key) {
		let encrypted = '';
		try {
			message = Buffer.from(JSON.stringify(message), 'utf8');
			if ((message.length % 16) !== 0) {
				message = Buffer.from([...message, ...Buffer.alloc(16 - (message.length % 16))])
			}
			const iv = Buffer.from(crypto.getRandomValues(new Uint8Array(12)));
			const counter = Buffer.from(crypto.getRandomValues(new Uint8Array(4)));
			const cipher = new chacha(key, iv, counter.reduce((a, b) => a * b));
			encrypted = iv.toString('base64') + '|' + counter.toString('base64') + '|' + Buffer.from(cipher.encrypt(message)).toString('base64')
		} catch (error) {
			this.logEvent('encryptClientMessage', error, 'error')
		}
		return (encrypted)
	}

	// Decrypt a message from a client
	// 解密来自客户端的消息
	decryptClientMessage(message, key) {
		let decrypted = {};
		try {
			const parts = message.split('|');
			const decipher = new chacha(key, Buffer.from(parts[0], 'base64'), Buffer.from(parts[1], 'base64').reduce((a, b) => a * b));
			decrypted = JSON.parse(Buffer.from(decipher.decrypt(Buffer.from(parts[2], 'base64'))).toString('utf8').replace(/\0+$/, ''))
		} catch (error) {
			this.logEvent('decryptClientMessage', error, 'error')
		}
		return (decrypted)
	}

	// XOR two hex strings
	// 对两个十六进制字符串进行异或
	xorHex(a, b) {
		let result = '',
			hexLength = Math.min(a.length, b.length);
		for (let i = 0; i < hexLength; ++i) {
			result += (parseInt(a.charAt(i), 16) ^ parseInt(b.charAt(i), 16)).toString(16)
		}
		return (result)
	}

	// Check if value is a string
	// 检查值是否为字符串
	isString(value) {
		return (value && Object.prototype.toString.call(value) === '[object String]' ? true : false)
	}

	// Check if value is an array
	// 检查值是否为数组
	isArray(value) {
		return (value && Object.prototype.toString.call(value) === '[object Array]' ? true : false)
	}

	// Check if value is an object
	// 检查值是否为对象
	isObject(value) {
		return (value && Object.prototype.toString.call(value) === '[object Object]' ? true : false)
	}

	// Handle server public key
	// 处理服务器公钥
	async handleServerKey(serverKey) {
		this.logEvent('handleServerKey', 'Received server key');
		localStorage.removeItem(this.SERVER_KEY_STORAGE);
		localStorage.setItem(this.SERVER_KEY_STORAGE, serverKey);
		this.config.rsaPublic = serverKey;
		return true
	}
};

if (typeof window !== 'undefined') {
	window.NodeCrypt = NodeCrypt
}
