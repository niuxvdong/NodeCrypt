'use strict';

const crypto = require('crypto');
const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');
const { spawn } = require('child_process');
const { DatabaseSync } = require('node:sqlite');
const sea = require('node:sea');
process.env.WS_NO_BUFFER_UTIL = '1';
process.env.WS_NO_UTF_8_VALIDATE = '1';
const WebSocket = require('ws');

const isSea = sea.isSea();
const runtimeDirectory = isSea ? path.dirname(process.execPath) : path.resolve(__dirname, '..');
const dataDirectory = process.env.NODECRYPT_DATA_DIR || path.join(runtimeDirectory, 'NodeCrypt-Data');
const databasePath = process.env.NODECRYPT_DB_PATH || path.join(dataDirectory, 'nodecrypt.sqlite');
const staticDirectory = process.env.NODECRYPT_STATIC_DIR || path.join(runtimeDirectory, 'dist');
const internalPort = Number(process.env.NODECRYPT_INTERNAL_WS_PORT) || 18088;
const preferredPort = Number(process.env.NODECRYPT_PORT) || 8788;
const sessionCookieName = 'NodeCryptSession';
const sessionLifetimeSeconds = 7 * 24 * 60 * 60;

fs.mkdirSync(dataDirectory, { recursive: true });
process.env.NODECRYPT_DB_PATH = databasePath;
process.env.NODECRYPT_WS_HOST = '127.0.0.1';
process.env.NODECRYPT_WS_PORT = String(internalPort);

require('./server.js');

const authDb = new DatabaseSync(databasePath);
authDb.exec(`
	PRAGMA journal_mode = WAL;
	PRAGMA synchronous = NORMAL;
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL COLLATE NOCASE UNIQUE,
		password_salt BLOB NOT NULL,
		password_hash BLOB NOT NULL,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS sessions (
		token_hash TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_expiry ON sessions(expires_at);
`);

const insertUser = authDb.prepare(`
	INSERT INTO users (username, password_salt, password_hash, created_at) VALUES (?, ?, ?, ?)
`);
const selectUserByName = authDb.prepare(`
	SELECT id, username, password_salt, password_hash FROM users WHERE username = ? COLLATE NOCASE
`);
const insertSession = authDb.prepare(`
	INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)
`);
const deleteSession = authDb.prepare(`DELETE FROM sessions WHERE token_hash = ?`);
const deleteExpiredSessions = authDb.prepare(`DELETE FROM sessions WHERE expires_at <= ?`);
const selectSessionUser = authDb.prepare(`
	SELECT users.id, users.username, sessions.expires_at
	FROM sessions JOIN users ON users.id = sessions.user_id
	WHERE sessions.token_hash = ? AND sessions.expires_at > ?
`);

const contentTypes = {
	'.css': 'text/css; charset=utf-8',
	'.html': 'text/html; charset=utf-8',
	'.js': 'text/javascript; charset=utf-8',
	'.json': 'application/json; charset=utf-8',
	'.png': 'image/png',
	'.svg': 'image/svg+xml',
	'.webp': 'image/webp',
	'.woff2': 'font/woff2'
};

const rateLimits = new Map();

function jsonResponse(response, status, body, extraHeaders = {}) {
	response.writeHead(status, {
		'Content-Type': 'application/json; charset=utf-8',
		'Cache-Control': 'no-store',
		'X-Content-Type-Options': 'nosniff',
		...extraHeaders
	});
	response.end(JSON.stringify(body))
}

function parseCookies(request) {
	const result = {};
	for (const part of (request.headers.cookie || '').split(';')) {
		const separator = part.indexOf('=');
		if (separator < 0) continue;
		result[part.slice(0, separator).trim()] = decodeURIComponent(part.slice(separator + 1).trim())
	}
	return result
}

function hashToken(token) {
	return crypto.createHash('sha256').update(token).digest('hex')
}

function getSessionUser(request) {
	const token = parseCookies(request)[sessionCookieName];
	if (!token || token.length > 256) return null;
	return selectSessionUser.get(hashToken(token), Date.now()) || null
}

function createSession(userId) {
	deleteExpiredSessions.run(Date.now());
	const token = crypto.randomBytes(32).toString('base64url');
	const now = Date.now();
	insertSession.run(hashToken(token), userId, now, now + sessionLifetimeSeconds * 1000);
	return token
}

function sessionCookie(token, maxAge = sessionLifetimeSeconds) {
	return `${sessionCookieName}=${encodeURIComponent(token)}; HttpOnly; SameSite=Strict; Path=/; Max-Age=${maxAge}`
}

