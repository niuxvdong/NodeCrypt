import { t } from './util.i18n.js';

let authenticatedUser = null;

async function request(path, options = {}) {
	const response = await fetch(path, {
		credentials: 'same-origin',
		headers: options.body ? { 'Content-Type': 'application/json' } : {},
		...options
	});
	let body = {};
	try {
		body = await response.json()
	} catch (error) {}
	return { response, body }
}

function errorText(code) {
	const messages = {
		invalid_credentials: t('auth.invalid_credentials', 'Invalid username or password'),
		username_exists: t('auth.username_exists', 'Username already exists'),
		too_many_attempts: t('auth.too_many_attempts', 'Too many attempts. Try again later.'),
		request_too_large: t('auth.request_failed', 'Request failed'),
		invalid_json: t('auth.request_failed', 'Request failed')
	};
	return messages[code] || t('auth.request_failed', 'Request failed')
}

function setAuthenticatedUser(user) {
	authenticatedUser = user;
	window.nodeCryptAccount = user
}

function renderAccountGate(gate, complete) {
	let mode = 'login';
	gate.hidden = false;
	gate.innerHTML = `
		<div class="account-panel" role="dialog" aria-modal="true">
			<div class="account-brand">NodeCrypt</div>
			<div class="account-tabs" role="tablist">
				<button type="button" class="account-tab active" data-mode="login" role="tab">${t('auth.login', 'Login')}</button>
				<button type="button" class="account-tab" data-mode="register" role="tab">${t('auth.register', 'Register')}</button>
			</div>
			<form class="account-form" id="account-form">
				<label for="account-username">${t('auth.username', 'Account name')}</label>
				<input id="account-username" name="username" type="text" autocomplete="username" minlength="2" maxlength="20" pattern="[A-Za-z0-9_\\u4e00-\\u9fff]{2,20}" required>
				<label for="account-password">${t('auth.password', 'Account password')}</label>
				<input id="account-password" name="password" type="password" autocomplete="current-password" minlength="8" maxlength="72" required>
				<div class="account-confirm" hidden>
					<label for="account-password-confirm">${t('auth.confirm_password', 'Confirm password')}</label>
					<input id="account-password-confirm" name="passwordConfirm" type="password" autocomplete="new-password" minlength="8" maxlength="72">
				</div>
				<div class="account-error" id="account-error" role="alert"></div>
				<button type="submit" class="account-submit">${t('auth.login', 'Login')}</button>
			</form>
		</div>
	`;
	const form = gate.querySelector('#account-form');
	const confirmGroup = gate.querySelector('.account-confirm');
	const confirmInput = gate.querySelector('#account-password-confirm');
	const submit = gate.querySelector('.account-submit');
	const error = gate.querySelector('#account-error');
	for (const tab of gate.querySelectorAll('.account-tab')) {
		tab.addEventListener('click', () => {
			mode = tab.dataset.mode;
			for (const item of gate.querySelectorAll('.account-tab')) item.classList.toggle('active', item === tab);
			confirmGroup.hidden = mode !== 'register';
			confirmInput.required = mode === 'register';
			form.password.autocomplete = mode === 'register' ? 'new-password' : 'current-password';
			submit.textContent = mode === 'register' ? t('auth.register', 'Register') : t('auth.login', 'Login');
			error.textContent = ''
		})
	}
	form.addEventListener('submit', async (event) => {
		event.preventDefault();
		error.textContent = '';
		if (mode === 'register' && form.password.value !== form.passwordConfirm.value) {
			error.textContent = t('auth.password_mismatch', 'Passwords do not match');
			return
		}
		submit.disabled = true;
		try {
			const result = await request(`/api/auth/${mode}`, {
				method: 'POST',
				body: JSON.stringify({
					username: form.username.value.trim(),
					password: form.password.value
				})
			});
			if (!result.response.ok || !result.body.user) {
				error.textContent = errorText(result.body.error);
				return
			}
			setAuthenticatedUser(result.body.user);
			gate.remove();
			complete(result.body.user)
		} catch (requestError) {
			error.textContent = t('auth.request_failed', 'Request failed')
		} finally {
			submit.disabled = false
		}
	})
}

export async function initAccountGate() {
	const gate = document.getElementById('account-gate');
	if (!gate) return null;
	let runtime;
	try {
		const result = await request('/api/runtime');
		runtime = result.body
	} catch (error) {
		gate.remove();
		return null
	}
	if (!runtime.authRequired) {
		gate.remove();
		return null
	}
	try {
		const session = await request('/api/auth/session');
		if (session.response.ok && session.body.user) {
			setAuthenticatedUser(session.body.user);
			gate.remove();
			return session.body.user
		}
	} catch (error) {}
	return new Promise((resolve) => renderAccountGate(gate, resolve))
}

export async function logoutAccount() {
	try {
		await request('/api/auth/logout', { method: 'POST' })
	} finally {
		location.reload()
	}
}

export function getAuthenticatedUser() {
	return authenticatedUser
}
