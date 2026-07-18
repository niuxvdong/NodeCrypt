import { spawnSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const releaseDirectory = join(root, 'release');
const workDirectory = join(root, 'build', 'exe');
const bundledEntry = join(workDirectory, 'nodecrypt-standalone.cjs');
const seaConfigPath = join(workDirectory, 'sea-config.json');
const seaBlobPath = join(workDirectory, 'nodecrypt-sea.blob');
const executablePath = join(releaseDirectory, 'NodeCrypt-LAN.exe');
const sentinelFuse = 'NODE_SEA_FUSE_fce680ab2cc467b6e072b8b5df1996b2';

if (process.platform !== 'win32') {
	throw new Error('The EXE packager must run on Windows.')
}

function run(command, args, options = {}) {
	const result = spawnSync(command, args, {
		cwd: root,
		stdio: 'inherit',
		shell: process.platform === 'win32' && command.toLowerCase().endsWith('.cmd'),
		...options
	});
	if (result.error) throw result.error;
	if (result.status !== 0) throw new Error(`${command} exited with code ${result.status}`)
}

function filesIn(directory) {
	const files = [];
	for (const entry of readdirSync(directory, { withFileTypes: true })) {
		const filename = join(directory, entry.name);
		if (entry.isDirectory()) files.push(...filesIn(filename));
		else if (entry.isFile()) files.push(filename)
	}
	return files
}

function stripWindowsSignature(filename) {
	let executable = readFileSync(filename);
	if (executable.length < 0x40 || executable.toString('ascii', 0, 2) !== 'MZ') return;
	const peOffset = executable.readUInt32LE(0x3c);
	const optionalHeader = peOffset + 24;
	if (peOffset + 24 >= executable.length || executable.toString('ascii', peOffset, peOffset + 4) !== 'PE\0\0') return;
	const magic = executable.readUInt16LE(optionalHeader);
	const dataDirectory = optionalHeader + (magic === 0x20b ? 112 : 96);
	const securityDirectory = dataDirectory + (8 * 4);
	if (securityDirectory + 8 > executable.length) return;
	const certificateOffset = executable.readUInt32LE(securityDirectory);
	const certificateSize = executable.readUInt32LE(securityDirectory + 4);
	executable.writeUInt32LE(0, securityDirectory);
	executable.writeUInt32LE(0, securityDirectory + 4);
	if (certificateOffset > 0 && certificateSize > 0 && certificateOffset + certificateSize === executable.length) {
		executable = executable.subarray(0, certificateOffset)
	}
	writeFileSync(filename, executable)
}

rmSync(workDirectory, { recursive: true, force: true });
mkdirSync(workDirectory, { recursive: true });
mkdirSync(releaseDirectory, { recursive: true });

run('npm.cmd', ['run', 'build']);

await build({
	absWorkingDir: root,
	entryPoints: ['server/standalone.js'],
	bundle: true,
	platform: 'node',
	format: 'cjs',
	target: 'node22',
	outfile: bundledEntry,
	external: ['node:*', 'bufferutil', 'utf-8-validate'],
	minify: false,
	sourcemap: false,
	logLevel: 'info'
});

const distDirectory = join(root, 'dist');
if (!existsSync(join(distDirectory, 'index.html'))) {
	throw new Error('Vite did not produce dist/index.html.')
}
const assets = {};
for (const filename of filesIn(distDirectory)) {
	const assetName = relative(distDirectory, filename).split(sep).join('/');
	assets[assetName] = filename
}
writeFileSync(seaConfigPath, JSON.stringify({
	main: bundledEntry,
	output: seaBlobPath,
	disableExperimentalSEAWarning: true,
	useSnapshot: false,
	useCodeCache: false,
	assets
}, null, 2));

run(process.execPath, ['--experimental-sea-config', seaConfigPath]);
copyFileSync(process.execPath, executablePath);
stripWindowsSignature(executablePath);

const postject = join(root, 'node_modules', '.bin', 'postject.cmd');
run(postject, [
	executablePath,
	'NODE_SEA_BLOB',
	seaBlobPath,
	'--sentinel-fuse',
	sentinelFuse
]);

writeFileSync(join(releaseDirectory, 'README.txt'), `NodeCrypt LAN

1. Double-click NodeCrypt-LAN.exe.
2. Allow access on Windows private networks when prompted.
3. The host browser opens automatically. Other users open the LAN URL printed in the console.
4. Keep the console window open while using NodeCrypt.
5. Accounts and encrypted chat history are stored in NodeCrypt-Data next to the EXE. Back up this folder.

Default port: 8788
Build command: npm run build:exe
`);

console.log(`\nCreated ${executablePath}`);
console.log('Double-click the EXE and allow Windows Firewall access for private networks.');