function passwordHash(password, salt) {
	return crypto.scryptSync(password, salt, 64, {
		N: 16384,
		r: 8,
		p: 1,
		maxmem: 64 * 1024 * 1024
	})
}

function validUsername(username) {
	return typeof username === 'string' && /^[A-Za-z0-9_\u4e00-\u9fff]{2,20}$/.test(username)
}

function validPassword(password) {
	return typeof password === 'string' && password.length >= 8 && password.length <= 72
}

function allowAuthAttempt(request) {
	const address = request.socket.remoteAddress || 'unknown';
	const now = Date.now();
	const current = rateLimits.get(address);
	if (!current || current.resetAt <= now) {
		rateLimits.set(address, { count: 1, resetAt: now + 10 * 60 * 1000 });
		return true
	}
	current.count += 1;
	return current.count <= 30
}

function readJson(request) {
	return new Promise((resolve, reject) => {
		let size = 0;
		const chunks = [];
		request.on('data', (chunk) => {
			size += chunk.length;
			if (size > 16 * 1024) {
				reject(new Error('request_too_large'));
				request.destroy();
				return
			}
			chunks.push(chunk)
		});
		request.on('end', () => {
			try {
				resolve(JSON.parse(Buffer.concat(chunks).toString('utf8') || '{}'))
			} catch (error) {
				reject(new Error('invalid_json'))
			}
		});
		request.on('error', reject)
	})
}

async function handleAuthApi(request, response, pathname) {
	if (pathname === '/api/runtime' && request.method === 'GET') {
		jsonResponse(response, 200, { authRequired: true, mode: 'standalone' });
		return
	}
	if (pathname === '/api/auth/session' && request.method === 'GET') {
		const user = getSessionUser(request);
		jsonResponse(response, user ? 200 : 401, user ? {
			authenticated: true,
			user: { id: Number(user.id), username: user.username }
		} : { authenticated: false });
		return
	}
	if (pathname === '/api/auth/logout' && request.method === 'POST') {
		const token = parseCookies(request)[sessionCookieName];
		if (token) deleteSession.run(hashToken(token));
		jsonResponse(response, 200, { ok: true }, { 'Set-Cookie': sessionCookie('', 0) });
		return
	}
	if ((pathname === '/api/auth/register' || pathname === '/api/auth/login') && request.method === 'POST') {
		if (!allowAuthAttempt(request)) {
			jsonResponse(response, 429, { error: 'too_many_attempts' });
			return
		}
		let body;
		try {
			body = await readJson(request)
		} catch (error) {
			jsonResponse(response, 400, { error: error.message });
			return
		}
		const username = typeof body.username === 'string' ? body.username.trim() : '';
		const password = body.password;
		if (!validUsername(username) || !validPassword(password)) {
			jsonResponse(response, 400, { error: 'invalid_credentials' });
			return
		}
		let user;
		if (pathname.endsWith('/register')) {
			if (selectUserByName.get(username)) {
				jsonResponse(response, 409, { error: 'username_exists' });
				return
			}
			const salt = crypto.randomBytes(16);
			try {
				const result = insertUser.run(username, salt, passwordHash(password, salt), Date.now());
				user = { id: Number(result.lastInsertRowid), username }
			} catch (error) {
				jsonResponse(response, 409, { error: 'username_exists' });
				return
			}
		} else {
			const stored = selectUserByName.get(username);
			if (!stored) {
				jsonResponse(response, 401, { error: 'invalid_credentials' });
				return
			}
			const actual = passwordHash(password, stored.password_salt);
			if (actual.length !== stored.password_hash.length || !crypto.timingSafeEqual(actual, stored.password_hash)) {
				jsonResponse(response, 401, { error: 'invalid_credentials' });
				return
			}
			user = { id: Number(stored.id), username: stored.username }
		}
		const token = createSession(user.id);
		jsonResponse(response, 200, { authenticated: true, user }, { 'Set-Cookie': sessionCookie(token) });
		return
	}
	jsonResponse(response, 404, { error: 'not_found' })
}

function safeAssetName(pathname) {
	let decoded;
	try {
		decoded = decodeURIComponent(pathname)
	} catch (error) {
		return null
	}
	const normalized = path.posix.normalize(decoded).replace(/^\/+/, '');
	if (!normalized || normalized === '.') return 'index.html';
	if (normalized.startsWith('..') || normalized.includes('/../')) return null;
	return normalized
}

