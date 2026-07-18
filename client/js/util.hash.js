import { sha256 } from '@noble/hashes/sha2.js';

function bytesToHex(bytes) {
	return Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
}

export async function sha256Hex(data) {
	const bytes = data instanceof Uint8Array ? data : new Uint8Array(data);
	if (globalThis.crypto && globalThis.crypto.subtle) {
		const digest = await globalThis.crypto.subtle.digest('SHA-256', bytes);
		return bytesToHex(new Uint8Array(digest))
	}
	return bytesToHex(sha256(bytes))
}
