import { spawnSync } from 'node:child_process';
import {
	copyFileSync,
	cpSync,
	existsSync,
	mkdirSync,
	readFileSync,
	readdirSync,
	rmSync,
	statSync,
	writeFileSync
} from 'node:fs';
import { createHash } from 'node:crypto';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const desktopDirectory = join(root, 'desktop');
const distDirectory = join(root, 'dist');
const nodeAssetsDirectory = join(desktopDirectory, 'nodeassets');
const releaseDirectory = join(root, 'release');
const outputName = 'NodeCrypt-Desktop.exe';
const builtExecutable = join(desktopDirectory, 'build', 'bin', outputName);
const releaseExecutable = join(releaseDirectory, outputName);

if (process.platform !== 'win32') {
	throw new Error('The desktop EXE packager must run on Windows.')
}

function run(command, args, cwd) {
	const goExecutable = findGo();
	const environment = {
		...process.env,
		GOPROXY: process.env.GOPROXY || 'https://goproxy.cn,direct',
		GOSUMDB: process.env.GOSUMDB || 'sum.golang.google.cn'
	};
	if (existsSync(goExecutable)) {
		const currentPath = process.env.PATH || process.env.Path || '';
		environment.PATH = `${dirname(goExecutable)};${currentPath}`;
		environment.Path = environment.PATH
	}
	const result = spawnSync(command, args, {
		cwd,
		stdio: 'inherit',
		shell: command.toLowerCase().endsWith('.cmd'),
		env: environment
	});
	if (result.error) throw result.error;
	if (result.status !== 0) throw new Error(`${command} exited with code ${result.status}`)
}

function findWails() {
	if (process.env.WAILS_BIN && existsSync(process.env.WAILS_BIN)) return process.env.WAILS_BIN;
	const userProfile = process.env.USERPROFILE || '';
	const installed = join(userProfile, 'go', 'bin', 'wails.exe');
	return existsSync(installed) ? installed : 'wails.exe'
}

function findGo() {
	if (process.env.GO_BIN && existsSync(process.env.GO_BIN)) return process.env.GO_BIN;
	const programFiles = process.env.ProgramFiles || 'C:\\Program Files';
	const installed = join(programFiles, 'Go', 'bin', 'go.exe');
	return existsSync(installed) ? installed : 'go.exe'
}

run('npm.cmd', ['run', 'build'], root);
if (!existsSync(join(distDirectory, 'index.html'))) {
	throw new Error('Vite did not produce dist/index.html.')
}

mkdirSync(nodeAssetsDirectory, { recursive: true });
for (const entry of readdirSync(nodeAssetsDirectory)) {
	if (entry !== '.gitkeep') rmSync(join(nodeAssetsDirectory, entry), { recursive: true, force: true })
}
cpSync(distDirectory, nodeAssetsDirectory, { recursive: true });

run(findWails(), [
	'build',
	'-clean',
	'-trimpath',
	'-s',
	'-compiler',
	findGo(),
	'-webview2',
	'browser',
	'-o',
	outputName
], desktopDirectory);

if (!existsSync(builtExecutable)) {
	throw new Error(`Wails did not produce ${builtExecutable}`)
}
mkdirSync(releaseDirectory, { recursive: true });
let packagedExecutable = releaseExecutable;
try {
	copyFileSync(builtExecutable, packagedExecutable)
} catch (error) {
	if (error && (error.code === 'EBUSY' || error.code === 'EPERM')) {
		packagedExecutable = join(releaseDirectory, 'NodeCrypt-Desktop-new.exe');
		copyFileSync(builtExecutable, packagedExecutable);
		console.warn(`\n${outputName} is running; wrote NodeCrypt-Desktop-new.exe instead.`)
	} else {
		throw error
	}
}

const executable = statSync(packagedExecutable);
const digest = createHash('sha256').update(readFileSync(packagedExecutable)).digest('hex');
writeFileSync(join(releaseDirectory, 'NodeCrypt-Desktop-README.txt'), `NodeCrypt Desktop

1. Double-click NodeCrypt-Desktop.exe on every LAN computer.
2. Allow private-network access if Windows Firewall asks.
3. Select the same node in both applications, then enter the same room.
4. Encrypted group history is stored on the selected node and in each desktop user's local SQLite under %APPDATA%\\NodeCrypt Desktop.
5. Use the native Node menu to return to node discovery.
6. If LAN connections are blocked, use Configure Firewall and approve the one-time Windows UAC prompt.

The application needs the Microsoft Edge WebView2 Runtime included with current Windows 10/11 systems.
Build command: npm run build:desktop
SHA-256: ${digest}
`, 'utf8');

console.log(`\nCreated ${packagedExecutable}`);
console.log(`Size: ${(executable.size / 1024 / 1024).toFixed(2)} MB`);
console.log(`SHA-256: ${digest}`);