function readAsset(assetName) {
	if (isSea) {
		try {
			return Buffer.from(sea.getAsset(assetName))
		} catch (error) {
			return null
		}
	}
	const filename = path.join(staticDirectory, ...assetName.split('/'));
	if (!filename.startsWith(path.resolve(staticDirectory))) return null;
	try {
		return fs.statSync(filename).isFile() ? fs.readFileSync(filename) : null
	} catch (error) {
		return null
	}
}

function serveAsset(response, assetName) {
	let body = readAsset(assetName);
	let resolvedName = assetName;
	if (!body) {
		body = readAsset('index.html');
		resolvedName = 'index.html'
	}
	if (!body) {
		response.writeHead(503, { 'Content-Type': 'text/plain; charset=utf-8' });
		response.end('Frontend assets are unavailable.');
		return
	}
	response.writeHead(200, {
		'Content-Type': contentTypes[path.extname(resolvedName).toLowerCase()] || 'application/octet-stream',
		'Cache-Control': resolvedName === 'index.html' ? 'no-cache' : 'public, max-age=31536000, immutable',
		'X-Content-Type-Options': 'nosniff',
		'Referrer-Policy': 'same-origin'
	});
	response.end(body)
}

const gateway = new WebSocket.Server({ noServer: true, perMessageDeflate: false });

gateway.on('connection', (client) => {
	const upstream = new WebSocket(`ws://127.0.0.1:${internalPort}`, { perMessageDeflate: false });
	const pending = [];
	client.on('message', (data, isBinary) => {
		if (upstream.readyState === WebSocket.OPEN) upstream.send(data, { binary: isBinary });
		else if (upstream.readyState === WebSocket.CONNECTING) pending.push([data, isBinary])
	});
	upstream.on('open', () => {
		for (const [data, isBinary] of pending.splice(0)) upstream.send(data, { binary: isBinary })
	});
	upstream.on('message', (data, isBinary) => {
		if (client.readyState === WebSocket.OPEN) client.send(data, { binary: isBinary })
	});
	client.on('close', () => upstream.close());
	upstream.on('close', () => client.close());
	client.on('error', () => upstream.terminate());
	upstream.on('error', () => client.terminate())
});

const httpServer = http.createServer(async (request, response) => {
	const url = new URL(request.url, `http://${request.headers.host || 'localhost'}`);
	if (url.pathname.startsWith('/api/')) {
		await handleAuthApi(request, response, url.pathname);
		return
	}
	if (request.method !== 'GET' && request.method !== 'HEAD') {
		response.writeHead(405, { Allow: 'GET, HEAD' });
		response.end();
		return
	}
	const assetName = safeAssetName(url.pathname);
	if (!assetName) {
		response.writeHead(400);
		response.end();
		return
	}
	serveAsset(response, assetName)
});

httpServer.on('upgrade', (request, socket, head) => {
	if (!getSessionUser(request)) {
		socket.write('HTTP/1.1 401 Unauthorized\r\nConnection: close\r\n\r\n');
		socket.destroy();
		return
	}
	gateway.handleUpgrade(request, socket, head, (client) => gateway.emit('connection', client, request))
});

function lanAddresses(port) {
	const addresses = [];
	for (const interfaces of Object.values(os.networkInterfaces())) {
		for (const address of interfaces || []) {
			if (address.family === 'IPv4' && !address.internal) addresses.push(`http://${address.address}:${port}`)
		}
	}
	return addresses
}

function openBrowser(url) {
	if (process.platform !== 'win32') return;
	const child = spawn('cmd.exe', ['/c', 'start', '', url], {
		detached: true,
		stdio: 'ignore',
		windowsHide: true
	});
	child.unref()
}

function listen(port) {
	httpServer.once('error', (error) => {
		if (error.code === 'EADDRINUSE' && port < preferredPort + 20) {
			listen(port + 1);
			return
		}
		console.error('Unable to start NodeCrypt:', error);
		process.exit(1)
	});
	httpServer.listen(port, '0.0.0.0', () => {
		const localUrl = `http://127.0.0.1:${port}`;
		console.log('\nNodeCrypt LAN is running');
		console.log('Local:   ', localUrl);
		for (const address of lanAddresses(port)) console.log('LAN:     ', address);
		console.log('Database:', databasePath);
		console.log('Keep this window open. Press Ctrl+C to stop.\n');
		if (isSea && process.env.NODECRYPT_OPEN_BROWSER !== '0') openBrowser(localUrl)
	})
}

listen(preferredPort);
